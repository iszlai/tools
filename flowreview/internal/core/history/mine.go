package history

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/git"
)

// recordSep separates commits in the git log output below, the same way
// internal/core/git/log.go does: a control character that never appears in a
// path, a sha, or a commit message.
const recordSep = "\x1e"

// mine runs a single `git log --numstat` over repo and folds it into a
// snapshot: per-file commit/churn totals and per-pair co-change support.
// --numstat gives both the file list and line counts in one pass, so this one
// git invocation feeds both Companions and Hotspot.
func mine(ctx context.Context, repo *git.Repo, opts Options) (*snapshot, error) {
	cutoff := time.Now().Add(-opts.Since).Format(time.RFC3339)
	out, err := runGit(ctx, repo.Root(),
		"log", "--no-color", "--numstat",
		"--since="+cutoff,
		"-n", strconv.Itoa(opts.MaxCommits+1), // +1 so we can tell "hit the cap" from "exactly this many exist"
		"--pretty=format:"+recordSep+"%H",
	)
	if err != nil {
		return nil, fmt.Errorf("history: mine: %w", err)
	}

	commits := parseNumstatLog(string(out))
	degraded := ""
	if len(commits) > opts.MaxCommits {
		commits = commits[:opts.MaxCommits]
		degraded = fmt.Sprintf("capped at %d commits", opts.MaxCommits)
	}

	snap := &snapshot{
		Commits:           len(commits),
		Degraded:          degraded,
		Since:             opts.Since,
		MaxCommits:        opts.MaxCommits,
		MaxFilesPerCommit: opts.MaxFilesPerCommit,
		Files:             map[string]FileStats{},
	}
	pairs := map[pairKey]int{}

	for _, c := range commits {
		if len(c.files) > opts.MaxFilesPerCommit {
			// A mechanical commit (repo-wide reformat, vendor bump, ...)
			// would flood every file it touches with bogus co-change
			// support, so it is dropped entirely rather than partially
			// counted.
			continue
		}
		seen := make(map[string]bool, len(c.files))
		paths := make([]string, 0, len(c.files))
		for _, f := range c.files {
			fs := snap.Files[f.path]
			fs.Commits++
			fs.Added += f.added
			fs.Deleted += f.deleted
			snap.Files[f.path] = fs
			if !seen[f.path] { // numstat should never repeat a path within a commit; guard anyway
				seen[f.path] = true
				paths = append(paths, f.path)
			}
		}
		sort.Strings(paths)
		for i := 0; i < len(paths); i++ {
			for j := i + 1; j < len(paths); j++ {
				pairs[pairKey{paths[i], paths[j]}]++
			}
		}
	}

	snap.Pairs = make([]PairStats, 0, len(pairs))
	for k, support := range pairs {
		snap.Pairs = append(snap.Pairs, PairStats{A: k.a, B: k.b, Support: support})
	}
	return snap, nil
}

type pairKey struct{ a, b string }

// runGit shells out to git directly, rooted at dir (a git.Repo's Root()).
// FR-2's Repo keeps its own invocation private and has no name-only/numstat
// log mode, so mining gets its own minimal runner rather than extending
// FR-2's API for a single call site.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type numstatCommit struct {
	files []numstatFile
}

type numstatFile struct {
	path           string
	added, deleted int
}

// parseNumstatLog splits `git log --numstat --pretty=format:<recordSep>%H`
// output into one numstatCommit per commit record.
func parseNumstatLog(out string) []numstatCommit {
	var commits []numstatCommit
	for _, rec := range strings.Split(out, recordSep) {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		lines := strings.Split(rec, "\n") // lines[0] is the commit sha from %H; the rest are numstat rows
		var c numstatCommit
		for _, l := range lines[1:] {
			if l == "" {
				continue
			}
			parts := strings.SplitN(l, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			// Binary files report "-" for both counts; Atoi's error is
			// discarded on purpose, leaving added/deleted at 0, which is the
			// right churn contribution for a file whose diff has no lines.
			added, _ := strconv.Atoi(parts[0])
			deleted, _ := strconv.Atoi(parts[1])
			c.files = append(c.files, numstatFile{
				path:    renamedPath(parts[2]),
				added:   added,
				deleted: deleted,
			})
		}
		commits = append(commits, c)
	}
	return commits
}

// renamedPath collapses git's numstat rename notation to the file's current
// path: "old => new" when there is no common prefix/suffix, or
// "dir/{old => new}/tail" when there is. Co-change mining only cares about
// the one path a file is known by today.
func renamedPath(raw string) string {
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.Index(raw[i:], "}"); j >= 0 {
			j += i
			prefix, inner, suffix := raw[:i], raw[i+1:j], raw[j+1:]
			if k := strings.Index(inner, " => "); k >= 0 {
				return prefix + inner[k+len(" => "):] + suffix
			}
		}
	}
	if k := strings.Index(raw, " => "); k >= 0 {
		return strings.TrimSpace(raw[k+len(" => "):])
	}
	return raw
}
