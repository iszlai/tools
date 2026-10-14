package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureTime hands out deterministic, strictly increasing timestamps for
// fixture commits, so SHAs never change between runs.
type fixtureTime struct{ t time.Time }

func (f *fixtureTime) next() string {
	f.t = f.t.Add(time.Minute)
	return f.t.Format(time.RFC3339)
}

func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit runs `git commit` with a fixed, deterministic author/committer and
// date, and with signing off (the user's global config may sign commits by
// default, which is slow and can block on a passphrase prompt in a test).
func commit(t *testing.T, dir string, clock *fixtureTime, msg string) string {
	t.Helper()
	when := clock.next()
	env := []string{
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=" + when,
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=" + when,
	}
	runGit(t, dir, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
	return strings.TrimSpace(runGit(t, dir, nil, "rev-parse", "HEAD"))
}

// fixtureRepo is a small repo with one commit and, optionally, a second
// commit and/or an uncommitted change on top, so tests can exercise
// worktree, commit and range sources against the same shape of history.
type fixtureRepo struct {
	dir   string
	root  string
	sha   map[string]string
	clock *fixtureTime
}

// newFixtureRepo builds a repo on "main" with one commit (root: add a.txt)
// and leaves the working tree clean.
func newFixtureRepo(t *testing.T) *fixtureRepo {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	f := &fixtureRepo{dir: dir, sha: map[string]string{}, clock: clock}
	writeFile(t, dir, "a.txt", []byte("line one\nline two\nline three\n"))
	runGit(t, dir, nil, "add", "a.txt")
	f.sha["root"] = commit(t, dir, clock, "root: add a.txt")

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.root = root
	return f
}

// dirty edits the tracked file so WorktreeDiff has something to show.
func (f *fixtureRepo) dirty(t *testing.T) {
	t.Helper()
	writeFile(t, f.dir, "a.txt", []byte("line one\nline TWO\nline three\n"))
}

// addCommit commits a second change on top of root, returning its sha, so
// range/commit sources have two commits to diff between.
func (f *fixtureRepo) addCommit(t *testing.T, name, content, msg string) string {
	t.Helper()
	writeFile(t, f.dir, name, []byte(content))
	runGit(t, f.dir, nil, "add", name)
	sha := commit(t, f.dir, f.clock, msg)
	f.sha[msg] = sha
	return sha
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}
