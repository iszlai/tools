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
	"go/token"
	"os"
	"sort"
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
}

// Loader loads the packages touched by a review: the ones containing the
// changed files, plus every package inside the same module that imports them
// (so FR-15's call graph can find callers anywhere in the module). The zero
// value is unusable; Dir is required.
type Loader struct {
	Dir      string   // module root (contains go.mod)
	Env      []string // environment for the `go` subprocess and packages.Load; nil = inherit the current process's
	CacheDir string   // where the import graph is persisted between runs; "" disables the disk cache
	Log      func(format string, args ...any)

	mu      sync.Mutex
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
func (l *Loader) Load(ctx context.Context, changedFiles []string) (*Result, error) {
	if l.Dir == "" {
		return nil, fmt.Errorf("golang: Loader.Dir is required")
	}

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
	res, err := loadScope(ctx, l.Dir, l.Env, scope)
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
func loadScope(ctx context.Context, dir string, env []string, scope []*pkgNode) (*Result, error) {
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
		BuildFlags: []string{"-mod=readonly"}, // never write go.mod/go.sum, never trust a stale vendor/; see listPackages
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
