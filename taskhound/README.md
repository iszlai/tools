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
mkdir -p ~/.local/bin
curl -fsSL "https://github.com/iszlai/tools/releases/latest/download/th_${os}_${arch}" -o ~/.local/bin/th
chmod +x ~/.local/bin/th
```

Builds are published for darwin and linux on amd64 and arm64.

**From source** (also installs the agent skill):

```bash
git clone https://github.com/iszlai/tools.git && cd tools/taskhound
./install.sh                 # ~/.local/bin/th + ~/.claude/skills/taskhound/
```

Then, in the repo you want to track:

```bash
th init          # writes ./.taskhound.yaml — commit it
```

## Upgrading

Whichever way you installed, `th version` says what you have, and the upgrade is
the same command you used the first time.

**If you installed the prebuilt binary**, re-run the same curl: it always
resolves to the latest release and overwrites in place.

```bash
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL "https://github.com/iszlai/tools/releases/latest/download/th_${os}_${arch}" -o ~/.local/bin/th
chmod +x ~/.local/bin/th
```

**If you have the repo**, `./install.sh --update` rebuilds from the checkout
when Go is present and otherwise pulls the latest published build. Either way it
prints what changed, and refreshes the agent skill while it is there:

```
$ ./install.sh --update
downloading latest for darwin/arm64...
updated ~/.local/bin/th: v0.2.0 -> v0.3.0
```

**If you have neither** — no checkout, no Go, and you would rather not hand-roll
the curl — fetch the installer alone and let it do the work. It is the only
piece you need:

```bash
curl -fsSL https://raw.githubusercontent.com/iszlai/tools/main/taskhound/install.sh -o /tmp/th-install.sh
bash /tmp/th-install.sh --from-release
```

`--from-release taskhound-v0.2.0` pins a version instead of taking the latest,
and any of these say so and change nothing when you are already current:

```
$ ./install.sh --update
already on the latest release (v0.3.0) — nothing to do
```

`./install.sh --help` lists every mode plus `--prefix`, `--skill-dir`,
`--no-skill` and `--uninstall`. `make help` lists the build, test and vendor
targets.

**Mixing versions.** From v0.4.0 on, a board survives being written by a build
older than the one that wrote it: saving re-attaches every key the running
binary has no field for, so a v0.4.0 `th` editing a board full of fields added
in v0.9.0 keeps them. A board whose `version` is higher than the binary
understands is refused outright rather than rewritten into something lossy.

Fields the binary *does* know can still be removed, which is the point — the
merge is decided from the struct, not from what happens to be in the file, so
clearing a description or dropping a label still sticks.

**v0.3.0 and earlier do not do this.** They drop unknown keys silently: v0.2.0
writing a board made by v0.3.0 removes every priority on it, with no error. If
anything on your board still runs one of those, upgrade it — `th version` says
which it has.

## Model

| Idea | How it works |
|---|---|
| **Issue** | id, title, description, status, labels, comments |
| **Status** | `todo`, `doing`, `done` — that is the whole set |
| **Blocked** | not a status. An issue is blocked when a blocker of it is not `done`, computed on read |
| **Edge** | only `blocked_by` is stored; `blocks` is derived, so the two directions cannot disagree |
| **Ready** | not `done`, and every blocker is `done` — this is what `th next` lists |
| **Priority** | `must`, `high`, `normal`, `low`. `normal` unless you say otherwise |

Cycles are refused at write time, so the graph is always a DAG and `next` can
never come back empty while open work exists.

## Commands

| Command | Does |
|---|---|
| `th init [--prefix TH]` | create `.taskhound.yaml` here |
| `th add <title>` | `-d BODY` (or `-d -` for stdin), `--blocked-by`, `--blocks`, `--label`, `--status`, `--priority` |
| `th list` | `--status`, `--priority`, `--label`, `--ready`, `--blocked` |
| `th next` | startable now, best leverage first |
| `th show <id>` | one issue in full, with both edge directions and comments |
| `th deps <id>` | everything `<id>` transitively waits on |
| `th dependents <id>` | everything that transitively waits on `<id>` |
| `th update <id>` | `--title`, `-d`, `--status`, `--priority`, `--blocked-by`, `--add-blocked-by`, `--remove-blocked-by`, `--blocks`, `--label`, `--unlabel` |
| `th comment <id> <body>` | append a comment |
| `th archive` | move long-finished issues into the done log; `--older-than`, `--dry-run`, `--list` |
| `th sync` | push the board to GitHub Issues; `--repo`, `--dry-run` |
| `th ui` | `--port` (default 8787), `--open` |
| `th agent-guide` | print the usage guide written for agents |

Every command takes `-f <file>`, and every query takes `--json`. Ids are
case-insensitive and the prefix is optional: `th show 3` is `th show TH-3`.

## Priority

Every issue is `must`, `high`, `normal` or `low`, and `normal` unless you say
otherwise:

```bash
th add "Production is down" --priority must
th update TH-3 --priority low
th list --priority high
```

- **must** outranks everything, including work already in flight. It is for the
  thing you drop everything for, so use it sparingly — a board where everything
  is a must is a board with no priorities.
- **low** sorts after everything else, but it is still offered when nothing else
  is ready. Last, not never.

The default is stored as nothing at all, so an unprioritised board keeps exactly
the file it always had, and a diff only ever shows a priority somebody chose.

### Urgency: nothing is less urgent than what waits on it

A `low` chore blocking a `must` **is** a `must` — the must cannot start until the
chore is done, so calling the chore low is just wrong. The priority you set is a
floor; the graph raises it:

```
$ th show TH-2
TH-2  Rotate the staging credentials
status:    todo
priority:  low (raised to must by TH-3, which waits on this)
```

That raised value is the issue's **urgency**, and it is what every ranking, every
`PRI` column and `th list --priority` actually use. It travels the whole chain,
not one edge, and it comes from open work only — finish or unblock the issue
above and the borrowed urgency goes away on its own.

Urgency is derived and never stored, exactly like "blocked". Nothing rewrites
your file behind your back, and the file can never hold a priority the graph
disagrees with. `--json` carries both: `priority` is what you set,
`urgency` is what it amounts to, and `urgency_from` names the issue that raised
it. Tables print urgency with a `↑` when it was inherited:

```
ID    PRI    STATUS  UNBLOCKS      TITLE
TH-2  must↑  todo    1 (1 urgent)  Rotate the staging credentials
```

### What `th next` actually ranks by

A `must` first, whatever else is true of it. After that the queue is about
**leverage** rather than the issue's own priority, because a `high` issue you
cannot start yet is worth no more than the thing standing in front of it:

1. `must` — including a `must` inherited from something waiting on it
2. whatever frees the most **urgent** work — open `must` or `high` issues
   transitively waiting on it
3. whatever frees the most work at all
4. the issue's own urgency
5. work already `doing` before work not started, then id order

Steps 2 and 3 count the **whole transitive fan-out**, not the direct edges. An
issue blocking one issue that in turn blocks four unblocks five, and outranks the
head of a three-long chain, which unblocks two — both have exactly one edge
leaving them, and the edge is not what matters.

So a `low` chore that a `high` issue is stuck behind outranks a `high` issue
nobody is waiting on. That is the point: the chore *is* the high issue.

```
$ th next
ID    PRI     STATUS  UNBLOCKS      TITLE
TH-7  low     todo    1 (1 urgent)  Rotate the staging credentials
TH-2  normal  doing   2             Extract the client
TH-4  high    todo    0             Write the launch post
```

The counts are in `--json` too, as `unblocks` and `unblocks_urgent`. Neither
counts a dependent that is already `done` — finishing something cannot unblock
work that is already finished.

TH-7 is `low` and top of the queue because a `high` is stuck behind it; TH-4 is
`high` and last because nothing is.

## When the board jams

`SetBlockedBy` refuses to create a cycle, so a board built entirely through `th`
cannot deadlock: follow the blockers back from any open issue and a DAG has to
end at something with none, which is ready by definition.

The file is not built entirely through `th`, though. People edit it, merges
resolve it, and before v0.4.0 an older binary could rewrite it. So the graph on
disk can hold a loop the API would have rejected, or a blocker naming an issue
that is not there — which blocks forever, because a blocker that cannot be found
is never done.

`th next` checks for both, and an empty queue never passes silently as a
finished board:

```
$ th next
loop: TH-1 → TH-2 → TH-1
missing: TH-3 is blocked by TH-42, which is not on the board
nothing is startable: every open issue is waiting on another, in a loop (TH-1 → TH-2)
forced pick: TH-2  Expose the API
start it anyway, or cut the edge: th update TH-2 --remove-blocked-by TH-1
```

The forced pick is the highest-priority open issue, then whatever unblocks the
most — because finishing that is what breaks the loop. A loop is reported even
when other work is ready, since a corrupt graph will bite later, and a loop made
entirely of `done` issues is history rather than a problem, so it is ignored.

The diagnosis goes to **stderr**, so `th next --json` stays a clean array for
`jq`. On a jammed board that array holds the forced pick, carrying
`"forced": true` and `"forced_reason"`, which means `.[0].id` gives you
something to start whatever state the board is in.

## The done log

A board that keeps every issue you ever closed stops being a board. `th archive`
lifts the long-finished ones off it into `.taskhound-done.yaml`, which sits
beside the board and gets committed with it.

```bash
th archive --dry-run                 # what would move, changes nothing
th archive                           # done at least 14 days ago
th archive --older-than 30d          # or 2w, or 48h, or 0 for everything done
th archive --list                    # read the done log
th show TH-1                         # still resolves after TH-1 is archived
```

Only `done` issues move, and moving them cannot change the board: references to
an archived issue are dropped from whatever stayed behind, which is safe exactly
because a done blocker already contributed nothing to whether anything was
ready. Dropping the reference — rather than leaving a dangling id — keeps every
`blocked_by` on the board resolvable. Ids are never reused, so nothing that has
been archived can come back as new work.

The log is only ever appended to, and it is written before the board is
rewritten: interrupted between the two, an issue is duplicated rather than lost,
and a duplicate is something you can see.

## GitHub Issues

`th sync` pushes the board to GitHub Issues through the `gh` CLI. It goes one
way. The board stays the source of truth; GitHub gets a readable shadow of it,
which is where other people can see the work.

```bash
th sync --dry-run                    # what it would create and update
th sync                              # the repo gh infers here
th sync --repo owner/name
```

It is safe to run again. The GitHub number is written back onto each issue as
soon as it is filed, so a second run edits what is already there instead of
filing it twice — and a crash mid-run costs a half-populated issue rather than a
duplicate.

| taskhound | GitHub |
|---|---|
| title, description | title, body |
| `blocked_by` | **Blocked by:** #12, #15 in the body |
| id | a `taskhound: TH-4` backlink in the body |
| labels | labels, created on the fly if missing |
| `status: doing` | still open, plus a `doing` label |
| `status: done` | closed |
| comments | comments, each posted exactly once |

Issues go up blockers-first, because a body cannot cite a number that does not
exist yet. GitHub has no native blocking relation in its API, which is why the
edge is text there and stays real here — `th next` and `th dependents` keep
working off the graph regardless of what GitHub thinks.

Nothing comes back down. Close an issue on GitHub and the board will reopen it
on the next sync, because the board is what is authoritative.

## The board

`th ui` serves a kanban on `127.0.0.1` — loopback only, since the API writes to
your files.

Title and description are shown, not offered for editing: a drawer opened to
read something cannot be typed over by accident. The ✎ beside either label — or
a click on the value itself — swaps in the field, which goes back to being a
value when you click away. A new issue opens with the title field already up,
since there is nothing there to read.

Descriptions are markdown. The drawer shows them rendered — headings, code,
and the `- [ ]` acceptance criteria every ticket is made of. The field is always
the value; the block beside it is only ever a view
of it, so saving does not care which one you are looking at. Rendering is
`mdlite`, copied in from `mdlite/md.js` and served out of the binary at
`/md.js`; it escapes before it renders, so a description that arrives over the
API cannot inject markup.

Four columns: **Blocked**, **Ready**, **Doing**, **Done**. The first is derived
from the dependency edges; the other three are the issue's own status, so
dragging a card between them is what sets it. **Sort** in the header reorders
the cards inside every column — by `created` (id order, which is the order the
file is in, and the default), by `priority`, or by `unblocks`, the count on the
card. The choice is remembered per browser. Click a card to edit its title,
description, status, blockers and labels, or to add a comment. Every id the
board shows is a link into that issue: the ⛔ chips on a card, and the
**Blocked by** and **Blocks** lists in the drawer, each open the issue they
name — so you can walk the dependency graph in either direction without
touching the CLI. **+ New issue**
files one. The board polls every 2s, so edits made by the CLI or by another
agent appear on their own — except while the editor drawer is open, which would
stomp on your typing.

The HTTP API is the same surface as the CLI:

```
GET    /api/board                    every issue, plus blocks/open_blockers/ready/unblocks/urgency
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
th archive --older-than 30d          # keep the board about the work that is left
th sync --dry-run                    # what would go up to GitHub Issues
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

