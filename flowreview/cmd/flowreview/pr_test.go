package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGitPR(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepoWithOrigin is a bare-minimum repo (no commits needed) with an
// origin remote, just enough for resolvePRRef's RemoteURL lookup.
func initRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	runGitPR(t, dir, "init", "-q", "-b", "main", ".")
	if originURL != "" {
		runGitPR(t, dir, "remote", "add", "origin", originURL)
	}
	return dir
}

func TestResolvePRRef_fullURL(t *testing.T) {
	ref, err := resolvePRRef(context.Background(), "https://github.com/cli/cli/pull/123", "")
	if err != nil {
		t.Fatalf("resolvePRRef: %v", err)
	}
	if ref.Owner != "cli" || ref.Repo != "cli" || ref.Number != 123 {
		t.Errorf("ref = %+v, want cli/cli#123", ref)
	}
}

func TestResolvePRRef_shorthand(t *testing.T) {
	ref, err := resolvePRRef(context.Background(), "o/r#7", "")
	if err != nil {
		t.Fatalf("resolvePRRef: %v", err)
	}
	if ref.Owner != "o" || ref.Repo != "r" || ref.Number != 7 {
		t.Errorf("ref = %+v, want o/r#7", ref)
	}
}

func TestResolvePRRef_bareNumberUsesCwdOrigin(t *testing.T) {
	dir := initRepoWithOrigin(t, "git@github.com:o/r.git")

	ref, err := resolvePRRef(context.Background(), "42", dir)
	if err != nil {
		t.Fatalf("resolvePRRef: %v", err)
	}
	if ref.Owner != "o" || ref.Repo != "r" || ref.Number != 42 {
		t.Errorf("ref = %+v, want o/r#42 resolved from origin", ref)
	}
}

func TestResolvePRRef_bareNumberNotARepoIsNotFoundError(t *testing.T) {
	notARepo := t.TempDir()
	_, err := resolvePRRef(context.Background(), "42", notARepo)
	if err == nil {
		t.Fatal("resolvePRRef: want an error when cwd is not a git repository")
	}
	if exitCode(err) != 3 {
		t.Errorf("exitCode = %d, want 3 (not a git repo / source not found)", exitCode(err))
	}
}

func TestResolvePRRef_bareNumberNoOriginIsNotFoundError(t *testing.T) {
	dir := initRepoWithOrigin(t, "") // repo with no "origin" remote at all
	_, err := resolvePRRef(context.Background(), "42", dir)
	if err == nil {
		t.Fatal("resolvePRRef: want an error when the repo has no origin remote")
	}
	if exitCode(err) != 3 {
		t.Errorf("exitCode = %d, want 3", exitCode(err))
	}
}

func TestResolvePRRef_garbageIsUsageError(t *testing.T) {
	dir := initRepoWithOrigin(t, "git@github.com:o/r.git")
	_, err := resolvePRRef(context.Background(), "not-a-pr-reference-at-all!!", dir)
	if err == nil {
		t.Fatal("resolvePRRef: want an error for unparseable input")
	}
	if exitCode(err) != 2 {
		t.Errorf("exitCode = %d, want 2 (usage error)", exitCode(err))
	}
}
