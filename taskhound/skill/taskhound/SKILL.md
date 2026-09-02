---
name: taskhound
description: Track work as issues in a version-controlled .taskhound.yaml using the `th` CLI. Use whenever the user asks to file, list, update, comment on, or query issues/tickets/tasks in a repo that has a .taskhound.yaml, when they ask "what's next" or "what depends on X", or when a skill (such as to-tickets) needs a local issue tracker with real blocking edges rather than loose markdown files.
---

# taskhound

`th` keeps issues in one YAML file at the root of the repo, `.taskhound.yaml`,
found by walking up from the working directory. Every write takes an exclusive
lock and lands atomically, so it is safe to run several agents and a human
against the same board at once.

Check the tool is present and the repo has a board before using it:

```bash
th list >/dev/null 2>&1 || echo "no board here — run: th init"
```

## Rules

- **Pass `--json` whenever you are going to read the output.** The plain output
  is a table for humans; `--json` gives you `id`, `status`, `blocked_by`,
  `blocks`, `open_blockers`, `ready`, `labels` and `comments` per issue.
- **Store one direction of each edge.** Only `blocked_by` is stored; `blocks`
  is derived. `--blocks X` is sugar that writes the edge onto X. Cycles are
  refused.
- **Priorities are `must`, `high`, `normal`, `low`**, defaulting to `normal`.
  `must` outranks everything in `th next`, including work in flight; `low` sorts
  last but is still offered when nothing else is ready. Reserve `must` for
  drop-everything work — if you mark several issues `must`, you have not
  prioritised anything.
- **Statuses are `todo`, `doing`, `done`.** There is no `blocked` status — an
  issue is blocked when a blocker of it is not yet `done`, and that is computed.
- **Create blockers before the issues that depend on them**, so the ids exist.
- **Do not hand-edit `.taskhound.yaml`** while agents are running; go through
  `th` so the lock is honoured.

## Commands

```bash
th init                                   # create .taskhound.yaml here
th add "Title" -d "Body"                  # prints the new id, e.g. TH-4
th add "Title" -d - <<'EOF'               # long description from stdin
multi-line body
EOF
th add "Title" --blocked-by TH-1,TH-2     # declare blocking edges up front
th add "Title" --blocks TH-9              # make TH-9 wait on the new issue
th add "Title" --priority must             # must|high|normal|low, default normal
th add "Title" --label ready-for-agent --json

th list --json                            # everything
th list --status doing --json
th list --ready --json                    # nothing open blocking them
th list --label ready-for-agent --json
th list --priority must --json

th next --json                            # startable now, best leverage first
th show TH-3 --json                       # one issue, full detail
th deps TH-3 --json                       # all TH-3 transitively waits on
th dependents TH-3 --json                 # all that transitively wait on TH-3

th update TH-3 --status doing
th update TH-3 --priority high
th update TH-3 --title "New title" -d "New body"
th update TH-3 --add-blocked-by TH-1 --remove-blocked-by TH-2
th update TH-3 --blocked-by TH-1,TH-4     # replace the whole blocker list
th update TH-3 --label needs-review --unlabel ready-for-agent
th comment TH-3 "Ran the migration on staging, green."

th archive --dry-run                      # what would leave the board
th archive --older-than 30d               # move long-finished work to the done log
th archive --list --json                  # read the done log
th sync --dry-run                         # what would go to GitHub Issues
th sync --repo owner/name                 # push the board to GitHub Issues
th ui --port 8787 --open                  # kanban board on localhost
```

`th next` ranks by priority first, then work already `doing`, then whatever
unblocks the most other issues — so the top row is the thing to pick up.

## The done log

`th archive` moves issues finished before a cutoff (default 14 days) into
`.taskhound-done.yaml`, so `th list` stays about the work that is left. Only
`done` issues move; references to them are dropped from the issues that stay,
which cannot change what is ready. `th show <id>` still resolves an archived id.
Ids are never reused.

Run it when a board has accumulated closed work, not after every ticket.

## Recipes

**Pick up the next piece of work.**

```bash
th next --json | jq -r '.[0].id'          # then: th update <id> --status doing
```

**Answer "what does finishing this unlock?"**

```bash
th dependents TH-3 --json | jq -r '.[].id'
```

**Close a slice and report what it opened up.**

```bash
th update TH-3 --status done
th next --json | jq -r '.[] | "\(.id)  \(.title)"'
```

**Compact a board after finishing a batch.**

```bash
th archive --older-than 0 --dry-run   # confirm the list first
th archive --older-than 0
```

**Check nothing is stuck.** An open issue with a blocker that will never be
done is a stalled board:

```bash
th list --blocked --json | jq -r '.[] | "\(.id) waits on \(.open_blockers | join(","))"'
```

## Pushing to GitHub Issues

`th sync` pushes the board to GitHub Issues via `gh`, one way. It records the
issue number on each card, so it is safe to run again — a second run edits
rather than duplicates. Blockers go up first and are cited as `#N` in the body,
since GitHub has no blocking relation in its API. `done` closes the issue,
`doing` adds a `doing` label, labels are created on the fly, and comments are
posted exactly once.

Always show the user `th sync --dry-run` before running it for real: it files
public issues on their repository.

Nothing is pulled back down. The board stays authoritative.

## Using taskhound as the tracker for `to-tickets`

When `to-tickets` (or any breakdown step) asks where to publish, taskhound is a
real tracker: it has native blocking edges, so publish issues instead of writing
one markdown file per ticket.

Publish **in dependency order — blockers first** — so each ticket can name real
ids:

```bash
first=$(th add "Prefactor: extract the rate limiter" -d - <<'EOF'
What to build: ...

Acceptance criteria:
- [ ] ...
EOF
)
th add "Rate-limit the public API" --blocked-by "$first" --label ready-for-agent
```

Then hand the frontier to whoever is implementing:

```bash
th next --json
```
