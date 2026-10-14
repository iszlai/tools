// Package golang is the exact-tier LanguageProvider's package loader: it
// turns a set of changed files into the go/packages data FR-14 (symbols) and
// FR-15 (call graph) need, loading only the packages that contain those
// files and the packages inside the module that import them.
//
// Loading is split into two phases, each timed and logged separately: Graph
// (the in-module import graph, built by shelling out to `go list` or read
// back from a disk cache) and Load (the go/packages.Load call that actually
// parses and type-checks the resolved scope). See BENCHMARK.md for numbers
// on a small and a large real repo, and for what "warm" honestly means for
// each phase.
package golang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/build/constraint"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

// Timings records how long each phase of one Load took.
type Timings struct {
	Graph time.Duration
	Load  time.Duration
}

// Result is everything one Load produced.
type Result struct {
	Fset      *token.FileSet
	Packages  []*packages.Package          // the resolved scope: changed packages + their in-module reverse importers
	ByPath    map[string]*packages.Package // every package reachable from Packages, by import path (includes dependencies)
	FileToPkg map[string]*packages.Package // absolute file path -> owning package, for files in Packages
	Errors    []packages.Error             // collected from every package touched; a package with errors does not stop the others
	Timings   Timings
	// Root is the directory paths in CodeSymbol/Location are relative to:
	// the Loader.Dir the caller passed (the repo root), even when the
	// module that was loaded sits in a subdirectory of it.
	Root string
}

// Loader loads the packages touched by a review: the ones containing the
// changed files, plus every package inside the same module that imports them
// (so FR-15's call graph can find callers anywhere in the module). The zero
// value is unusable; Dir is required.
type Loader struct {
	Dir      string   // module root (contains go.mod)
	Env      []string // environment for the `go` subprocess and packages.Load; nil = inherit the current process's
	CacheDir string   // where the import graph is persisted between runs; "" disables the disk cache
	// Tags are build tags for `go list` and packages.Load. nil means
	// "derive from the changed files": the positive tags in their
	// //go:build lines, so a cucumber/integration test file behind a tag
	// is loaded instead of silently skipped.
	Tags []string
	Log  func(format string, args ...any)

	mu      sync.Mutex
	sub     map[string]*Loader // per nested module dir, see Load
	loaded  map[string]*Result // in-memory, keyed by scopeCacheKey(scope) — see scopeCacheKey
	current *future
}

func (l *Loader) logf(format string, args ...any) {
	if l.Log != nil {
		l.Log(format, args...)
	}
}

// future lets Warm start a Load in a goroutine and Get wait on it, possibly
// from a different goroutine, without re-running it.
type future struct {
	done chan struct{}
	res  *Result
	err  error
}

