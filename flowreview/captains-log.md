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

## 2026-10-14 — FR-26 merged; catalog of six real GGUFs, exact-tier filter

**Decision (agent, accepted for now).** Catalog has six entries with sha256/size taken from the HF tree API: Qwen2.5-Coder-7B, Qwen3-8B, Llama-3.1-8B, Gemma-3-12B (large), Qwen2.5-Coder-3B (medium), Qwen2.5-Coder-1.5B (small). None recommended, eval score 0 until FR-27. `Filter(tier)` matches the tier exactly, so a 32 GB Mac is not offered the 3B model; revisit in FR-27/FR-29 if a faster small model should be offered to big machines too. `DefaultDataRoot` duplicates session.DefaultRoot to avoid a cross-area import.

## 2026-10-14 — FR-27 eval set and weight downloads, decided without the user

**Decision.** The committed eval set uses hunks from this personal `tools` repo only (no secrets, no employer code in a repo that may be public). The harness also accepts an uncommitted directory of exported hunks (e.g. from ledger) for private runs. To produce the ≥3-model report, the agent downloads the 1.5B, 3B and 7B Qwen2.5-Coder GGUFs (~8 GB) into the FlowReview models dir; removable with `flowreview model rm`. Which model is *recommended* stays the user's call (FR-27 is HITL): the slice stays open until they choose.

## 2026-10-14 — FR-13 spike numbers on ledger

