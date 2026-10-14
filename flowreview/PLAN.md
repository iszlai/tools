# FlowReview — Execution Plan

**Status:** Plan v1.1, 2026-10-14 (decisions from the grill applied; see §8). Supersedes the architecture section of the draft PRD; the PRD's goals, non-goals and safety rules stand.
**Shape:** one Go binary (`flowreview`) that opens its own native window (WKWebView) over an embedded web UI, embedded `llama-server` sidecar, on-demand Go toolchain, tree-sitter structure for every language plus exact `go/packages` + `go/ssa` semantics for Go, `gh` token for GitHub.
**Board:** `flowreview/.taskhound.yaml` (prefix `FR`). `th next` lists what an agent can start.

---

## 1. Review of the draft PRD

The PRD is strong on product rules (human control, evidence-linked findings, local-first). Six things change so that it can be built fast by small agents and used daily within weeks.

| # | PRD said | Change | Why |
|---|---|---|---|
| 1 | Tauri + Monaco/CodeMirror, Rust shell, Go analysis service | **Go binary serving an embedded vanilla-JS UI, shown in its own WKWebView window from M1** (never a browser tab); the HTTP/JSON API is a hard boundary so a SwiftUI front end can replace the web UI later without touching the core | The semantic engine must be Go (go/types, go/ssa, callgraph exist only there), so any Rust or C++ shell would still ship a Go process. Three toolchains is the slowest path for small agents. `taskhound`, `diagme` and `gh-view` prove the Go + web UI + Playwright pattern. Snappiness comes from virtualized, precomputed rows and never blocking on analysis; a strict budget (16 ms frames, <100 ms file switch, <1 s open) is in the acceptance criteria. If daily use still feels web-ish, the native front end is a scheduled option (§8). |
| 2 | Monaco/CodeMirror diff editor | **Server-rendered diff rows** (Go computes the diff and syntax highlight with `chroma`; UI is a virtualized table) | Editors fight the diff-position mapping that comment publishing depends on. Server-rendered rows carry `(file, side, line, hunk, position)` as data attributes, so anchors are trivially exact. No bundler, no node at build time. |
| 3 | `gopls`/LSP for semantics; other languages "future" | **Two-tier `LanguageProvider`**: a tree-sitter structural provider for every language (function boundaries, outline, heuristic callers/callees as in gh-view) and exact in-process `go/packages` + `go/types` + `go/ssa` + `callgraph` (VTA, CHA fallback) for Go. Java, JS/TS and Gherkin get the structural tier in M1/M2; Gherkin additionally gets step-to-step-definition linking. LSP providers (JDT, tsserver) are the upgrade path to exact semantics. The Go toolchain is downloaded on demand, not required on PATH. | Multi-language from the start without waiting on per-language exact analysis. gopls is a process to manage and gives no call graph. |
| 4 | OAuth + Keychain | **Reuse the `gh` CLI token** (`gh auth token`) first; device-flow OAuth + Keychain as a later task | Zero auth work for the MVP; `gh` already stores the token in the keyring. |
| 5 | llama.cpp "or MLX", runtime selection open | **Embedded `llama-server`**: the macOS arm64 Metal build is `go:embed`-ded in the binary, extracted to Application Support on first run, spawned as a sidecar, spoken to over its OpenAI-compatible HTTP API. Only model weights are downloaded, once, with consent and a checksum. | Hard requirement: a user installs one thing. llama.cpp is MIT, has Metal, JSON-schema-constrained output, prompt caching slots and an embeddings endpoint. A Go `ModelProvider` interface keeps cgo/MLX swaps possible. |
| 6 | Four sequential phases | **Tracer-bullet slices**; a usable daily tool (local diff + PR + comments + publish) exists after Milestone 1, before any semantics or LLM work | Day-to-day value early; semantics and LLM are additive. |

Gaps in the PRD that the plan fills: no "what changed since I last reviewed" mode (essential for PR re-review); no blast-radius for unchanged callers; no concrete entry-point heuristics; no eval set for summaries; no distribution/upgrade story; no story for using the core from agents (CLI text output).

## 2. Opportunities and feature combinations

Ranked by value per effort. Items marked **M1–M4** are in the plan; **Later** are recorded, not scheduled.

