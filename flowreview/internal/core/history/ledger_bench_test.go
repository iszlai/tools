package history

import (
	"context"
	"os"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/git"
)

// ledgerRoot is Upvest's large monorepo, used once to sanity-check that
// mining stays fast on a ledger-sized history (PLAN.md FR-20's accept
// criterion). It is read-only: BenchmarkLedger only runs `git log`, and the
// cache it writes goes to a temp dir, never into the ledger checkout itself.
const ledgerRoot = "/Users/lehel/repos/ledger"

// BenchmarkLedger is not part of `go test ./...` (benchmarks only run with
// -bench) and skips itself when the ledger checkout isn't present, so it
// never affects `make check`. Run manually with:
//
//	go test -run=^$ -bench=BenchmarkLedger -benchtime=1x ./internal/core/history/
func BenchmarkLedger(b *testing.B) {
	if _, err := os.Stat(ledgerRoot); err != nil {
		b.Skipf("ledger checkout not found at %s: %v", ledgerRoot, err)
	}
	ctx := context.Background()
	repo, err := git.Open(ctx, ledgerRoot)
	if err != nil {
		b.Fatal(err)
	}
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m := New(repo, b.TempDir(), Options{}) // defaults: 365d / 5000 commits
		b.StartTimer()

		stats, err := m.Mine(ctx, head)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(stats.Commits), "commits")
		if stats.Degraded != "" {
			b.Logf("degraded: %s", stats.Degraded)
		}
	}
}
