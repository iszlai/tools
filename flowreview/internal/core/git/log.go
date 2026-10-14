package git

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FileChange is one file touched by a commit, as reported by
// `git log --name-status`.
type FileChange struct {
	Status  string // raw git status code: A, M, D, R100, C087, ...
	Path    string // new path (the only path for a non-rename/copy)
	OldPath string // set for renames and copies
}

// Commit is one commit in a Log result.
type Commit struct {
	SHA         string
	Subject     string
	Author      string
	AuthorEmail string
	Time        time.Time
	Files       []FileChange
}

// logRecordSep separates commits in the `git log` output below; logFieldSep
// separates the fields of one commit. Both are control characters that never
// appear in a commit's own text, so subjects and names can contain anything.
const (
	logRecordSep = "\x1e"
	logFieldSep  = "\x1f"
)

// Log lists the commits in rangeExpr (anything `git log` accepts: "A..B", a
// single rev, "--all", ...), each with its name-status file list (renames
// detected via -M).
func (r *Repo) Log(ctx context.Context, rangeExpr string) ([]Commit, error) {
	format := logRecordSep + "%H" + logFieldSep + "%an" + logFieldSep + "%ae" + logFieldSep + "%aI" + logFieldSep + "%s"
	out, err := r.run(ctx, "log", "--name-status", "-M", "--no-color", "--pretty=format:"+format, rangeExpr)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out))
}

func parseLog(out string) ([]Commit, error) {
	var commits []Commit
	for _, rec := range strings.Split(out, logRecordSep) {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		lines := strings.Split(rec, "\n")
		fields := strings.Split(lines[0], logFieldSep)
		if len(fields) != 5 {
			return nil, fmt.Errorf("git: malformed log record: %q", lines[0])
		}
		t, err := time.Parse(time.RFC3339, fields[3])
		if err != nil {
			return nil, fmt.Errorf("git: malformed commit date %q: %w", fields[3], err)
		}
		c := Commit{
			SHA:         fields[0],
			Author:      fields[1],
			AuthorEmail: fields[2],
			Time:        t,
			Subject:     fields[4],
		}
		for _, l := range lines[1:] {
			if l == "" {
				continue
			}
			parts := strings.Split(l, "\t")
			switch len(parts) {
			case 2:
				c.Files = append(c.Files, FileChange{Status: parts[0], Path: parts[1]})
			case 3:
				c.Files = append(c.Files, FileChange{Status: parts[0], OldPath: parts[1], Path: parts[2]})
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}
