# taskhound (`th`)

Issues in a file you can commit. One `.taskhound.yaml` at the root of a repo, a
CLI to drive it, and a kanban board on localhost that can do everything the CLI
can. Several agents and a human can work the same board at once without
corrupting it.

```
th add "Rate-limit the public API" --blocked-by TH-1
th next                     # what can I start right now?
th dependents TH-1          # what does finishing TH-1 unlock?
th ui --open                # kanban board
```

## Install

**Prebuilt binary** (no Go needed):

```bash
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL "https://github.com/iszlai/tools/releases/latest/download/th_${os}_${arch}" -o /tmp/th
install -m 0755 /tmp/th ~/.local/bin/th
```

**From source** (also installs the agent skill):

```bash
git clone https://github.com/iszlai/tools.git && cd tools/taskhound
./install.sh                 # ~/.local/bin/th + ~/.claude/skills/taskhound/
```

`./install.sh --help` lists `--prefix`, `--skill-dir`, `--no-skill` and
`--uninstall`. `make help` lists the build, test and vendor targets.

Then, in the repo you want to track:

```bash
th init          # writes ./.taskhound.yaml — commit it
```

## Model

| Idea | How it works |
|---|---|
| **Issue** | id, title, description, status, labels, comments |
| **Status** | `todo`, `doing`, `done` — that is the whole set |
| **Blocked** | not a status. An issue is blocked when a blocker of it is not `done`, computed on read |
| **Edge** | only `blocked_by` is stored; `blocks` is derived, so the two directions cannot disagree |
| **Ready** | not `done`, and every blocker is `done` — this is what `th next` lists |

Cycles are refused at write time, so the graph is always a DAG and `next` can
never come back empty while open work exists.

## Commands

| Command | Does |
|---|---|
| `th init [--prefix TH]` | create `.taskhound.yaml` here |
| `th add <title>` | `-d BODY` (or `-d -` for stdin), `--blocked-by`, `--blocks`, `--label`, `--status` |
| `th list` | `--status`, `--label`, `--ready`, `--blocked` |
| `th next` | startable now, work in flight first, then whatever unblocks the most |
| `th show <id>` | one issue in full, with both edge directions and comments |
| `th deps <id>` | everything `<id>` transitively waits on |
| `th dependents <id>` | everything that transitively waits on `<id>` |
| `th update <id>` | `--title`, `-d`, `--status`, `--blocked-by`, `--add-blocked-by`, `--remove-blocked-by`, `--blocks`, `--label`, `--unlabel` |
| `th comment <id> <body>` | append a comment |
| `th ui` | `--port` (default 8787), `--open` |
| `th agent-guide` | print the usage guide written for agents |

Every command takes `-f <file>`, and every query takes `--json`. Ids are
case-insensitive and the prefix is optional: `th show 3` is `th show TH-3`.

## The board

`th ui` serves a kanban on `127.0.0.1` — loopback only, since the API writes to
your files.

Four columns: **Blocked**, **Ready**, **Doing**, **Done**. The first is derived
from the dependency edges; the other three are the issue's own status, so
dragging a card between them is what sets it. Click a card to edit its title,
description, status, blockers and labels, or to add a comment. **+ New issue**
files one. The board polls every 2s, so edits made by the CLI or by another
agent appear on their own — except while the editor drawer is open, which would
stomp on your typing.

The HTTP API is the same surface as the CLI:

```
GET    /api/board                    every issue, plus blocks/open_blockers/ready
POST   /api/issues                   {title, description, status, blocked_by, labels}
PATCH  /api/issues/{id}              any subset of those fields
POST   /api/issues/{id}/comments     {body, author}
```

## Concurrency

Every write is a read-modify-write while holding an exclusive `flock`, and lands
by writing a temp file and renaming it over the store. So a reader sees either
the old file or the new one, never half of one, and two instances writing at the
same moment cannot both build on the same stale snapshot.

The lock is a sidecar, `.taskhound.yaml.lock`, rather than the store itself:
renaming the temp file swaps the inode, and a lock held on the old inode would
be guarding nothing. Add the lock file to `.gitignore`; commit the board.

`go test` runs 24 concurrent `th add` processes and 20 concurrent `th comment`
processes against one file and asserts nothing is lost. POSIX only — `flock`
has no Windows equivalent here.

## For agents

`install.sh` puts a skill in `~/.claude/skills/taskhound/`, so Claude Code picks
up taskhound on its own. Without the skill, `th agent-guide` prints the same
document to stdout — pipe it into whatever you are driving.

The one rule: **pass `--json` whenever you are going to read the output.** The
plain output is a table for humans; `--json` carries `blocks`, `open_blockers`
and `ready` already computed.

```bash
th next --json | jq -r '.[0].id'                  # what to pick up
th dependents TH-3 --json | jq -r '.[].id'        # what closing it unlocks
th list --blocked --json | jq -r '.[] | "\(.id) waits on \(.open_blockers|join(","))"'
```

### Migrating an existing todo list

If you are an agent holding a `tasks/todo.md`, a markdown checklist, or an
in-memory todo list, move it here in one pass. Nothing about this is
taskhound-specific except the last two steps.

**1. Install and initialise.**

```bash
command -v th >/dev/null || (cd /path/to/tools/taskhound && ./install.sh)
th list >/dev/null 2>&1 || th init
```

**2. Read the source list.** Each `- [ ]` / `- [x]` line, heading or numbered
step becomes one issue. Keep the wording; do not summarise it away.

**3. Work out the order before writing anything.** taskhound's value over a
markdown checklist is the edges, so decide, for each item, which other items
genuinely have to be finished first. Two things to watch:

- A checklist is written top to bottom, but that order is often just the order
  someone thought of them. Only make B wait on A if A really gates B.
- Blockers must exist before the issues that name them, so file in dependency
  order: everything with no blockers first.

**4. File them, capturing status and edges as you go.**

```bash
schema=$(th add "Add the ledger schema" -d - <<'EOF'
What to build: tables, indexes and the migration.

Acceptance criteria:
- [ ] migration runs clean on an empty database
- [ ] rollback tested
EOF
)
api=$(th add "Expose the ledger API" --blocked-by "$schema" --label ready-for-agent)
th add "Ledger screen" --blocked-by "$api"

th update "$schema" --status done      # items already ticked in the source list
```

Use `-d -` with a heredoc for anything longer than a line: it lands in the YAML
as a literal block, so later edits diff one line at a time.

**5. Check the graph says what you meant, then delete the old list.**

```bash
th next                # should be the items you could genuinely start today
th list --blocked      # each line should name a blocker you agree with
git rm tasks/todo.md && git add .taskhound.yaml
```

If `th next` is empty you have written a cycle or made everything depend on
something unfinished; if it lists nearly everything, you have not written the
edges that matter.

## Layout

```
taskhound/
├── main.go              CLI: commands, flags, output
├── store.go             model, dependency graph, locking, atomic save
├── web.go               HTTP server and JSON API
├── ui.html              the kanban board, embedded into the binary
├── skill/taskhound/     the agent skill, also embedded for `th agent-guide`
├── store_test.go        graph and persistence
├── e2e_test.go          the real binary: CLI, HTTP, concurrency
├── Makefile             build, test, install, vendor
└── install.sh
```

Dependencies are vendored (`gopkg.in/yaml.v3`), so `make build` and `make test`
work with no network and no module proxy.
