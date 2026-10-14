package golang

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// TestBenchmarkCallGraph is FR-15's companion to FR-13's TestBenchmarkRepo
// (benchmark_test.go): point it at a real repo and changed-file set, and it
// times SSA construction and the VTA-or-CHA call graph build on top of an
// already-loaded Result. Skipped by default — `make check` never touches a
// real repo — and is not an assertion of any budget: the numbers that matter
// are recorded by hand in BENCHMARK.md.
//
//	FLOWREVIEW_BENCH_REPO=/Users/you/repos/ledger \
//	FLOWREVIEW_BENCH_FILES=a.go,b.go \
//	go test ./internal/lang/golang/ -run TestBenchmarkCallGraph -v
func TestBenchmarkCallGraph(t *testing.T) {
	repo := os.Getenv("FLOWREVIEW_BENCH_REPO")
	filesEnv := os.Getenv("FLOWREVIEW_BENCH_FILES")
	if repo == "" || filesEnv == "" {
		t.Skip("set FLOWREVIEW_BENCH_REPO and FLOWREVIEW_BENCH_FILES to run the FR-15 call graph benchmark")
	}
	changed := strings.Split(filesEnv, ",")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	l := &Loader{Dir: repo, CacheDir: t.TempDir(), Log: func(f string, a ...any) { t.Logf(f, a...) }}
	res, err := l.Load(ctx, changed)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Logf("load: %d packages (graph=%s load=%s)", len(res.Packages), res.Timings.Graph, res.Timings.Load)

	ssaStart := time.Now()
	prog, _ := ssautil.Packages(res.Packages, ssa.InstantiateGenerics)
	prog.Build()
	ssaElapsed := time.Since(ssaStart)
	t.Logf("ssa build: %s", ssaElapsed)

	cgStart := time.Now()
	graph, source := buildWithFallback(prog)
	cgElapsed := time.Since(cgStart)
	t.Logf("call graph (%s): %s, %d nodes", source, cgElapsed, len(graph.Nodes))

	implStart := time.Now()
	impls := buildImplementsEdges(res)
	implElapsed := time.Since(implStart)
	t.Logf("implements edges: %s, %d edges", implElapsed, len(impls))
}
