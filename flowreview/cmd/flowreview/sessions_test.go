package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func runGitSessions(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newSessionsFixture builds a repo with one commit, then dirties the working
// tree, so a worktree session always has one hunk to count.
func newSessionsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitSessions(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGitSessions(t, dir, nil, "config", "user.name", "Fixture")
	runGitSessions(t, dir, nil, "config", "user.email", "fixture@example.com")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSessions(t, dir, nil, "add", "a.txt")
	env := []string{
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z",
	}
	runGitSessions(t, dir, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "root: add a.txt")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line one\nline TWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLocalDiff_worktree(t *testing.T) {
	dir := newSessionsFixture(t)
	files, ok := localDiff(model.Source{Kind: model.SourceWorktree, RepoPath: dir})
	if !ok {
		t.Fatal("localDiff: ok = false, want true for a clean local worktree diff")
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("files = %+v, want one file with one hunk", files)
	}
}

func TestLocalDiff_missingRepoIsNotOK(t *testing.T) {
	_, ok := localDiff(model.Source{Kind: model.SourceWorktree, RepoPath: t.TempDir()})
	if ok {
		t.Fatal("localDiff: ok = true for a directory that is not a git repository, want false")
	}
}

func TestLocalDiff_prWithoutClonedRepoIsNotOK(t *testing.T) {
	_, ok := localDiff(model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "o", Repo: "r", Number: 1}})
	if ok {
		t.Fatal("localDiff: ok = true for a PR session with no local clone recorded, want false")
	}
}

func TestSourceSummary(t *testing.T) {
	cases := []struct {
		src  model.Source
		want string
	}{
		{model.Source{Kind: model.SourceWorktree}, "worktree"},
		{model.Source{Kind: model.SourceRange, Ref: "main..feature"}, "range main..feature"},
		{model.Source{Kind: model.SourceCommit, Ref: "abc123"}, "commit abc123"},
		{model.Source{Kind: model.SourcePatch, PatchPath: "/tmp/x/change.diff"}, "patch change.diff"},
		{model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "o", Repo: "r", Number: 9}}, "pr o/r#9"},
	}
	for _, tc := range cases {
		if got := sourceSummary(tc.src); got != tc.want {
			t.Errorf("sourceSummary(%+v) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestBuildSessionListView(t *testing.T) {
	dir := newSessionsFixture(t)
	sess := &model.ReviewSession{
		ID:        "worktree",
		Title:     "Worktree",
		Source:    model.Source{Kind: model.SourceWorktree, RepoPath: dir},
		UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		States: map[string]*model.ReviewState{
			"h1": {HunkID: "h1", Status: model.HunkReviewed},
		},
	}
	view := buildSessionListView(sess)
	if view.ReviewedHunks != 1 {
		t.Errorf("ReviewedHunks = %d, want 1", view.ReviewedHunks)
	}
	if view.TotalHunks == nil || *view.TotalHunks != 1 {
		t.Errorf("TotalHunks = %v, want a known total of 1 (cheap local diff)", view.TotalHunks)
	}
}

func TestRunSessionsCLI_listsCurrentRepoSessions(t *testing.T) {
	dir := newSessionsFixture(t)
	t.Setenv("FLOWREVIEW_HOME", t.TempDir())

	store := session.New(session.DefaultRoot())
	ws, err := review.Open(context.Background(), store, model.Source{Kind: model.SourceWorktree, RepoPath: dir})
	if err != nil {
		t.Fatalf("review.Open: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldwd) })

	var stdout, stderr bytes.Buffer
	code := runSessionsCLI([]string{"--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSessionsCLI exit = %d, stderr = %s", code, stderr.String())
	}

	var views []sessionListView
	if err := json.Unmarshal(stdout.Bytes(), &views); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, stdout.String())
	}
	if len(views) != 1 || views[0].ID != ws.Session.ID {
		t.Fatalf("views = %+v, want exactly the worktree session %q", views, ws.Session.ID)
	}
}

func TestRunSessionsCLI_unknownFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runSessionsCLI([]string{"--bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown argument") {
		t.Errorf("stderr = %q, want a message about the unknown argument", stderr.String())
	}
}