| Feature | Combination of | Value | Where |
|---|---|---|---|
| **Delta re-review** — after a PR is pushed again, review only hunks changed since the head you last marked reviewed; carry reviewed state forward for untouched hunks | diff model + review store + stable hunk identity | The single biggest daily time saver | M2 |
| **Blast radius** — for each changed function: unchanged callers in the repo, and for signature/interface changes every call site and implementer that must change | call graph + diff | Finds the classic "changed the function, missed a caller" bug without any LLM | M2 |
| **Co-change companions** — files that historically change together with the files in this diff (inkode `coupling`) but are absent from it: "usually changes with `handler_test.go`, not in this PR" | git log mining + diff | Cheap, high hit rate for forgotten tests, migrations, docs | M2 |
| **Hotspot badge** — change frequency × size (inkode `hotspot`) shown per file, used as tie-breaker in ordering | git log + ordering | Tells the reviewer where to slow down | M2 |
| **Test reach** — which changed functions are reachable from a `Test*` function; suggest `go test -run` for exactly those; optionally run it as a read-only check | call graph + diff + check runner | Answers "is this change tested" structurally | M2/M4 |
| **Structured summaries** — the local model returns JSON (`kind: behavior/refactor/rename/test/config`, `what`, `inferred_why`, `impact`, `risk_flags[]`, `confidence`) via llama.cpp JSON-schema; UI renders badges, can sort risky-first and collapse "refactor-only" hunks | embedded LLM + ordering + review queue | Summaries become review signals, not prose; grounded fields are easy to evaluate | M3 |
| **Prompt-cache-aware scheduling** — summaries for consecutive hunks of one file share a cached prefix slot in `llama-server` | embedded LLM + ordering | Big latency win on an 8B model | M3 |
| **Agent as provider** — `claude -p` (read-only tools) if installed, else the embedded model with a small tool loop; both emit the same `Finding` JSON | agentic review + existing CLI agents | Ship agentic review without building an agent framework | M4 |
| **Core as a CLI/agent tool** — `flowreview order`, `flowreview context pkg.Func`, `flowreview summarize` print text/JSON; usable from Claude Code sessions and scripts | CLI + semantics | The same engine improves agent-run reviews in your toolkit | M2 |
| **Commit walkthrough** — order by the author's commits as an alternative to flow order | git + ordering | Cheap second narrative | M2 |
| **Review summary draft** — generate the top-level review body from your own comments and reviewed state | LLM + comments | Removes the last chore before submit | M3 |
| Embeddings: "similar code elsewhere" for a changed hunk (llama-server `--embedding`) | embedded LLM | Finds duplicated logic that should have changed too | Later |
| Per-repo reviewer notes injected into prompts ("in this repo retries must be idempotent") | store + LLM | Personal memory | Later |
| Other hosts (GitLab), other languages (TS via LSP/tree-sitter) | provider interfaces | Growth | Later |

## 3. Architecture

```
flowreview (one Go binary, arm64 macOS)
├─ cmd/flowreview            CLI: open | diff | pr | order | context | summarize | model | ui
├─ internal/core
│   ├─ git        diff for worktree/refs/patch, file contents at rev, log mining
│   ├─ diff       unified-diff parse → ChangedFile/DiffHunk, stable hunk ids, position map
│   ├─ session    ReviewSession, ReviewState, CommentDraft; JSON store, atomic write, flock
│   ├─ order      flow ordering, explanations, alternative orders
│   └─ render     diff rows → HTML fragments (chroma), function expansion
├─ internal/lang
│   ├─ provider.go  LanguageProvider interface + capability flags (structural | exact)
│   ├─ treesitter/  structural tier for all languages: functions, outline, heuristic calls
│   ├─ gherkin/     scenarios, steps, step-definition linking (godog, cucumber-jvm, cucumber-js)
│   ├─ golang/      go/packages loader+cache, symbols, callgraph, implementations, entrypoints
│   └─ toolchain/   on-demand Go distribution download, GOTOOLCHAIN=auto
├─ internal/host
│   ├─ provider.go  Host interface (PR fetch, comments, publish)
│   └─ github/      REST via gh token
├─ internal/llm
│   ├─ provider.go  ModelProvider (Complete, CompleteJSON, Embed, Health)
│   ├─ sidecar/     embedded llama-server: extract, spawn, health, stop
│   ├─ models/      catalog, download, sha256, tiers, remove
│   └─ summary/     prompts, schema, cache, prioritized queue
├─ internal/agent    Finding model, providers (claude-cli, embedded), read-only checks
├─ internal/web      HTTP API + go:embed ui/ (index.html, app.js, styles.css, md.js from mdlite)
├─ internal/window   native WKWebView window (cgo + Objective-C shim): title, menus, shortcuts, focus
└─ third_party/llama  embedded llama-server build + licence
```

