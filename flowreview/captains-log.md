# flowreview — captain's log

Why things are the way they are. Append-only; written with `th log add`.

## 2026-10-14 — Autonomous build run started

**Context.** User asked (2026-10-14) to implement the FR backlog as far as possible without them, using Sonnet agents for coding, and to log every decision made without them here.
**Decision.** Order of work: M1 first (daily-usable), then M2, M3, M4, M5. Opus writes FR-1 (frozen types) and reviews/merges; Sonnet agents implement slices in parallel git worktrees, one slice per agent. HITL slices (FR-27 model choice, FR-38 signing) are built up to the human decision and left open.

## 2026-10-14 — FR-1 frozen types: placement and identity rules

**Decision.** Core types live in `internal/model` (one package everyone imports, no cycles); interfaces live in `internal/{lang,host,llm,agent}/provider.go`. Module path `github.com/iszlai/tools/flowreview`, go 1.25.
**HunkID.** sha1(path + "\n" + body) where body is marker+text per line, no @@ header. CRLF is normalized (a trailing \r dropped) — the only normalization; whitespace and a missing final newline are content. Rename changes the id (plan says path is part of it); FR-22 must therefore match renamed hunks by body when carrying review state, if wanted.
**Dependencies.** Not vendored during the build-out (many cgo grammar deps; the goproxy works); revisit at FR-36 release.
**Providers.** LanguageProvider is constructed per repo root; FunctionAt/Outline take source bytes so old/new revisions both work. AgentProvider streams via an emit callback rather than a channel.

## 2026-10-14 — FR-31 merged; evidence verification is substring within ±2 lines

**Decision (agent, accepted).** `Verify` accepts a finding when at least one evidence excerpt is a trimmed substring of the cited line or a line within ±2 (line corrected to the match). Additive `Limits.MaxFindings` field. Schema drift is guarded by a test with `-update`, not a separate generator command.
**Also.** Worktrees created by the agent harness were sometimes cut from a stale base (46263fd, before FR-1); agents are told to rebase onto main, and Opus imports only the slice's own package when merging such a branch (done for FR-5a's window package: its bootstrap go.mod/Makefile were discarded).

## 2026-10-14 — FR-4 merged; needs-re-review is path-level until FR-22

**Decision (agent, accepted).** `Refresh` marks a vanished reviewed hunk `needs-re-review` when its file still has any hunk, else `stale`. A path-level proxy for "overlapping location": ReviewState has no line range. FR-22 must refine this (match by location/body similarity) — noted on FR-22.
**Session ids** derive from the source identity (`pr-owner-repo-N`, `range-<hash>`, `worktree`) and `FindBySource` ignores SHAs, so reopening an updated PR resumes the same session.

## 2026-10-14 — FR-32 merged; x/mod pinned to stay on go 1.25

**Decision (agent, accepted).** `golang.org/x/mod` pinned at v0.28.0 because latest requires go 1.26 and would bump the module's go directive. Scratch files under the toolchain root are dot-prefixed so `List` can ignore them. `flowreview toolchain list|rm` CLI wiring deferred to the CLI slice.

## 2026-10-14 — FR-3 merged; GitHub position rule verified

**Verified, not assumed.** Position 1 is the first line after a file's first @@; every later @@ line and every "\ No newline at end of file" marker also consumes a position. Header lines are kept verbatim so Emit never re-quotes a path; mode-only changes are FileModified (no separate status).

## 2026-10-14 — FR-39 merged; SQL grammar dropped, Gherkin hand-parsed

**Decision (agent, accepted).** No Go-bindable tree-sitter SQL grammar ships its generated parser.c (DerekStride lacks it; m-novikov has no Go binding), so SQL is out of the structural tier for now; re-adding is one grammar entry + a query file. Gherkin has no Go binding either and uses a small hand-written line parser (Feature/Background/Rule/Scenario + steps). Added SymFeature/SymBackground/SymRule to model (additive). Closures get position-based ids and are omitted from Outline.

## 2026-10-14 — FR-25 merged; llama.cpp b11609 pinned

**Decision (agent, accepted).** Pinned llama.cpp release b11609 (macOS arm64 tar.gz; sha256 committed in third_party/llama/SHA256SUMS). `make fetch-llama` copies the 11 files llama-server needs (otool closure, symlinks dereferenced) into the gitignored dist/; `make build` adds `-tags llama_embed` when dist/ exists, otherwise builds use the stub provider. CompleteJSON uses `response_format: json_schema`, retrying once with the bare `json_schema` field if the server rejects it. Verified end to end with SmolLM2-135M (20 tokens in 75 ms).

## 2026-10-14 — FR-20 merged; history cache keyed by caller-supplied HEAD

**Decision (agent, accepted).** `Mine(ctx, headSHA)` takes HEAD from the caller so a cache hit runs no git at all. The cache is a plain JSON write (regenerable), invalidated when mining params change. Ledger: ~0.75 s for 654 commits in 365 days, under the 2 s budget.
**Also.** Test fixtures pass `-c commit.gpgsign=false` per git call: the user's global gpgsign made the FR-2 git tests take 36 s.

## 2026-10-14 — FR-10 merged; PR clone fetches pull/N/head plus origin

**Decision (agent, accepted).** Token order: GITHUB_TOKEN, GH_TOKEN, then `gh auth token`. A PR is reviewed in the local clone when origin matches (only `git fetch`, never a checkout), else in a blobless `gh repo clone` cache under the data root. Outdated review comments (`line: null`) are kept and flagged. Verified read-only against cli/cli#14679.
