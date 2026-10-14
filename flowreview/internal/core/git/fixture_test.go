package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture is a small, deterministic repository used by every test in this
// package. Every commit runs with a fixed author/committer identity and a
// fixed, strictly increasing date, so the resulting SHAs never change between
// runs or machines.
type fixture struct {
	repo *Repo
	dir  string
	sha  map[string]string // name -> commit sha
}

// fixtureTime hands out deterministic, strictly increasing timestamps for
// fixture commits — never the real wall clock, so tests stay reproducible.
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
// date, and returns the new commit's sha.
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

// newFixture builds a repo with a linear history on "main" plus a diverging
// "feature" branch, then leaves HEAD back on main with a clean working tree.
// Commits, in order:
//
//	root    add a.txt
//	modify  edit a.txt, add b.txt
//	rename  git mv b.txt renamed.txt
//	delete  git rm a.txt
//	binary  add bin.dat (binary content)
//
// "feature" branches off "modify" and adds one commit of its own, so range
// diffs can show the "A..B" vs "A...B" (merge-base) distinction on real
// divergent history.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")
	runGit(t, dir, nil, "remote", "add", "origin", "https://example.com/upvest/flowreview-fixture.git")

	f := &fixture{dir: dir, sha: map[string]string{}}

	writeFile(t, dir, "a.txt", []byte("line one\nline two\nline three\n"))
	runGit(t, dir, nil, "add", "a.txt")
	f.sha["root"] = commit(t, dir, clock, "root: add a.txt")

	writeFile(t, dir, "a.txt", []byte("line one\nline TWO\nline three\n"))
	writeFile(t, dir, "b.txt", []byte("b, soon to be renamed\n"))
	runGit(t, dir, nil, "add", "a.txt", "b.txt")
	f.sha["modify"] = commit(t, dir, clock, "modify: edit a.txt, add b.txt")

	// Branch "feature" off "modify", before the rest of main's history, to
	// give the range tests a real divergence to tell "A..B" from "A...B".
	runGit(t, dir, nil, "branch", "feature", f.sha["modify"])

	runGit(t, dir, nil, "mv", "b.txt", "renamed.txt")
	f.sha["rename"] = commit(t, dir, clock, "rename: b.txt -> renamed.txt")

	runGit(t, dir, nil, "rm", "-q", "a.txt")
	f.sha["delete"] = commit(t, dir, clock, "delete: remove a.txt")

	writeFile(t, dir, "bin.dat", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 'b', 'i', 'n'})
	runGit(t, dir, nil, "add", "bin.dat")
	f.sha["binary"] = commit(t, dir, clock, "binary: add bin.dat")

	runGit(t, dir, nil, "checkout", "-q", "feature")
	writeFile(t, dir, "feature.txt", []byte("feature-only content\n"))
	runGit(t, dir, nil, "add", "feature.txt")
	f.sha["feature"] = commit(t, dir, clock, "feature: add feature.txt")

	runGit(t, dir, nil, "checkout", "-q", "main")

	ctx := context.Background()
	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	f.repo = repo
	return f
}

// dirty makes the working tree unclean: a tracked edit and a new untracked
// file, for WorktreeDiff tests. It is a separate step so other tests see the
// fixture with a clean working tree.
func (f *fixture) dirty(t *testing.T) {
	t.Helper()
	writeFile(t, f.dir, "renamed.txt", []byte("b, soon to be renamed\nand now edited in the worktree\n"))
	writeFile(t, f.dir, "untracked.txt", []byte("not yet added\n"))
}
