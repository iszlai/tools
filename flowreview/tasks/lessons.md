# Lessons

## 2026-10-14 — "merged into main" does not mean "finished"

**What happened:** A cleanup loop removed every agent worktree whose branch was `git branch --merged main`. Running agents that had not committed yet sit exactly at a main ancestor, so they counted as merged: four in-flight worktrees (FR-6, FR-12, FR-14, FR-27) were deleted with their uncommitted work.

**Rules:**
1. Only remove a worktree after its agent's completion notification, by explicit id; never by a "merged" heuristic.
2. Tell agents to make WIP commits early, so their work is in the branch and not only on disk.