Data lives in `~/Library/Application Support/FlowReview/`: `sessions/<repo-key>/<session>.json`, `cache/summaries/<hash>.json`, `cache/go/<module>/…`, `bin/llama/<version>/`, `bin/go/<version>/`, `models/<name>.gguf`.

Key identities (decide once, in FR-1):

- `hunk_id = sha1(path + normalized hunk body without line numbers)`. Survives rebases that only move lines; any content change invalidates review state (strict). The store keeps the body of the last reviewed version of each hunk so the UI can show "diff against what you reviewed" when the change is small.
- `anchor = {path, side(old|new), line, hunk_id, head_sha}`; publishing maps to GitHub `line`+`side` (new API), with the diff `position` kept for verification.
- `summary_key = sha256(model_id + prompt_version + hunk body + enclosing-function text + context slice)`.

### Embedded LLM specifics

- Sidecar: `llama-server` from a pinned llama.cpp release (macOS arm64, Metal) plus its dylibs, embedded via `go:embed`, extracted to `bin/llama/<version>/` with `0755`, started on `127.0.0.1:0` with `--parallel 2 --ctx-size 8192 --cache-prompt --jinja`, health-checked, killed on exit. Binary size cost is tens of MB; acceptable.
- Model tiers by physical RAM: ≥24 GB → 7–8B Q4_K_M (~5 GB); 16 GB → 3–4B Q4 (~2 GB); 8 GB → 1.5B or summaries off. Catalog is a Go struct with URL, sha256, licence, size and the FR-27 eval score; there is no silent default: at first run the user picks from the catalog (filtered to their RAM tier, eval recommendation highlighted) or chooses "summaries off"; downloaded after a disclosure dialog; resumable; deletable from `flowreview model rm`.
- Candidates for FR-27 eval: Qwen2.5-Coder-7B-Instruct, Qwen3-8B, Llama-3.1-8B-Instruct, Gemma-3-12B (fits on 32 GB) and a 3B tier. Licence shown per entry; the eval report, not taste, decides what is highlighted.
- Structured output via `response_format: {type: json_schema}`; deterministic fallback (`"Modified <func>: +a/−b lines"`) when the sidecar is down.
- Nothing leaves the machine unless the user picks a remote provider; the UI shows a badge whenever a remote provider is active.

## 4. Milestones

| Milestone | You can… | Slices |
|---|---|---|
| **M1 Daily-usable reviewer** | open a local diff or PR in a native window, read side-by-side in any language, expand functions, mark reviewed, draft and publish comments, resume later | FR-1 … FR-12, FR-39 |
| **M2 Flow and risk** | files ordered by call flow with explanations, callers/callees/implementations (exact for Go, structural for Java/JS/TS), Gherkin steps linked to step definitions, blast radius, companions, hotspots, test reach, delta re-review with "diff since reviewed", CLI text output | FR-13 … FR-24, FR-32, FR-40, FR-41 |
| **M3 Embedded summaries** | structured local summaries above hunks, risky-first sort, user-picked model from an eval-ranked catalog, no install | FR-25 … FR-30 |
| **M4 Agentic review** | opt-in findings from the embedded model with verified evidence, approve-to-comment, read-only checks | FR-31, FR-33 … FR-35 |
| **M5 Distribution** | `curl` install, `flowreview upgrade`, `.app` bundle polish, notarization | FR-36 … FR-38 |

## 5. Slices

Conventions: every slice is **AFK** unless marked **HITL**. Size: S ≤ 300 lines, M ≤ 800, L needs a spike first. Every slice ends with `make check` green (`go vet`, `go test ./...`, and `make smoke` where the UI changed). Interfaces are frozen in FR-1; later slices extend, never rewrite.

### M1 — Daily-usable reviewer

