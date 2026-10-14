package golang

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// listFields are the only `go list -json` fields the graph needs. Asking for
// just these (instead of the full Package struct, which always includes the
// transitive Deps list) cuts a ~6 MB listing on a 150-package module down to
// a few hundred KB and the wall time roughly in half.
const listFields = "ImportPath,Dir,GoFiles,TestGoFiles,XTestGoFiles,Imports,TestImports,XTestImports"

// pkgNode is one package's direct edges, the slice of a `go list -json`
// Package the graph keeps.
type pkgNode struct {
	ImportPath   string   `json:"ImportPath"`
	Dir          string   `json:"Dir"`
	GoFiles      []string `json:"GoFiles"`
	TestGoFiles  []string `json:"TestGoFiles"`
	XTestGoFiles []string `json:"XTestGoFiles"`
	Imports      []string `json:"Imports"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
}

// files returns every .go file the package owns (non-test, test and
// external-test), as absolute paths.
func (n *pkgNode) files() []string {
	out := make([]string, 0, len(n.GoFiles)+len(n.TestGoFiles)+len(n.XTestGoFiles))
	for _, group := range [][]string{n.GoFiles, n.TestGoFiles, n.XTestGoFiles} {
		for _, f := range group {
			out = append(out, filepath.Join(n.Dir, f))
		}
	}
	return out
}

// importGraph is the direct, in-module import graph of one module, as of one
// `go list ./...` run (or the disk-cached equivalent of one).
type importGraph struct {
	packages map[string]*pkgNode // by import path
	byDir    map[string]string   // absolute package dir -> import path
	reverse  map[string][]string // import path -> direct in-module importers
}

// graphFile is what gets persisted to CacheDir: the listing plus the key it
// was built for, so a stale file found under a reused name is still checked.
type graphFile struct {
	Key      string     `json:"key"`
	Packages []*pkgNode `json:"packages"`
}

// importGraph builds (or loads from CacheDir) the in-module import graph for
// l.Dir. A disk hit skips the `go list` subprocess entirely; that is the only
// thing "warm" means for the graph phase — see BENCHMARK.md.
func (l *Loader) importGraph(ctx context.Context) (*importGraph, error) {
	key, keyErr := l.manifestKey()

	if l.CacheDir != "" && keyErr == nil {
		if nodes, ok := l.readGraphCache(key); ok {
			l.logf("golang: import graph cache hit (%s)", key[:12])
			return buildGraph(nodes), nil
		}
	}

	nodes, err := l.listPackages(ctx)
	if err != nil {
		return nil, err
	}

	if l.CacheDir != "" && keyErr == nil {
		l.writeGraphCache(key, nodes)
	}
	return buildGraph(nodes), nil
}

// listPackages runs `go list -mod=readonly -json=<listFields> ./...` in l.Dir
// and decodes the stream of Package objects it prints. -mod=readonly is
// forced regardless of l.Env: flowreview must never write to go.mod/go.sum in
// the reviewed repo, and it must never silently resolve from a vendor/
// directory that go.mod and vendor/modules.txt disagree about (a real repo
// in the wild had exactly that drift; without the flag `go list` refuses to
// run at all). A package that fails to resolve (a bad import, a parse error
// reachable from the import block) makes `go list` exit non-zero and write to
// stderr, but it still prints valid JSON for every other package first — so a
// non-zero exit with at least one decoded package is logged and otherwise
// ignored, not treated as a failure: the broken package simply never makes it
// into the graph, matching "errors in unrelated packages must not abort".
func (l *Loader) listPackages(ctx context.Context) ([]*pkgNode, error) {
	args := append([]string{"list", "-mod=readonly", "-json=" + listFields}, tagsFlag(l.Tags)...)
	cmd := exec.CommandContext(ctx, "go", append(args, "./...")...)
	cmd.Dir = l.Dir
	if l.Env != nil {
		cmd.Env = l.Env
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("golang: go list: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("golang: go list: %w", err)
	}

	var nodes []*pkgNode
	dec := json.NewDecoder(bufio.NewReaderSize(out, 1<<20))
	for dec.More() {
		var n pkgNode
		if err := dec.Decode(&n); err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("golang: decode go list output: %w", err)
		}
		nodes = append(nodes, &n)
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(nodes) == 0 {
			return nil, fmt.Errorf("golang: go list ./...: %s", msg)
		}
		l.logf("golang: go list ./... reported errors in some packages (ignored, %d packages still listed): %s", len(nodes), msg)
	}
	return nodes, nil
}

// buildGraph indexes a flat node list into byDir and the direct in-module
// reverse-import edges.
func buildGraph(nodes []*pkgNode) *importGraph {
	g := &importGraph{
		packages: make(map[string]*pkgNode, len(nodes)),
		byDir:    make(map[string]string, len(nodes)),
		reverse:  make(map[string][]string),
	}
	for _, n := range nodes {
		g.packages[n.ImportPath] = n
		g.byDir[n.Dir] = n.ImportPath
	}
	for _, n := range nodes {
		all := make([]string, 0, len(n.Imports)+len(n.TestImports)+len(n.XTestImports))
		all = append(all, n.Imports...)
		all = append(all, n.TestImports...)
		all = append(all, n.XTestImports...)
		for _, imp := range all {
			if _, inModule := g.packages[imp]; inModule {
				g.reverse[imp] = append(g.reverse[imp], n.ImportPath)
			}
		}
	}
	for k, v := range g.reverse {
		sort.Strings(v)
		g.reverse[k] = v
	}
	return g
}

// scopeFor resolves changedFiles (repo-relative paths) to the packages that
// contain them, plus every package inside the module that imports one of
// those packages, directly or transitively. A changed file outside any
// listed package (e.g. under a directory `go list` skips) is silently
// ignored — there is nothing to load for it.
func (g *importGraph) scopeFor(dir string, changedFiles []string) []*pkgNode {
	start := map[string]bool{}
	for _, rel := range changedFiles {
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, rel)
		}
		pkgDir := filepath.Dir(abs)
		if ip, ok := g.byDir[pkgDir]; ok {
			start[ip] = true
		}
	}
	if len(start) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(start))
	queue := make([]string, 0, len(start))
	for ip := range start {
		seen[ip] = true
		queue = append(queue, ip)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, r := range g.reverse[cur] {
			if !seen[r] {
				seen[r] = true
				queue = append(queue, r)
			}
		}
	}

	out := make([]*pkgNode, 0, len(seen))
	for ip := range seen {
		out = append(out, g.packages[ip])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportPath < out[j].ImportPath })
	return out
}

// manifestKey hashes go.mod, go.sum and every .go file's path/size/mtime
// under l.Dir (skipping vendor, dot-directories and node_modules) into one
// cache key. Content is not read — size+mtime is what the slice asked for —
// so an edit that preserves both bytes and the exact timestamp would be
// missed; that is a deliberate, documented approximation, see BENCHMARK.md.
func (l *Loader) manifestKey() (string, error) {
	h := sha256.New()
	h.Write([]byte("tags=" + strings.Join(l.Tags, ",") + "\n"))
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(l.Dir, name))
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		fmt.Fprintf(h, "%s:%d\n", name, len(data))
		h.Write(data)
	}

	type fileStamp struct {
		rel     string
		size    int64
		modNano int64
	}
	var stamps []fileStamp
	err := filepath.WalkDir(l.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules":
				return filepath.SkipDir
			}
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.Dir, path)
		if err != nil {
			return err
		}
		stamps = append(stamps, fileStamp{rel, info.Size(), info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].rel < stamps[j].rel })
	for _, s := range stamps {
		fmt.Fprintf(h, "%s:%d:%d\n", s.rel, s.size, s.modNano)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (l *Loader) graphCachePath(key string) string {
	return filepath.Join(l.CacheDir, "graph-"+key+".json")
}

func (l *Loader) readGraphCache(key string) ([]*pkgNode, bool) {
	data, err := os.ReadFile(l.graphCachePath(key))
	if err != nil {
		return nil, false
	}
	var gf graphFile
	if err := json.Unmarshal(data, &gf); err != nil || gf.Key != key {
		return nil, false
	}
	return gf.Packages, true
}

// writeGraphCache persists nodes and removes any other cached graph for this
// loader (an earlier go.mod/go.sum/file-set state), so CacheDir does not grow
// without bound across repo edits. Failure is logged, not fatal: the graph
// itself was already built successfully.
func (l *Loader) writeGraphCache(key string, nodes []*pkgNode) {
	if err := os.MkdirAll(l.CacheDir, 0o755); err != nil {
		l.logf("golang: cache dir: %v", err)
		return
	}
	matches, _ := filepath.Glob(filepath.Join(l.CacheDir, "graph-*.json"))
	for _, m := range matches {
		os.Remove(m)
	}

	data, err := json.Marshal(graphFile{Key: key, Packages: nodes})
	if err != nil {
		l.logf("golang: marshal graph cache: %v", err)
		return
	}
	tmp := l.graphCachePath(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		l.logf("golang: write graph cache: %v", err)
		return
	}
	if err := os.Rename(tmp, l.graphCachePath(key)); err != nil {
		l.logf("golang: rename graph cache: %v", err)
		os.Remove(tmp)
	}
}
