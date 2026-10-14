# FR-20 benchmark: Mine on a ledger-sized history

Accept criterion (PLAN.md FR-20): mining must run under 2s on `ledger`-sized
history, or degrade with a note.

Run against `/Users/lehel/repos/ledger` (read-only; only `git log` is
invoked, and the cache is written to a temp dir, never into the checkout):

```
go test -run NoSuchTest -bench BenchmarkLedger -benchtime 1x -v ./internal/core/history/
```

Result (Apple M5, default Options — 365 days, cap 5000 commits):

```
BenchmarkLedger-10    1    669600376 ns/op    654.0 commits
```

**~0.67-0.83s** across repeated runs, mining **654 commits** (everything in
ledger's last 365 days — well below the 5000-commit cap, so `Degraded` is
empty). Comfortably under the 2s budget; no degradation needed at this repo's
current activity level.

If ledger's commit rate within a year ever exceeds ~5000, the `MaxCommits`
cap kicks in and `Stats.Degraded` reports e.g. `"capped at 5000 commits"`
(exercised directly in `history_test.go`'s
`TestMineDegradesWhenCappedBelowHistory`, which forces a low cap against the
small fixture repo).
