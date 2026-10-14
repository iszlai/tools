package golang

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBenchmarkRepo is the FR-13 spike harness: point it at a real repo with
// FLOWREVIEW_BENCH_REPO and a comma-separated list of repo-relative changed
// .go files with FLOWREVIEW_BENCH_FILES, and it logs cold and warm Graph/Load
// timings. It is skipped by default — `make check` never touches a real repo
// — and is not an assertion of the FR-13 budget: real repos vary, and the
// numbers that matter are recorded by hand in BENCHMARK.md.
//
// Example:
//
//	FLOWREVIEW_BENCH_REPO=/Users/you/repos/ledger \
//	FLOWREVIEW_BENCH_FILES=services/booking/booking-service/server/jobs.go,services/booking/booking-service/server/server.go \
//	go test ./internal/lang/golang/ -run TestBenchmarkRepo -v
func TestBenchmarkRepo(t *testing.T) {
	repo := os.Getenv("FLOWREVIEW_BENCH_REPO")
	filesEnv := os.Getenv("FLOWREVIEW_BENCH_FILES")
	if repo == "" || filesEnv == "" {
		t.Skip("set FLOWREVIEW_BENCH_REPO and FLOWREVIEW_BENCH_FILES to run the FR-13 spike benchmark")
	}
	changed := strings.Split(filesEnv, ",")

	cacheDir := t.TempDir()
	logf := func(format string, args ...any) { t.Logf(format, args...) }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("=== cold: no disk graph cache, no in-memory cache ===")
	cold := &Loader{Dir: repo, CacheDir: cacheDir, Log: logf}
	coldStart := time.Now()
	res, err := cold.Load(ctx, changed)
	if err != nil {
		t.Fatalf("cold Load: %v", err)
	}
	t.Logf("cold total: %s (graph=%s load=%s), %d packages, %d errors",
		time.Since(coldStart), res.Timings.Graph, res.Timings.Load, len(res.Packages), len(res.Errors))
	for _, e := range res.Errors {
		t.Logf("  package error: %s", e)
	}

	t.Log("=== warm: same process, same Loader (in-memory cache hit) ===")
	warmStart := time.Now()
	res2, err := cold.Load(ctx, changed)
	if err != nil {
		t.Fatalf("warm (same loader) Load: %v", err)
	}
	t.Logf("warm (same loader) total: %s (graph=%s load=%s)",
		time.Since(warmStart), res2.Timings.Graph, res2.Timings.Load)

	t.Log("=== warm: new process (new Loader), disk graph cache hit, go build cache hit ===")
	warmProc := &Loader{Dir: repo, CacheDir: cacheDir, Log: logf}
	warmProcStart := time.Now()
	res3, err := warmProc.Load(ctx, changed)
	if err != nil {
		t.Fatalf("warm (new loader) Load: %v", err)
	}
	t.Logf("warm (new loader) total: %s (graph=%s load=%s)",
		time.Since(warmProcStart), res3.Timings.Graph, res3.Timings.Load)
}