**FR-1 Skeleton and frozen types** (S) — blocked by: none
Go module, `Makefile` (`build test check smoke release`), directory layout above, and the core types with JSON tags: `ReviewSession`, `ChangedFile`, `DiffHunk`, `Anchor`, `ReviewState`, `CommentDraft`, `CodeSymbol`, `NavigationEdge{Kind, Confidence, Source}`, `AgentFinding`, `SnippetSummary`, `ModelConfig`. Interfaces: `LanguageProvider`, `Host`, `ModelProvider`, `AgentProvider`, each with a `Capabilities()` method. Identity functions `HunkID`, `SummaryKey` with table tests.
Accept: `go build ./...`; identity tests cover rename-only, whitespace-only, line-shift-only cases.

**FR-2 Git adapter** (M) — blocked by: FR-1
Worktree diff, `A...B` and `A..B`, single commit, patch file; file content at a revision; rename detection (`-M`); binary files flagged. Shell out to `git`, no libgit.
Accept: fixture repo in `testdata/` built by a script; tests for each mode, renames, deletions, untracked files.

**FR-3 Unified diff parser and position map** (M) — blocked by: FR-1
Parse unified diff into `ChangedFile`/`DiffHunk`; compute GitHub `position` per line and old/new line numbers; `HunkID`. Round-trip test: parse → re-emit equal text.
Accept: golden tests on real diffs including `\ No newline at end of file`, mode changes, renames with similarity, empty files.

**FR-4 Session store** (S) — blocked by: FR-1
JSON session files, atomic temp+rename write, sidecar flock (copy the taskhound approach), repo key = `sha1(remote URL or abs path)`. CRUD for `ReviewState` and `CommentDraft`. Reviewed state stores the hunk body at review time. `Refresh(newDiff)` marks state whose `hunk_id` disappeared as `stale`, never deletes.
Accept: concurrency test with 16 writers; stale detection test.

**FR-5 Server, native window and UI shell** (M) — blocked by: FR-3, FR-4
HTTP on `127.0.0.1:<port>`, `go:embed ui/`, endpoints `GET /api/session`, `GET /api/files`, `GET /api/file/{id}/rows`, `POST /api/state`, `POST /api/comments`. `flowreview` opens its own WKWebView window (`internal/window`, cgo + a small Objective-C shim: title, standard Edit menu, `⌘W`, `⌘Q`, `⌘,`), never a browser tab; `--browser` exists only for Playwright. A lock file lets a second invocation focus the running window (`POST /api/open`). Three-pane layout: file queue, diff, side panel. System font, system colors, dark/light from `prefers-color-scheme`.
Accept: `curl` tests for every endpoint; Playwright smoke harness (`tests/smoke.js`, chromium-headless-shell, per diagme lessons) opens a session via `--browser` and sees the file list; manual check that the window opens, focuses and closes natively; session open to first paint < 1 s on a mid diff.

**FR-6 Side-by-side diff renderer** (M) — blocked by: FR-5
Server renders rows: `{old_no, new_no, kind, old_html, new_html, hunk_id, position}` with `chroma` highlighting per language, intra-line change highlighting; UI virtualizes rows (only visible rows in DOM) so a 10k-line diff scrolls smoothly; hunk navigation `n/p`.
Accept: smoke measures and fails on budget misses: 10k-line fixture first paint < 1 s, file switch < 100 ms, no long tasks > 50 ms while scrolling (PerformanceObserver); row data attributes carry exact anchors.

**FR-7 Review queue, reviewed state, progress** (S) — blocked by: FR-6
File states unreviewed/in-progress/reviewed; hunk toggle `r`; file toggle `R`; progress bar; completion feedback (subtle); session summary panel; persists via FR-4; keyboard: `j/k` files, `n/p` hunks, `c` comment, `?` help.
Accept: smoke: mark hunk, reload page and process, state persists; progress counts match.

**FR-8 Enclosing-function expansion** (M) — blocked by: FR-6
`go/ast`-only (no type checking) `FunctionAt(path, line)` returning the decl range at old and new revision; UI "expand function" renders unchanged rows in context, keeps anchors; collapse returns to the original scroll position. Non-Go files use the tree-sitter structural provider (FR-39) and fall back to ±N lines when no grammar exists.
Accept: unit tests on methods, closures, generics; smoke: expand/collapse preserves scroll and anchors.

