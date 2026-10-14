# Lessons

## 2026-10-14 — "merged into main" does not mean "finished"

**What happened:** A cleanup loop removed every agent worktree whose branch was `git branch --merged main`. Running agents that had not committed yet sit exactly at a main ancestor, so they counted as merged: four in-flight worktrees (FR-6, FR-12, FR-14, FR-27) were deleted with their uncommitted work.

**Rules:**
1. Only remove a worktree after its agent's completion notification, by explicit id; never by a "merged" heuristic.
2. Tell agents to make WIP commits early, so their work is in the branch and not only on disk.

## Run test instances of flowreview with an isolated FLOWREVIEW_HOME (2026-10-14)
**What happened.** A manual end-to-end run (`flowreview diff … --no-open`) found the user's own window holding the single-instance lock, so it handed its source to that window via /api/open and switched the user's open review to my test diff.
**Rule.** Every manual or scripted test run uses `FLOWREVIEW_HOME=$(mktemp -d)`; symlink `models/` from the real home if the real model is needed. Never run a bare `flowreview` while the user might have a window open.
