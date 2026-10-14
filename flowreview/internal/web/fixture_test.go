package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/model"
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
// date, with signing forced off (the user's global config may sign commits
// by default, which is slow and can block on a passphrase prompt in a test).
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

// newFixtureRepo builds a repo on "main" with one commit, then dirties the
// working tree with a second edit, so a worktree review always has a diff to
// show without every test needing to construct its own.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFile(t, dir, "a.txt", []byte("line one\nline two\nline three\n"))
	runGit(t, dir, nil, "add", "a.txt")
	commit(t, dir, clock, "root: add a.txt")

	writeFile(t, dir, "a.txt", []byte("line one\nline TWO\nline three\n"))
	return dir
}

// newTestWorkspace opens a worktree review of a freshly built fixture repo,
// backed by a session store under t.TempDir(), for handler tests that need a
// real Workspace rather than a hand-built one.
func newTestWorkspace(t *testing.T) (*review.Workspace, *session.Store) {
	t.Helper()
	dir := newFixtureRepo(t)
	store := session.New(t.TempDir())
	ws, err := review.Open(context.Background(), store, model.Source{
		Kind:     model.SourceWorktree,
		RepoPath: dir,
	})
	if err != nil {
		t.Fatalf("review.Open: %v", err)
	}
	return ws, store
}
