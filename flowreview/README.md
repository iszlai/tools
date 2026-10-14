# flowreview

flowreview reviews a local diff or a GitHub pull request in code-flow order,
in its own native window. One binary, no browser tab, no separate server to
run.

## Install

```sh
make build            # builds bin/flowreview (embeds llama-server only if
                       # third_party/llama/dist/ has been populated by
                       # `make fetch-llama`; otherwise falls back to a stub
                       # model provider, which is the normal case for a dev
                       # checkout)
make install          # installs bin/flowreview into $PREFIX (default
                       # $HOME/.local/bin); make sure that is on your PATH
```

`make build` requires `CGO_ENABLED=1` (the Makefile sets this itself) because
the native window and the tree-sitter grammars are cgo. Requires a GitHub
token for the `pr` command: either `gh auth login` once, or set
`GITHUB_TOKEN`/`GH_TOKEN`.

## Commands

### `flowreview`

Review the current worktree's uncommitted changes against `HEAD`, in the
repository containing the current directory.

### `flowreview diff <ref|range>`

Review one commit (`<ref>` has no `..`) or a range (`A..B` or `A...B`,
interpreted exactly as `git diff` interprets them).

```sh
flowreview diff HEAD~3
flowreview diff main..feature
flowreview diff main...feature   # diffs feature against the merge-base of main and feature
```

### `flowreview pr <url|owner/repo#N|N>`

Review a GitHub pull request. Accepts a full URL, the `owner/repo#N`
shorthand, or a bare number — a bare number is resolved against the `origin`
remote of the repository in the current directory, so it only works from
inside a clone of that PR's repository.

```sh
flowreview pr https://github.com/cli/cli/pull/123
flowreview pr cli/cli#123
flowreview pr 123   # from inside a clone of cli/cli
```

The PR's diff, head/base SHAs and existing comments are fetched from GitHub;
its commits are fetched into a local clone — the repository in the current
directory when its `origin` already names the same owner/repo, otherwise a
cache clone under the data directory (see below), fetched once and reused on
later opens. Comments already on the PR are shown read-only in the UI,
fetched from `GET /api/remote-comments`; publishing your own comments back is
FR-11 and not part of this slice.

### `flowreview patch <file>` / `flowreview --patch <file>`

Review a standalone patch file (anything `git diff` or `diff -u` could have
produced). Both forms are equivalent; `--patch` is also accepted mixed with
no other subcommand.

```sh
flowreview patch change.diff
flowreview --patch change.diff
```

### `flowreview sessions [--json] [--all]`

List review sessions: id, title, source, when it was last updated, and
reviewed/total hunk progress.

- With no flags: sessions for the repository containing the current
  directory.
- `--all`: sessions across every repository flowreview has ever reviewed.
- `--json`: machine-readable output (one array of objects).

The total hunk count is recomputed from a local, offline diff where that is
cheap to do — a plain `git diff` for a worktree/commit/range/patch session, or
a diff between the PR's already-fetched base/head SHAs for a PR session.
Never a network call. When that isn't possible (the repo or clone has moved
or been removed), `total_hunks` is omitted from JSON output and shown as `?`
in the table, while the reviewed count (always known, straight from the
session file) is still shown.

```sh
$ flowreview sessions
ID         TITLE              SOURCE              UPDATED               PROGRESS
worktree   Worktree           worktree            2026-10-14T10:02:00Z  2/5
pr-cli-cli-123  fix: ...      pr cli/cli#123      2026-10-13T18:41:00Z  0/3
```

### `flowreview model <command>`

Manage the embedded local model used for summaries (FR-25/FR-26). See
`flowreview model` with no arguments for its own usage; briefly:
`list | install <name> | rm <name> | status`.

### `flowreview toolchain list|rm <v>`

Manage Go toolchains flowreview downloaded on demand (FR-32) to analyze a
repo without requiring `go` on `PATH`.

```sh
flowreview toolchain list        # installed versions and their GOROOT
flowreview toolchain rm 1.22.3   # delete a cached toolchain
```

### `flowreview version`

Print the version flowreview was built with (`dev` for a plain `go build`
outside `make build`/`make release`, which stamp it via `-ldflags`).

### `flowreview help` / `flowreview -h`

Print a short summary of the commands, flags and exit codes above.

## Flags (review commands only)

These apply to the bare `flowreview`, `diff`, `pr` and `patch` commands —
not to `sessions`, `model`, `toolchain`, `version` or `help`. A flag may
appear before or after the positional argument:
`flowreview --no-open diff main..feature` and
`flowreview diff main..feature --no-open` are equivalent.

| Flag | Meaning |
|---|---|
| `--no-open` | Serve only: print the session URL on stdout, open no window and no browser. Used by tests and scripts. |
| `--browser` | Open the system default browser instead of the native window. Used by the Playwright smoke harness; not the normal way to use flowreview day to day. |
| `--port <n>` | HTTP port to serve on. `0` (the default) picks a free port. |

If a `flowreview` instance is already running, a second invocation hands its
source to that instance over `POST /api/open` and asks it to bring its window
forward, instead of starting a second server.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Ok |
| `1` | Runtime error (anything not classified below) |
| `2` | Usage error — bad flags or arguments |
| `3` | Not a git repository, or the requested source was not found (missing patch file, 404 from the host, a bare PR number typed outside any clone of its repo) |
| `4` | Auth or host error — no GitHub token found, the host rejected the token, rate limited, or otherwise would not answer |

## Data locations

Everything flowreview writes lives under one data directory, never inside the
repository being reviewed:

```
~/Library/Application Support/FlowReview/
├── sessions/<repo-key>/<session-id>.json   review sessions (FR-4)
├── repos/<owner>/<repo>/                   cached clones used to review a PR
│                                           when origin doesn't already match
├── models/<name>.gguf                      downloaded summarizer models (FR-26)
├── bin/go/<version>/                       downloaded Go toolchains (FR-32)
└── bin/llama/<version>/                    extracted llama-server (FR-25)
```

Set `FLOWREVIEW_HOME` to use a different directory (tests use this to isolate
state between runs; it's also how you'd point flowreview at a second profile).

## Manual check against a real PR

With `gh` installed and logged in (`gh auth login`), from inside a clone of a
repository with recent pull requests:

```sh
make build
bin/flowreview pr <owner>/<repo>#<N> --no-open
# -> prints http://127.0.0.1:<port>/
curl -s http://127.0.0.1:<port>/api/session | jq .
curl -s http://127.0.0.1:<port>/api/remote-comments | jq .
# Ctrl-C to stop
```

Confirm the session title matches the PR's title, `source.kind` is `"pr"`,
and `/api/files` lists the PR's changed files. This is read-only: flowreview
only fetches the PR's metadata, diff and comments, and fetches its commits
into a local clone — it never pushes, comments, or otherwise writes to the
repository on GitHub.