**FR-9 Comment drafts** (S) — blocked by: FR-7
Inline draft on any rendered row (changed or expanded-context, with publishability flag), general comments, edit/delete, markdown preview via `mdlite`. Drafts persist and survive refresh; stale drafts shown with a warning and a "re-anchor" picker.
Accept: smoke: draft → reload → still there; stale draft flagged after a fixture refresh.

**FR-10 GitHub host: read** (M) — blocked by: FR-2, FR-3
Token from `gh auth token` (error with install hint if missing). Fetch PR metadata, head/base SHAs, the PR diff (`application/vnd.github.v3.diff`), existing review comments and reviews; fetch the repo if not local (`gh repo clone` into cache) or use the local clone when `origin` matches. Existing comments render in the side panel at their anchors.
Accept: recorded-HTTP tests (fixtures, no network in CI); manual run against a real PR documented in README.

**FR-11 GitHub host: publish** (M) — blocked by: FR-9, FR-10
Submit a batch as a review (`COMMENT`, `APPROVE`, `REQUEST_CHANGES`), single comment publish, per-comment result, retry on failure, drafts kept until confirmed; preflight checks that each anchor still maps on the current head (re-fetch head SHA).
Accept: recorded tests for success, partial failure, 422 on bad position; smoke with a stub server.

**FR-12 CLI surface M1** (S) — blocked by: FR-5, FR-10
`flowreview` (worktree), `flowreview diff <ref|range>`, `flowreview pr <url|number>`, `flowreview --patch f`, `flowreview sessions`, `--no-open`, `--port`. Stable exit codes; `--json` on list commands.
Accept: e2e test runs the binary against the fixture repo; README documents every command.

### M2 — Flow and risk

**FR-13 Go package loader with cache** (L, spike first) — blocked by: FR-2, FR-32
`go/packages` load of packages containing changed files plus their reverse importers inside the module (via `go list -deps` graph), `NeedTypes|NeedSyntax|NeedTypesInfo`; results cached per module by file-hash set; background warm on session open; timings logged. Benchmark on `ledger` (the large case) and a small repo; record numbers in the slice.
Accept: load of changed+reverse packages on a mid repo < 10 s cold, < 1 s warm; errors in unrelated packages do not abort.

**FR-14 Symbols and definitions** (M) — blocked by: FR-13
`CodeSymbol` for funcs, methods, types, interfaces; definition lookup from any identifier position (`types.Info.Uses`); changed-symbol detection by intersecting decl ranges with hunks; signature-change detection (old vs new type string).
Accept: tests on receiver methods, embedded interfaces, generics, dot-imports.

**FR-15 Call graph: callers, callees, implementations** (L) — blocked by: FR-14
`go/ssa` + `callgraph/vta` (CHA fallback when SSA build fails); edges carry `Confidence` (`static`, `interface-dispatch`, `heuristic`) and `Source`; implementations via `types.Implements`; multiple candidates returned, never one invented. `flowreview context <pkg.Func>` prints it.
Accept: fixture with direct calls, interface dispatch, method values, closures, goroutines; golden output.

**FR-16 Entry points** (S) — blocked by: FR-14
Heuristics with explanations: `main`, `init`, `http.HandlerFunc` signatures, `gin`/`echo`/`chi` handlers, gRPC service methods (generated `*Server` interfaces), `cobra` `RunE`, `Test*`, exported funcs with no in-module callers. Configurable extra patterns in `.flowreview.yaml`.
Accept: fixture covers each kind; each entry point carries its `reason`.

**FR-17 Flow ordering** (M) — blocked by: FR-15, FR-16
Build the subgraph of changed symbols plus the paths that connect them to entry points; topological order callers-first; group by connected component ("flow groups") with a generated title (entry point name); unclassified files after, ordered by import depth then path; tie-break by hotspot score when available; each file carries `why: "called from X (changed) ← handler Y"`. Alternative orders: alphabetical, directory, original diff, commits.
Accept: deterministic golden order on the fixture; `flowreview order` prints groups with reasons.

**FR-18 Call-chain explorer UI** (M) — blocked by: FR-15, FR-8
Side panel: callers / callees / implementations of the symbol under the cursor; click opens the target (changed → jump in diff; unchanged → read-only view with breadcrumb); back/forward history; destination badge changed/unchanged/outside-diff; confidence shown.
Accept: smoke: navigate three hops and return to the exact original row.

