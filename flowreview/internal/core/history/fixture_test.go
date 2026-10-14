package history

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/git"
)

// fixture is a small, deterministic repository used by every test in this
// package. Every commit runs with a fixed author/committer identity and a
// fixed, strictly increasing date, so the resulting history (and any sha
// derived from it) never changes between runs or machines. Approach copied
// from internal/core/git/fixture_test.go.
type fixture struct {
	repo *git.Repo
	dir  string
}

type fixtureTime struct{ t time.Time }

func (f *fixtureTime) next() string {
	f.t = f.t.Add(time.Hour)
	return f.t.Format(time.RFC3339)
}

func runFixtureGit(t *testing.T, dir string, env []string, args ...string) string {
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

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFiles(t *testing.T, dir string, clock *fixtureTime, msg string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	runFixtureGit(t, dir, nil, "add", "-A")
	when := clock.next()
	env := []string{
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=" + when,
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=" + when,
	}
	// -c commit.gpgsign=false overrides any global signing config for this
	// one invocation only (never writes config); fixture commits don't need
	// a real signature and signing is slow enough to dominate test time.
	runFixtureGit(t, dir, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
}

// newFixture builds a repo whose history has a clear co-change signal:
//
//   - a.go and a_test.go change together in every one of 5 commits (the
//     commits function mines): support 5, confidence 1.0 for a.go -> a_test.go.
//   - c.go and c_test.go change together in only 2 commits: support 2, below
//     the default MinSupport (5) but visible once a test lowers it.
//   - unrelated.go changes once, alone, so it never pairs with anything and
//     has a low hotspot score.
//   - a mechanical commit touches every tracked file at once, well past
//     MaxFilesPerCommit in the fixture's test options, so it must not count
//     toward anyone's co-change or commit totals.
//
// All commits land within the last few hours (fixed clock), comfortably
// inside any Since window a test uses.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Now().Add(-6 * time.Hour)}

	runFixtureGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runFixtureGit(t, dir, nil, "config", "user.name", "Fixture")
	runFixtureGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	for i := 1; i <= 5; i++ {
		commitFiles(t, dir, clock, "a: iteration", map[string]string{
			"a.go":      fmt.Sprintf("package a\n\nfunc A() int { return %d }\n", i),
			"a_test.go": fmt.Sprintf("package a\n\nfunc TestA(t *testing.T) { _ = %d }\n", i),
		})
	}

	for i := 1; i <= 2; i++ {
		commitFiles(t, dir, clock, "c: iteration", map[string]string{
			"c.go":      fmt.Sprintf("package c\n\nfunc C() int { return %d }\n", i),
			"c_test.go": fmt.Sprintf("package c\n\nfunc TestC(t *testing.T) { _ = %d }\n", i),
		})
	}

	commitFiles(t, dir, clock, "unrelated: one-off change", map[string]string{
		"unrelated.go": "package unrelated\n\nfunc U() {}\n",
	})

	// A mechanical commit: touches every file already in the tree at once, as
	// a repo-wide reformat would. MaxFilesPerCommit in test Options is set
	// below the file count here, so mining must skip it entirely.
	commitFiles(t, dir, clock, "mechanical: reformat everything", map[string]string{
		"a.go":         "package a\n\nfunc A() int { return 0 }\n",
		"a_test.go":    "package a\n\nfunc TestA(t *testing.T) { _ = 0 }\n",
		"c.go":         "package c\n\nfunc C() int { return 0 }\n",
		"c_test.go":    "package c\n\nfunc TestC(t *testing.T) { _ = 0 }\n",
		"unrelated.go": "package unrelated\n\nfunc U() { _ = 0 }\n",
	})

	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("git.Open(%q): %v", dir, err)
	}
	return &fixture{repo: repo, dir: dir}
}