If you do not have the repo checked out, grab the binary with the `curl` line
under **Install** instead.

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

If the source list was long and mostly ticked, file the finished items anyway
and then run `th archive --older-than 0` — the history lands in the done log
and the board opens on the work that is actually left.

## Smoke test

Which column a card lands in, whether a drag actually moves it, whether the
drawer writes back, whether the poll notices the CLI — none of that is visible
to a Go test. `tests/smoke.js` drives the real board in headless Chromium and
checks 25 of them, including that a refused write leaves both the board and the
open drawer alone.

```bash
npm install playwright-core
PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
make smoke
```

It needs Playwright's own chromium — corporate-managed Chrome refuses to be
automated. It is not part of `make check`, so CI stays free of a browser
download; run it before touching `ui.html`.

## Layout

```
taskhound/
├── main.go              CLI: commands, flags, output
├── store.go             model, dependency graph, locking, atomic save
├── compat.go            keeps unknown fields across versions
├── web.go               HTTP server and JSON API
├── ui.html              the kanban board, embedded into the binary
├── md.js                a copy of mdlite/md.js, also embedded
├── skill/taskhound/     the agent skill, also embedded for `th agent-guide`
├── store_test.go        graph and persistence
├── e2e_test.go          the real binary: CLI, HTTP, concurrency
├── compat_test.go       fields written by a newer th survive an older one
├── tests/smoke.js       the board in a real browser
├── Makefile             build, test, install, vendor
└── install.sh
```

Dependencies are vendored (`gopkg.in/yaml.v3`), so `make build` and `make test`
work with no network and no module proxy.