**FR-19 Blast radius and signature changes** (S) — blocked by: FR-15, FR-14
Per changed function: count and list of unchanged in-module callers; for signature or interface-method changes: every call site and implementer, flagged whether it is in the diff. Shown in the file header and side panel.
Accept: fixture where an interface method changes and one implementer is not in the diff → flagged.

**FR-20 Co-change companions and hotspots** (M) — blocked by: FR-2
Git-log mining (last 365 days, cap 5000 commits): co-change confidence per file pair (min support 5, confidence 0.5) and change frequency; cached per repo+HEAD. "Usually changes with …, not in this diff" list; hotspot badge.
Accept: fixture repo with scripted history; thresholds configurable; runs < 2 s on `ledger`-sized history or degrades with a note.

**FR-21 Test reach** (S) — blocked by: FR-15
Which changed functions are reachable from `Test*`/`Benchmark*`/`Example*` functions; list unreached changed functions; suggested `go test ./pkg -run 'TestA|TestB'` line, copyable.
Accept: fixture with reached and unreached functions.

**FR-22 Delta re-review** (M) — blocked by: FR-4, FR-10
On refresh: keep reviewed state for hunks whose `hunk_id` is unchanged, mark changed ones `needs-re-review`, show a "since you last reviewed (sha…)" filter and a range-diff style list of what moved; for each `needs-re-review` hunk a toggle shows the diff between the body you reviewed (stored in FR-4) and the current body, so a one-character fix is one glance; works for local branches re-committed and for PR pushes.
Accept: fixture with amended commit: unchanged hunks stay reviewed, changed hunk flagged; smoke covers the filter.

**FR-23 Commit walkthrough order** (S) — blocked by: FR-17
Order by commits of the range; a hunk appears under the first commit that introduced it; switchable in the order menu.
Accept: golden on fixture range with three commits.

**FR-24 CLI text/JSON surface for agents** (S) — blocked by: FR-17, FR-19, FR-21
`flowreview order|context|radius|tests --json`; one-screen plain text by default; documented so a Claude Code skill can call it.
Accept: e2e tests; a `skill/flowreview/SKILL.md` describes usage from an agent.

