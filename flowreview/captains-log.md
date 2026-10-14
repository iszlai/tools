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
