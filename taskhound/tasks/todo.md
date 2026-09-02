# taskhound — todo

Goal: a small CLI (`th`) over a version-controllable YAML file, safe under
concurrent instances, with a localhost kanban UI that can do everything the CLI
can, and a skill so agents can drive it.

## Plan

- [x] Decide shape: Go single binary, YAML store (vendored `yaml.v3`), full read/write UI
- [x] `store.go` — model, file resolution, flock + atomic write, dependency graph
- [x] `main.go` — `init add list next show deps dependents update comment ui agent-guide`
- [x] `web.go` + `ui.html` — kanban board, drag between columns, edit drawer, comments
- [x] `skill/taskhound/SKILL.md` — how an agent drives `th`, wired to `to-tickets`
- [x] `Makefile` + `install.sh`
- [x] Tests: graph unit tests + end-to-end CLI/HTTP/concurrency test
- [x] README section in the repo root README

## Review

**What shipped.** `th` stores every issue in one `.taskhound.yaml` found by walking
up from the cwd, the way git finds `.git`. Only `blocked_by` edges are stored;
`blocks` is derived on read, so the two directions can never disagree. Cycles are
refused at write time.

**Concurrency.** Every write is read-modify-write while holding an exclusive
`flock` on a sidecar `.taskhound.yaml.lock`, and lands via temp-file + `rename`,
so a reader either sees the old file or the new one, never a half-written one.
The lock is on the sidecar rather than the store itself because `rename` swaps
the inode out from under any lock held on it. `TestConcurrentAdds` runs 24
processes adding at once and asserts 24 distinct issues survive.

**Deliberately left out.** No delete (nothing asked for it; `status: done` and
git history cover it), no auth on the UI (it binds `127.0.0.1` only), no
Windows support (`flock` is POSIX).