**FR-39 Tree-sitter structural provider** (M) — blocked by: FR-1
`LanguageProvider` tier `structural` using the official Go tree-sitter bindings (cgo) with grammars compiled in for Go, Java, JavaScript, TypeScript, Gherkin, YAML, SQL, Python, Rust; gives function/method/class ranges (`FunctionAt`), outline, identifier under cursor, and heuristic callers/callees by name search within the repo (gh-view's approach) marked `Confidence: heuristic`. Highlighting stays with `chroma`. Adding a language = adding a grammar and a small query file.
Accept: table tests per grammar on fixtures; FR-8 expansion works for a Java method and a JS function; `make build` builds with `CGO_ENABLED=1`.

**FR-40 Gherkin step linking** (M) — blocked by: FR-39
For `.feature` files: scenarios and steps as symbols; link each step to its step definition by matching step text against registered patterns (godog `ctx.Step(regexp, fn)`, cucumber-jvm `@Given("…")`, cucumber-js `Given(/…/)`), then into the callee graph. Changed step definitions list the scenarios that exercise them; changed scenarios list unmatched steps. Flow ordering treats a scenario as an entry point.
Accept: fixture with godog and cucumber-js step defs; unmatched step reported; `flowreview order` shows `Scenario → step def → service`.

**FR-41 Java and JS/TS structural flow** (S) — blocked by: FR-39, FR-17
Entry-point heuristics for the structural tier: Spring `@RestController`/`@GetMapping`, JAX-RS, `main`, JUnit `@Test`; Express/Fastify/Next route handlers, `export default`, Jest `test(`. Flow ordering runs over heuristic edges with the lower confidence shown in the explanation.
Accept: Java and TS fixtures produce a sensible golden order with reasons.

### M3 — Embedded summaries

**FR-25 Sidecar embedding and lifecycle** (M) — blocked by: FR-1
Pin a llama.cpp release; `make fetch-llama` downloads the macOS arm64 Metal archive into `third_party/llama/` with checksum (committed checksum, not the binary); `go:embed` the server and dylibs; on start extract to `bin/llama/<ver>/` if missing, `chmod`, spawn on a free port with the flags above, poll `/health`, stop on exit and on `flowreview model stop`. `ModelProvider` implementation over `/v1/chat/completions` with `json_schema` and cancellation via context.
Accept: integration test (skipped without the archive) starts, completes a 20-token prompt, stops; binary builds without the archive using a stub provider.

**FR-26 Model manager** (M) — blocked by: FR-25
Catalog struct; RAM tier detection (`sysctl hw.memsize`); `flowreview model list|install <name>|rm|status`; resumable download to `models/`, sha256 verify, disclosure of size, licence and RAM before download; UI first-run picker listing the catalog filtered to the tier with eval score, size and licence per entry, no preselected model; "summaries off" is always a choice and is the default below 16 GB with the reason shown.
Accept: download test against a local HTTP fixture; interrupted download resumes; wrong checksum is deleted.

**FR-27 Eval harness and model choice** (HITL, M) — blocked by: FR-25, FR-26
`eval/` with 40–60 real hunks from your repos (exported, no secrets) and hand-written expected facts; scorer checks factual claims (named symbols exist, counts match), latency, tokens/s, memory; runs each catalog model; report as markdown. You decide which entries stay in the catalog and which is highlighted as recommended per tier.
Accept: report produced for ≥3 models on this Mac; scores recorded in the catalog with the report linked.

**FR-28 Summary pipeline** (M) — blocked by: FR-25, FR-3, FR-8
Prompt v1 (hunk + enclosing function + imports + up to 2 callers' signatures); JSON schema `{kind, what, inferred_why, impact, risk_flags[], confidence}`; cache keyed by `SummaryKey`; worker with priority queue (visible hunks first, then flow order), 2 in flight, cancellation when the viewport moves away; deterministic fallback; `prompt_version` constant bumps invalidate the cache.
Accept: unit tests for cache keys and queue priority; integration test with the sidecar.

**FR-29 Summary UI** (S) — blocked by: FR-28, FR-7
Card above each hunk: what / inferred why / impact, kind badge, risk flags, confidence; loading shimmer; regenerate; "expand" asks for more detail; "risky first" sort and "collapse refactor-only" toggle; settings: on/off, model, tier.
Accept: smoke with a stub provider: cards render, sort changes order, off hides all.

**FR-30 Review body draft** (S) — blocked by: FR-28, FR-11
Generate a review summary from the user's own comments and reviewed state; editable before submit; never auto-submitted.
Accept: unit test on prompt assembly; smoke with stub.

### M4 — Agentic review

(FR-32 below belongs to M2; it kept its board id when the Claude CLI provider was dropped. Agentic review ships with the embedded model only: no cloud provider and no external CLI in M4.)

**FR-31 Finding model and provider interface** (S) — blocked by: FR-1
`AgentFinding{severity, title, explanation, evidence[]{path,line,excerpt}, confidence, verification, suggested_comment, anchor}`; `AgentProvider.Review(ctx, session, scope)` streams findings; resource limits (time, tokens); every run logged to `sessions/.../runs/`.
Accept: JSON schema file generated from the struct; tests.

**FR-32 On-demand Go toolchain** (M) — blocked by: FR-1
No `go` required on PATH. On first open of a Go repo: pick the version from `go.mod` (`toolchain`/`go` directive), download the official darwin/arm64 archive from go.dev/dl into `bin/go/<version>/` after the same disclosure dialog as model weights (size, source, checksum), verify sha256 against the published list, set `GOROOT`, `GOTOOLCHAIN=auto`, `GOFLAGS=-mod=readonly`, `GOCACHE` under Application Support; reuse across repos; `flowreview toolchain list|rm`.
Accept: integration test with a local HTTP fixture serving a tarball; corrupted archive is deleted; a repo with a newer `toolchain` line triggers the auto switch; no network when the version is cached.

**FR-33 Embedded-model provider** (M) — blocked by: FR-31, FR-28, FR-15
The only agentic provider in M4. Small bounded loop: for each changed function, give hunk + callers + callees (from FR-15, no tool calls needed) and ask for findings with the schema; cap at N functions; evidence must quote lines that exist (verified before display).
Accept: integration test; hallucinated-evidence fixture is dropped.

**FR-34 Findings UI and approve-to-comment** (S) — blocked by: FR-31, FR-9
Findings panel with severity filter, evidence links that jump to code, "turn into draft comment" (editable), explicit AI label on anything AI-originated, nothing published without the normal submit flow.
Accept: smoke: finding → draft → appears in review batch marked AI-assisted.

**FR-35 Read-only checks runner** (M) — blocked by: FR-21
Configurable commands per repo (`.flowreview.yaml`: `go vet ./...`, `go test` for reached packages, `staticcheck` if installed), allowlist only, timeout, output captured as evidence, never writes (runs with `GOFLAGS=-mod=readonly` and a clean env); results attach to files as findings.
Accept: tests with a fixture failing `go vet`; a disallowed command is refused.

### M5 — Distribution

**FR-36 Release and upgrade** (S) — blocked by: FR-12, FR-25
`make release` builds darwin/arm64 (amd64 later) with the embedded sidecar, GitHub release on `flowreview-v*` tag, `install.sh`, `flowreview upgrade` (copy taskhound's), `flowreview version`.
Accept: tag on a branch produces a release artifact that runs on a clean user account.

**FR-37 `.app` bundle polish** (S) — blocked by: FR-36
The window already exists (FR-5); this packages the binary as `FlowReview.app` with icon, `Info.plist`, Dock presence, Open Recent, and registers the `flowreview` CLI symlink; CLI remains primary.
Accept: double-click opens the last session; `flowreview pr …` from a terminal focuses the running app.

**FR-38 Signing and notarization** (HITL, S) — blocked by: FR-37
Developer ID signing of binary, dylibs and app; notarize; document the Gatekeeper story for the curl install path.
Accept: fresh Mac opens the app without warnings.

## 6. Verification strategy

- `make check`: `go vet`, `go test ./...` (fixtures under `testdata/`, recorded HTTP for GitHub, stub model provider). Fast, no network, no browser.
- `make smoke`: Playwright `tests/smoke.js` against the real binary, Playwright's own chromium (corporate Chrome refuses automation), run outside the sandbox. Every UI slice adds assertions.
- `make eval`: FR-27 harness; re-run when the prompt version or catalog changes.
- Dogfood gate per milestone: review one real PR at work end-to-end with the tool before starting the next milestone; write what hurt into `tasks/lessons.md`.

## 7. Risks

| Risk | Mitigation |
|---|---|
| `go/packages` on large modules is slow | FR-13 spike with numbers before anything depends on it; cache; load only changed + reverse importers; UI never waits on it |
| 8B summaries hallucinate | Structured schema with verifiable fields, FR-27 scorer, confidence shown, "observed vs inferred" split in the card |
| Embedding dylibs and Gatekeeper | Curl-installed binaries are not quarantined; `.app` path gets notarized in FR-38; sidecar extracted into Application Support, not `/tmp` |
| GitHub comment positions drift | Anchors carry `head_sha`; preflight in FR-11; stale drafts re-anchored by the user, never silently moved |
| Scope creep into IDE | Non-goals stand; everything outside the slices is in the Later list |

## 8. Decisions (grill, 2026-10-14)

| Topic | Decision |
|---|---|
| UI layer | Go core + web UI in its own WKWebView window from M1 (option A). API boundary kept so a SwiftUI/AppKit front end can replace the web UI if daily use feels web-ish; revisit at the M3 dogfood gate. |
| Performance budget | Strict and tested: ≤16 ms frames, <100 ms file switch, <1 s session open. |
| Languages | Multi-language from M1 via tree-sitter structure (Java, JS/TS, Gherkin, Go, …); exact semantics for Go; Gherkin step linking in M2; LSP providers are the exact-semantics upgrade path for Java/TS. |
| Go toolchain | Downloaded on demand into Application Support, GOTOOLCHAIN=auto; not required on PATH. |
| State store | Application Support only; nothing written into the reviewed repo. |
| Hunk identity | Strict content hash; the body reviewed is stored so the UI can show the diff against it. |
| Model catalog | No silent default; user picks at first run from an eval-ranked, tier-filtered catalog with licence shown. |
| Weights | Download on first use with consent and sha256. |
| Low RAM (8 GB) | Summaries off by default with the reason shown; small tier is an explicit opt-in. |
| Agentic providers | Embedded model only in M4; no cloud API, no external CLI. |
| Spike target | `ledger` is the large-repo benchmark for FR-13. |
| Granularity | 38 + 3 slices at S/M as planned. |
| Platform | arm64 macOS only until M5. |