**Measured.** ledger (147 pkgs): cold 4.4 s (graph 0.75 s + load 3.7 s), same-process warm 34 ms, new-process warm 2.1–2.7 s (the disk cache only skips `go list`; type-checking can't be cached cheaply). taskhound: cold 0.5 s.
**Decision (agent, accepted).** `-mod=readonly` forced everywhere (ledger's vendor dir has drifted; and flowreview never writes a reviewed repo). No depth limit on reverse importers: it saved ~10% and would drop real callers FR-15 needs; NeedDeps off was 5.5× slower. "<1 s warm" is read as same-session. x/tools pinned v0.48.0 to stay on go 1.25.

## 2026-10-14 — FR-5 merged; single instance via lock file, RepoPath resolved before handoff

**Decision (agent, accepted).** The `Workspace` gains `Sync()` (disk-only refresh after a store write) and `Snapshot()` (session+files read together) so the web layer never pairs a session from before a swap with files from after it. A second invocation sends its source to `/api/open` of the running instance, with RepoPath resolved from its own cwd first (the bug it fixed: the running process otherwise resolved an empty path against its own cwd). The "diff" subcommand must precede flags until FR-12. Smoke: first paint ~50 ms.

## 2026-10-14 — FR-40 merged; step link confidence rule

**Decision (agent, accepted).** A step matching exactly one step definition is `static`; two or more matches are `heuristic` (ambiguity = several edges from one step). Patterns starting with `^` are regexes, anything else a cucumber expression. Outline placeholders use the first Examples row. Handlers named via a selector (`suite.Handler`) are left unresolved rather than guessed. The `flowreview order` acceptance moves to FR-17 (comment there).

## 2026-10-14 — FR-14 merged; two id tiers for Go symbols

**Decision (agent, accepted).** Type-checked symbols use `pkgpath.Name` / `pkgpath.(Recv).Name` (receiver bare, `*Recv` kept in Receiver); syntax-only paths (FunctionAt/Outline, wholly deleted files) use the structural `path#Name`. FR-15 must map between them when joining call-graph edges to diff symbols. Definition returns a Location (covers vars/params too). Signature changes are compared syntactically on both sides; renames are delete+add.

## 2026-10-14 — FR-12 merged; CLI exit codes and PR hook shape

**Decision (agent, accepted).** Exit codes: 0 ok, 1 runtime, 2 usage, 3 not a repo / source not found (incl. GitHub 404), 4 auth/host. Flags may come before or after the positional arg (hand-written scanner). `review.FetchPR` returns the resolved Source (clone path, SHAs). Remote comments are best-effort: a failure never blocks opening the review. `FLOWREVIEW_GITHUB_API` points the binary at another API base (tests). `flowreview context` is not mounted yet; FR-15 exposes ContextText for FR-24.

## 2026-10-14 — FR-16 merged; framework detection by package+type name

**Decision (agent, accepted).** gin/echo/fiber/chi/cobra are matched by package name + type name, not import path (works on vendored/forked copies; fixture uses stub packages). gRPC services are found by the generated `Register*Server` + `*Server` interface pair, with `Unimplemented*Server` embedding as a hint. Entry points may repeat under several kinds; FR-17 dedupes. `.flowreview.yaml` `entrypoints.patterns` take func_regex and/or a package-relative signature.

## 2026-10-14 — FR-6 merged; side-by-side rows, budget met

**Measured.** 10k-line file first paint 32 ms, file switch 41 ms, 0 long tasks while scrolling, session first paint 53 ms (budgets 1000/100/50/1000 ms).
**Decision (agent, accepted).** Rows carry `old_position` and `new_position` (each side has its own GitHub position). Highlighting is per hunk side (the diff holds no text between hunks); per-cell horizontal scroll keeps virtualization simple (synchronized pane scroll is later polish). A stale-response guard fixes fast j/k showing the wrong file.

## 2026-10-14 — FR-15 merged; VTA stays default (0.8 s on ledger)

**Measured.** ledger, FR-13's 3-file load set: SSA 0.12 s + VTA 0.6 s + implements index 0.07 s; under the 5 s bar, so VTA (with CHA fallback on panic) is the default.
**Decision (agent, accepted).** Closures attribute edges to the enclosing named func; generic instantiations to their origin. Implementations search only the in-module load set, not stdlib. Structural ids are mapped to exact ones via path+line. ContextText is ready for `flowreview context` (FR-24 mounts it).

## 2026-10-14 — FR-36 merged; release pipeline ready, first tag is the user's

**Decision (agent, accepted).** `make release` depends on fetch-llama and writes a .sha256; `flowreview upgrade` verifies the published .sha256 before ever executing the download (absent checksum: proceeds, like taskhound). Workflow `.github/workflows/flowreview-release.yml` builds on macos-14 for `flowreview-v*` tags. Not done without the user: pushing the first tag (publishes a public release).

## 2026-10-14 — Scope narrowed to MVP by the user

**Context.** User is running low on credits (2026-10-14).
**Decision (user).** Let the running agents finish (FR-7+9, FR-8, FR-17, FR-19, FR-21, FR-27), but start nothing new except what the usable MVP needs: FR-11 (publish) after FR-9 lands, then build/install for dogfooding. Everything else (FR-18, FR-22–24, FR-28–30, FR-33–35, FR-37–38, FR-41) waits.

## 2026-10-14 — FR-27 harness merged; choosing the recommendation stays with the user

**Measured (43 cases).** 7B: 0.739, 7% hallucinated symbols, 3.1 s/hunk, 5.6 GB. 1.5B: 0.701, 11.5%, 0.9 s, 1.9 GB. 3B: 0.673, 16%, 1.5 s, 3.0 GB (worse than 1.5B on every axis).
**Decision (agent).** Grounding counts the file path as a valid symbol. Near-duplicate board-bump hunks dropped (71 → 43). No rename cases exist in this history (gap). FR-27 stays open (doing) until the user picks Recommended entries; Qwen3.5 entries need an eval run in a session where `go run` is permitted.

## 2026-10-14 — FR-21 merged; test commands grouped by the test's package

**Decision (agent, accepted).** Suggested `go test -run` lines group by the test's own package dir (where `go test` runs), not the changed function's. `/api/tests` computes in the background behind `{"pending":true}`; its cache is a package-level map keyed by *Server to avoid touching the Server struct, so it should be unified with the order/radius endpoints later.

## 2026-10-14 — FR-19 merged; heuristic pass catches the implementer left behind

**Decision (agent, accepted).** Once an interface method's signature changes, `types.Implements` no longer sees a stale implementer, so a name-matched pass over old-interface method names adds `heuristic` implementers; any implementer outside the diff gets a warning. A changed interface computes radius for all its methods (broader, never narrower). Struct-only changes are out of scope. `/api/radius` needs BaseSHA for patch sources (errors there for now).

## 2026-10-14 — FR-8 merged; expansion lives in its own package

**Decision (agent, accepted).** Provider dispatch and old/new alignment live in `internal/core/expand` (it depends on lang providers, which render should not); render gains only ContextRows and `Row.Expanded`. Closures expand to their enclosing named func; no grammar or no symbol falls back to ±15 lines. Expanded rows have position 0 (not publishable as GitHub comments).

## 2026-10-14 — FR-17 merged; flow ordering with a narrated Why

**Decision (agent, accepted).** Backward BFS over callers (depth 6, 500 nodes) to the first entry point; components become flow groups, callers-first by Kahn with lexicographic cycle-breaking. A file joins only the first group claiming it. Unclassified Go files are approximated by BFS depth (there's no import-depth primitive). Hotspot is injected as a func, so `order` doesn't depend on history. Flow analysis starts on the first `/api/order` request rather than at session open (no UI menu yet). Gherkin chains surface when step-def handler ids map to exact Go ids.

## 2026-10-14 — FR-9: FR-7 and FR-9 merged; comments live in the side panel

**Decision (agent, accepted).** Comment threads render in the side panel, with a gutter dot and a focused-line outline in the diff, because the virtualized grid assumes a fixed row height and inline variable-height rows would endanger the scroll budget. `R` posts one state per hunk (no batch endpoint). API responses send `Cache-Control: no-store` (a reload could read stale review state). `POST /api/reload` re-diffs and runs Refresh. Merge note: FR-8's expand click handler and FR-9's line click handler were unified into one delegated `onDiffClick`.

## 2026-10-14 — FR-11 merged; MVP (M1) complete

**Decision (agent, accepted).** Preflight keeps a draft only if its hunk id still exists in the freshly fetched PR diff (stronger than comparing head SHAs, and tolerant of unrelated pushes). New line+side API (old→LEFT, new→RIGHT); general drafts go into the review body. A 422 falls back to a body-only review plus per-comment posts, so one bad line can't sink the batch. Retrying only-failed drafts posts them individually. Batch reviews don't return per-comment links (GitHub API limit).
**Milestone.** With FR-1–FR-12 and FR-39 merged, M1 is functionally complete; the remaining gate is the user's dogfood review of a real PR.

## 2026-10-14 — Backlog stays paused after the MVP

**Context.** The session /goal asked for the whole backlog; the user later limited the run to the MVP for credit reasons.
**Decision (user, 2026-10-14).** Stay paused: 14 slices remain open on the board. The user clears the goal hook with /goal clear.