func (f *future) wait(ctx context.Context) (*Result, error) {
	select {
	case <-f.done:
		return f.res, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Warm starts Load(ctx, changedFiles) in the background and remembers it as
// the one Get will wait for. The UI must never block on a Load, so Warm
// returns immediately; call it as soon as a session's changed-file set is
// known.
func (l *Loader) Warm(ctx context.Context, changedFiles []string) {
	f := &future{done: make(chan struct{})}
	l.mu.Lock()
	l.current = f
	l.mu.Unlock()
	go func() {
		defer close(f.done)
		f.res, f.err = l.Load(ctx, changedFiles)
	}()
}

// Get waits for the Load started by the most recent Warm call. Calling Get
// without a prior Warm is an error: Get never starts a Load itself, so a
// caller can never be surprised by one blocking on the calling goroutine.
func (l *Loader) Get(ctx context.Context) (*Result, error) {
	l.mu.Lock()
	f := l.current
	l.mu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("golang: Get called before Warm")
	}
	return f.wait(ctx)
}

// Load finds the packages containing changedFiles (repo-relative .go paths)
// plus their in-module reverse importers, and loads exactly that set with
// go/packages. It is synchronous; Warm/Get is the non-blocking API the UI
// should use.
//
// Dir may be a repo root whose Go module lives in a subdirectory (a
// monorepo: services/x/go.mod). Load then loads the module holding the
// most changed files, and Result.Root stays Dir so every path still comes
// out repo-relative, matching the diff's own paths.
func (l *Loader) Load(ctx context.Context, changedFiles []string) (*Result, error) {
	if l.Dir == "" {
		return nil, fmt.Errorf("golang: Loader.Dir is required")
	}
	if l.Tags == nil {
		l.mu.Lock()
		l.Tags = buildTags(l.Dir, changedFiles)
		l.mu.Unlock()
	}
	if modDir, modFiles := nestedModule(l.Dir, changedFiles); modDir != "" {
		l.mu.Lock()
		if l.sub == nil {
			l.sub = map[string]*Loader{}
		}
		sub := l.sub[modDir]
		if sub == nil {
			sub = &Loader{Dir: modDir, Env: l.Env, CacheDir: l.CacheDir, Log: l.Log, Tags: l.Tags}
			l.sub[modDir] = sub
		}
		l.mu.Unlock()
		res, err := sub.Load(ctx, modFiles)
		if err != nil {
			return nil, err
		}
		out := *res
		out.Root = l.Dir
		return &out, nil
	}
	res, err := l.loadModule(ctx, changedFiles)
	if err != nil {
		return nil, err
	}
	if res.Root == "" {
		res.Root = l.Dir
	}
	return res, nil
}

// nestedModule returns the module directory below dir holding the most of
// changedFiles (nearest go.mod above each file, not above dir) and those
// files relative to it -- or "" when that module is dir itself (or there
// is none), meaning load dir as it is.
func nestedModule(dir string, changedFiles []string) (string, []string) {
	byMod := map[string][]string{}
	for _, f := range changedFiles {
		d := filepath.Dir(filepath.Join(dir, filepath.FromSlash(f)))
		for {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				break
			}
			if d == dir || len(d) <= len(dir) {
				d = ""
				break
			}
			d = filepath.Dir(d)
		}
		if d == "" || d == dir {
			byMod[dir] = append(byMod[dir], f)
			continue
		}
		rel, err := filepath.Rel(d, filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		byMod[d] = append(byMod[d], filepath.ToSlash(rel))
	}
	best := ""
	for m, fs := range byMod {
		if best == "" || len(fs) > len(byMod[best]) || (len(fs) == len(byMod[best]) && m < best) {
			best = m
		}
	}
	if best == "" || best == dir {
		return "", nil
	}
	return best, byMod[best]
}

// loadModule is Load for a Dir that is itself the module root.
func (l *Loader) loadModule(ctx context.Context, changedFiles []string) (*Result, error) {
	graphStart := time.Now()
	g, err := l.importGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("golang: build import graph: %w", err)
	}
	graphElapsed := time.Since(graphStart)
	l.logf("golang: graph phase: %d packages in %s", len(g.packages), graphElapsed)

	scope := g.scopeFor(l.Dir, changedFiles)
	if len(scope) == 0 {
		return &Result{
			Fset:      token.NewFileSet(),
			ByPath:    map[string]*packages.Package{},
			FileToPkg: map[string]*packages.Package{},
			Timings:   Timings{Graph: graphElapsed},
		}, nil
	}

	key, keyErr := scopeCacheKey(l.Dir, scope)
	key += "|tags=" + strings.Join(l.Tags, ",")
	if keyErr == nil {
		l.mu.Lock()
		cached := l.loaded[key]
		l.mu.Unlock()
		if cached != nil {
			l.logf("golang: load phase: cache hit, %d packages", len(cached.Packages))
			hit := *cached
			hit.Timings.Graph = graphElapsed
			hit.Timings.Load = 0
			return &hit, nil
		}
	}

	loadStart := time.Now()
	res, err := loadScope(ctx, l.Dir, l.Env, l.Tags, scope)
	if err != nil {
		return nil, err
	}
	res.Timings = Timings{Graph: graphElapsed, Load: time.Since(loadStart)}
	l.logf("golang: load phase: %d packages, %d errors in %s", len(res.Packages), len(res.Errors), res.Timings.Load)

	if keyErr == nil {
		l.mu.Lock()
		if l.loaded == nil {
			l.loaded = map[string]*Result{}
		}
		l.loaded[key] = res
		l.mu.Unlock()
	}
	return res, nil
}

