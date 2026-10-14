# FR-13 spike: `go/packages` load with cache

Machine: Apple M5, 32 GB RAM, macOS 27.0.1 (Darwin 27.0.0), `go version go1.26.1 darwin/arm64`
(ledger's own `go.mod` pins `toolchain go1.26.9`; `GOTOOLCHAIN=auto` switches to it automatically —
the numbers below are from that 1.26.9 toolchain building ledger).

Harness: `TestBenchmarkRepo` in `benchmark_test.go`, gated by `FLOWREVIEW_BENCH_REPO` /
`FLOWREVIEW_BENCH_FILES` (unset ⇒ skipped; never runs in `make check`).

## Repos

| Repo | Packages (`go list ./...`) | .go files (non-vendor/testdata) |
|---|---|---|
| `/Users/lehel/repos/ledger` (large, per PLAN.md) | 147 | ~808 |
| `/Users/lehel/repos/tools/taskhound` (small) | 1 | ~15 |

ledger needed `GOPROXY=http://goproxy.upvest.io` (already configured in this environment) to
resolve `github.com/toknapp/*` private modules; no VPN prompt was needed — the module cache on
this machine was already warm from normal ledger development. `go list ./...` scans ~800
`.go` files into 147 packages; this is the real package count of the module's own tree (most of
the 9.5k `.go` files under the ledger checkout are in `testdata/`, `vendor/`, or other worktrees,
none of which `./...` descends into).

Changed files used for ledger (3 real files from recent history, `git log --name-only -5`):

```
services/booking/booking-service/server/jobs.go
services/booking/booking-service/server/server.go
services/booking/booking-service/internal/v2/operation_types.go
```

These resolve to 2 changed packages (`.../server`, `.../internal/v2`) whose in-module reverse
closure (direct + transitive, including edges from `_test.go` files) is 80 packages out of 147 —
a large fraction of the module, because `internal/v2` is a widely-depended-on package.

Changed files used for taskhound: `store.go`, `sync.go` (both in the repo's single package).

## Numbers

| Repo | Scenario | Graph | Load | Total | Packages |
|---|---|---|---|---|---|
| ledger | cold (no disk graph cache, no in-memory cache) | 745 ms | 3.66 s | **4.43 s** | 151 `*packages.Package` (80 import paths × test variants) |
| ledger | warm, same process & Loader (in-memory `Result` cache hit) | 14.6 ms | 0 s | **34 ms** | same objects, pointer-identical |
| ledger | warm, new process (disk graph cache hit, Go build cache hit, no in-memory `Result`) | 13 ms | 2.63 s | **2.67 s** | 151 |
| taskhound | cold | 33.5 ms | 490 ms | **524 ms** | 3 |
| taskhound | warm, same process & Loader | 0.9 ms | 0 s | **1.3 ms** | same objects |
| taskhound | warm, new process | 0.7 ms | 459 ms | **460 ms** | 3 |

Acceptance target: changed+reverse load < 10 s cold, < 1 s warm.

- **Cold**: both repos are well inside budget (ledger 4.43 s, taskhound 0.52 s).
- **Warm, same process**: both repos are far inside budget (ledger 34 ms, taskhound 1.3 ms). This
  is the realistic "warm" path for daily use — flowreview opens a repo once per review session
  (that's the cold cost above) and then every further query in that session reuses the same
  `Loader`, hitting the in-memory `Result` cache.
- **Warm, new process**: ledger's new-process number (2.67 s) is **not** under 1 s, even though
  the disk-persisted import graph is hit (13 ms instead of 745 ms — a real 50× win on the graph
  phase). The 2.6 s is almost entirely `packages.Load`'s own `go list`-based type-checking of the
  80-package scope, which cannot be skipped without serializing `*packages.Package` across
  processes — the slice's own premise is that "results can't be serialized cheaply", so this is
  the honest limit of the two-level cache design, not a bug.

## What "warm" actually means here (be honest)

Two different things get cached, at two different lifetimes:

1. **Disk-persisted import graph** (keyed by a hash of go.mod/go.sum + every `.go` file's
   path/size/mtime): survives process restarts. It only ever claims to skip the `go list ./...`
   graph-building step — confirmed above (745 ms → 13 ms on ledger). It does nothing for the
   `packages.Load` step.
2. **In-memory `Result` cache** (keyed by module root + a content hash of every file in the
   resolved scope): lives only as long as the `Loader` (i.e., the flowreview process). It is what
   actually makes repeat queries in one session near-instant (34 ms on ledger). It cannot survive
   a restart because `*packages.Package` (types, ASTs) is not cheaply serializable.

So: "warm" in the FR-13 target is best read as *same-session* warm (one `Loader`, multiple
`Load` calls as the reviewer navigates) — that case is met with large margin on both repos.
A *cross-process* warm open (quit flowreview, reopen the same repo a minute later) still re-runs
`packages.Load` in full; on a repo the size of ledger that costs ~2.6 s, which is the number a
reviewer would actually feel on "reopen app". It is comfortably inside the 10 s cold budget, just
not inside the stricter 1 s warm one if read that way.

There is also a known imprecision in the disk graph cache key: it hashes file **size and mtime**,
not content (as specified), so an edit that preserves both — vanishingly rare in practice — would
be missed until some other file changes.

## Mitigations tried for the one number that misses budget

The slice calls out two "obvious mitigations" to try if ledger blows the budget. Both were tried
against the 80-package ledger scope above and **neither helps**, so neither is implemented:

| Mitigation | Result | Verdict |
|---|---|---|
| Limit reverse importers to depth ≤ 1 (33 packages) or depth ≤ 2 (42 packages) instead of unbounded (80 packages) | 2.12 s / 2.23 s vs. 2.36 s unbounded — a ~5–10% change, within noise | **Not worth it.** The cost is dominated by `packages.Load`'s fixed overhead (invoking `go list`-based type-checking at all, warming GOCACHE's export data for the dependency closure), not by the pattern count in this range. Depth-limiting would also silently drop real reverse importers that FR-15's call graph needs later — a correctness cost for no measured speed gain. |
| `NeedDeps` off (keep `NeedTypes`/`NeedSyntax` for the requested packages only) | 12.97 s — **5.5× slower**, not faster | **Actively harmful here, not implemented.** Removing `NeedDeps` does not mean go/packages avoids computing dependency types (it still must, to type-check imports of them) — it just changes how the driver's underlying `go list` invocation is shaped, and for ledger's dependency graph that shape is markedly slower. |

Given neither mitigation pays for itself, `NeedDeps` stays on and reverse importers stay
unbounded/transitive, matching FR-15's eventual need for the full in-module blast radius. The
two-level cache (disk graph + in-memory `Result`) is the mitigation that matters, and it already
turns every same-session query after the first into single-digit milliseconds.
