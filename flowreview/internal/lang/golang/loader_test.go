package golang

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func importPaths(res *Result) []string {
	out := make([]string, 0, len(res.Packages))
	for _, p := range res.Packages {
		out = append(out, p.PkgPath)
	}
	sort.Strings(out)
	return out
}

func TestLoad_ChangedFilePlusReverseImporter(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := importPaths(res)
	const (
		pa = "fixture.example/flowreviewtest/a"
		pb = "fixture.example/flowreviewtest/b"
		pc = "fixture.example/flowreviewtest/c"
	)
	has := func(path string) bool {
		for _, g := range got {
			if g == path {
				return true
			}
		}
		return false
	}
	if !has(pa) {
		t.Errorf("Packages %v missing changed package %s", got, pa)
	}
	if !has(pb) {
		t.Errorf("Packages %v missing reverse importer %s", got, pb)
	}
	if has(pc) {
		t.Errorf("Packages %v should not contain unrelated package %s", got, pc)
	}

	if _, ok := res.ByPath[pa]; !ok {
		t.Errorf("ByPath missing %s", pa)
	}
	if res.FileToPkg == nil {
		t.Fatal("FileToPkg is nil")
	}
	aFile := filepath.Join(l.Dir, "a", "a.go")
	if res.FileToPkg[aFile] == nil {
		t.Errorf("FileToPkg missing %s", aFile)
	}
}

// TestLoad_BrokenPackageDoesNotAbort loads a change to a, whose scope sweeps
// in "broken" (it imports a, so it is a reverse importer) alongside the
// healthy "b". broken has an unresolvable import and must fail to type-check
// without stopping a or b from loading cleanly.
func TestLoad_BrokenPackageDoesNotAbort(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("Load: %v (a broken unrelated package must not abort the whole load)", err)
	}

	const pbroken = "fixture.example/flowreviewtest/broken"
	brokenPkg, ok := res.ByPath[pbroken]
	if !ok {
		t.Fatalf("ByPath missing %s; packages: %v", pbroken, importPaths(res))
	}
	if len(brokenPkg.Errors) == 0 && len(res.Errors) == 0 {
		t.Fatal("expected broken's unresolved import to surface as a packages.Error")
	}

	const (
		pa = "fixture.example/flowreviewtest/a"
		pb = "fixture.example/flowreviewtest/b"
	)
	if pkg := res.ByPath[pa]; pkg == nil || len(pkg.Errors) != 0 {
		t.Errorf("a should load cleanly despite broken failing, got %+v", pkg)
	}
	if pkg := res.ByPath[pb]; pkg == nil || len(pkg.Errors) != 0 {
		t.Errorf("b should load cleanly despite broken failing, got %+v", pkg)
	}
}

func TestLoad_TestFilesIncluded(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := l.Load(ctx, []string{"d/d_test.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Packages) == 0 {
		t.Fatal("expected d's test package to be loaded")
	}
	testFile := filepath.Join(l.Dir, "d", "d_test.go")
	if res.FileToPkg[testFile] == nil {
		t.Errorf("FileToPkg missing the _test.go file %s; packages: %v", testFile, importPaths(res))
	}
}

func TestLoad_NoMatchingPackage(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := l.Load(ctx, []string{"nowhere/missing.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Packages) != 0 {
		t.Errorf("expected no packages, got %v", importPaths(res))
	}
}

// TestLoad_InMemoryCacheHit calls Load twice with the same changed files and
// an untouched fixture: the second call must return the exact same
// *packages.Package objects (not merely equal ones) because a real second
// packages.Load would allocate fresh objects. That pointer identity is the
// proof the in-memory cache, not a second load, served the request.
func TestLoad_InMemoryCacheHit(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	first, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if first.Timings.Load == 0 {
		t.Fatal("first Load should not be a cache hit (Timings.Load == 0)")
	}

	second, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if second.Timings.Load != 0 {
		t.Errorf("second Load should be a cache hit (Timings.Load == 0), got %s", second.Timings.Load)
	}
	if len(first.Packages) == 0 || len(second.Packages) == 0 {
		t.Fatal("expected packages in both results")
	}
	if first.Packages[0] != second.Packages[0] {
		t.Error("expected the cached Load to return the same *packages.Package objects")
	}
}

// TestLoad_WarmAndGet exercises the non-blocking API: Warm must return
// immediately and Get must observe the eventual result.
func TestLoad_WarmAndGet(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	l.Warm(ctx, []string{"a/a.go"})
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Warm should return immediately, took %s", elapsed)
	}

	res, err := l.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(res.Packages) == 0 {
		t.Fatal("expected packages from Get")
	}
}

func TestGet_WithoutWarm(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	if _, err := l.Get(context.Background()); err == nil {
		t.Fatal("expected an error calling Get before Warm")
	}
}

// TestImportGraph_DiskCacheIsActuallyUsed poisons the persisted graph cache
// with a package that could never come out of a real `go list` run on this
// fixture, then loads the graph again with a fresh Loader pointed at the same
// CacheDir. If the poisoned package shows up, the cache — not a fresh `go
// list` — served the request.
func TestImportGraph_DiskCacheIsActuallyUsed(t *testing.T) {
	cacheDir := t.TempDir()
	dir := fixtureDir(t)

	l1 := &Loader{Dir: dir, CacheDir: cacheDir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := l1.importGraph(ctx); err != nil {
		t.Fatalf("first importGraph: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(cacheDir, "graph-*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one graph cache file, got %v (err=%v)", matches, err)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var gf graphFile
	if err := json.Unmarshal(data, &gf); err != nil {
		t.Fatal(err)
	}
	const sentinel = "fixture.example/flowreviewtest/sentinel-from-cache"
	gf.Packages = append(gf.Packages, &pkgNode{ImportPath: sentinel, Dir: filepath.Join(dir, "sentinel")})
	poisoned, err := json.Marshal(gf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(matches[0], poisoned, 0o644); err != nil {
		t.Fatal(err)
	}

	l2 := &Loader{Dir: dir, CacheDir: cacheDir}
	g2, err := l2.importGraph(ctx)
	if err != nil {
		t.Fatalf("second importGraph: %v", err)
	}
	if _, ok := g2.packages[sentinel]; !ok {
		t.Error("expected the second importGraph to read the poisoned disk cache, not rebuild via `go list`")
	}
}

func TestManifestKey_StableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &Loader{Dir: dir}
	k1, err := l.manifestKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := l.manifestKey()
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Error("manifestKey should be stable when nothing changed")
	}

	// Touch the file with a later mtime so the key changes even though size
	// does not.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "x.go"), future, future); err != nil {
		t.Fatal(err)
	}
	k3, err := l.manifestKey()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k3 {
		t.Error("manifestKey should change when a file's mtime changes")
	}
}

func TestListPackages_IgnoresErrorsInUnrelatedPackage(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	nodes, err := l.listPackages(ctx)
	if err != nil {
		t.Fatalf("listPackages: %v (go list ./... failing in one package must not fail the whole listing)", err)
	}
	var sawBroken bool
	for _, n := range nodes {
		if strings.HasSuffix(n.ImportPath, "/broken") {
			sawBroken = true
		}
	}
	if !sawBroken {
		t.Error("expected the broken package to still be listed")
	}
}