// loadScope runs packages.Load for exactly the given in-module packages,
// with Tests enabled so FR-21's test-reach needs _test.go files and Test*
// funcs. go/packages never aborts a load because one package failed to
// type-check; it attaches the failure to that package's Errors instead, so
// collecting res.Errors from every package is enough to satisfy "errors in
// unrelated packages must not abort".
func loadScope(ctx context.Context, dir string, env, tags []string, scope []*pkgNode) (*Result, error) {
	patterns := make([]string, len(scope))
	for i, n := range scope {
		patterns[i] = n.ImportPath
	}

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir:        dir,
		Env:        env,
		Fset:       fset,
		Tests:      true,
		BuildFlags: append([]string{"-mod=readonly"}, tagsFlag(tags)...), // never write go.mod/go.sum, never trust a stale vendor/; see listPackages
	}

	top, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("golang: packages.Load: %w", err)
	}

	res := &Result{
		Fset:      fset,
		Packages:  top,
		ByPath:    map[string]*packages.Package{},
		FileToPkg: map[string]*packages.Package{},
	}
	packages.Visit(top, func(p *packages.Package) bool {
		res.ByPath[p.PkgPath] = p
		for _, f := range p.CompiledGoFiles {
			res.FileToPkg[f] = p
		}
		res.Errors = append(res.Errors, p.Errors...)
		return true
	}, nil)
	return res, nil
}

// scopeCacheKey is (module root, sorted file-hash set of the scope's files):
// the module root pins the key to this Loader's repo, and hashing every
// source file the scope's packages own means any edit inside the scope — not
// just to a changed file — invalidates the cache, exactly as a second
// packages.Load would see it.
func scopeCacheKey(dir string, scope []*pkgNode) (string, error) {
	var files []string
	for _, n := range scope {
		files = append(files, n.files()...)
	}
	sort.Strings(files)

	h := sha256.New()
	fmt.Fprintf(h, "dir:%s\n", dir)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s:%x\n", f, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// platformTags are build constraints the go command sets from the target
// platform and toolchain. Forcing one on with -tags would pull in files for
// another OS or arch next to this one's and break type-checking, so
// buildTags never passes them.
var platformTags = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true,
	"ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true,
	"wasip1": true, "windows": true, "unix": true, "386": true, "amd64": true, "arm": true, "arm64": true,
	"loong64": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "ppc64": true,
	"ppc64le": true, "riscv64": true, "s390x": true, "wasm": true, "cgo": true, "gc": true, "gccgo": true,
	"ignore": true,
}

// buildTags collects the positive, non-platform tags from the //go:build
// lines of changedFiles (paths relative to dir), sorted. A tag only ever
// negated (!integration) is left off.
func buildTags(dir string, changedFiles []string) []string {
	set := map[string]bool{}
	for _, f := range changedFiles {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				break
			}
			if !constraint.IsGoBuild(line) {
				continue
			}
			expr, err := constraint.Parse(line)
			if err != nil {
				continue
			}
			collectPositiveTags(expr, false, set)
		}
	}
	tags := []string{}
	for t := range set {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

func collectPositiveTags(e constraint.Expr, negated bool, set map[string]bool) {
	switch x := e.(type) {
	case *constraint.TagExpr:
		if !negated && !platformTags[x.Tag] && !strings.HasPrefix(x.Tag, "go1.") {
			set[x.Tag] = true
		}
	case *constraint.NotExpr:
		collectPositiveTags(x.X, !negated, set)
	case *constraint.AndExpr:
		collectPositiveTags(x.X, negated, set)
		collectPositiveTags(x.Y, negated, set)
	case *constraint.OrExpr:
		collectPositiveTags(x.X, negated, set)
		collectPositiveTags(x.Y, negated, set)
	}
}

// tagsFlag renders tags as go command flags: none, or one -tags=a,b.
func tagsFlag(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(tags, ",")}
}
