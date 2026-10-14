package github

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// initRepo creates a bare-minimum git repository with an origin remote and
// no commits — enough for `git remote get-url` and `git rev-parse
// --show-toplevel`, which is all ResolveClone's origin-matching needs. No
// commit is made, so there is nothing for commit.gpgsign to sign.
func initRepo(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks up front (macOS's /var -> /private/var) so the result
	// matches what `git rev-parse --show-toplevel` returns.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, dir, "init", "-q", "-b", "main", ".")
	if originURL != "" {
		runLocalGit(t, dir, "remote", "add", "origin", originURL)
	}
	return dir
}

func runLocalGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// stubFetchAndClone replaces the network-touching vars with fakes that
// record calls and never touch the network, restoring them after the test.
func stubFetchAndClone(t *testing.T) (fetchCalls *[]string, cloneCalls *[]string) {
	t.Helper()
	var fetches, clones []string

	oldFetch := runGitFetch
	runGitFetch = func(ctx context.Context, dir string, args ...string) error {
		fetches = append(fetches, dir)
		return nil
	}
	t.Cleanup(func() { runGitFetch = oldFetch })

	oldClone := cloneRepo
	cloneRepo = func(ctx context.Context, owner, repo, dir string) error {
		clones = append(clones, owner+"/"+repo+" -> "+dir)
		// A real `gh repo clone` would leave a .git directory behind; the
		// cache-exists check in ResolveClone depends on that.
		return os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	}
	t.Cleanup(func() { cloneRepo = oldClone })

	return &fetches, &clones
}

func TestResolveClone_usesMatchingLocalOrigin(t *testing.T) {
	ctx := context.Background()
	fetches, clones := stubFetchAndClone(t)

	repoDir := initRepo(t, "git@github.com:o/r.git")
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 42}
	cacheRoot := t.TempDir()

	got, err := ResolveClone(ctx, ref, repoDir, cacheRoot)
	if err != nil {
		t.Fatalf("ResolveClone: %v", err)
	}
	if got != repoDir {
		t.Errorf("ResolveClone = %q, want the local repo %q", got, repoDir)
	}
	if len(*clones) != 0 {
		t.Errorf("clone calls = %v, want none (origin matched)", *clones)
	}
	if len(*fetches) == 0 {
		t.Error("want at least one fetch into the local repo")
	}
	for _, dir := range *fetches {
		if dir != repoDir {
			t.Errorf("fetched into %q, want the local repo %q", dir, repoDir)
		}
	}
}

func TestResolveClone_mismatchedOriginUsesCache(t *testing.T) {
	ctx := context.Background()
	fetches, clones := stubFetchAndClone(t)

	repoDir := initRepo(t, "git@github.com:other/unrelated.git")
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 42}
	cacheRoot := t.TempDir()
	wantCacheDir := filepath.Join(cacheRoot, "repos", "o", "r")

	got, err := ResolveClone(ctx, ref, repoDir, cacheRoot)
	if err != nil {
		t.Fatalf("ResolveClone: %v", err)
	}
	if got != wantCacheDir {
		t.Errorf("ResolveClone = %q, want cache dir %q", got, wantCacheDir)
	}
	if len(*clones) != 1 {
		t.Fatalf("clone calls = %v, want exactly 1", *clones)
	}
	if len(*fetches) == 0 {
		t.Error("want at least one fetch into the cloned repo")
	}
}

func TestResolveClone_noLocalRepoUsesCache(t *testing.T) {
	ctx := context.Background()
	fetches, clones := stubFetchAndClone(t)

	ref := model.PRRef{Owner: "o", Repo: "r", Number: 42}
	cacheRoot := t.TempDir()
	cwd := t.TempDir() // not a git repo at all

	got, err := ResolveClone(ctx, ref, cwd, cacheRoot)
	if err != nil {
		t.Fatalf("ResolveClone: %v", err)
	}
	want := filepath.Join(cacheRoot, "repos", "o", "r")
	if got != want {
		t.Errorf("ResolveClone = %q, want %q", got, want)
	}
	if len(*clones) != 1 {
		t.Fatalf("clone calls = %v, want exactly 1", *clones)
	}
	if len(*fetches) == 0 {
		t.Error("want at least one fetch after cloning")
	}
}

func TestResolveClone_reusesExistingCacheWithoutRecloning(t *testing.T) {
	ctx := context.Background()
	fetches, clones := stubFetchAndClone(t)

	ref := model.PRRef{Owner: "o", Repo: "r", Number: 42}
	cacheRoot := t.TempDir()
	cwd := t.TempDir()

	cacheDir := filepath.Join(cacheRoot, "repos", "o", "r")
	if err := os.MkdirAll(filepath.Join(cacheDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveClone(ctx, ref, cwd, cacheRoot)
	if err != nil {
		t.Fatalf("ResolveClone: %v", err)
	}
	if got != cacheDir {
		t.Errorf("ResolveClone = %q, want %q", got, cacheDir)
	}
	if len(*clones) != 0 {
		t.Errorf("clone calls = %v, want none (cache already populated)", *clones)
	}
	if len(*fetches) == 0 {
		t.Error("want a fetch even when the cache already exists, to pick up new commits")
	}
}

func TestResolveClone_emptyCwdSkipsLocalOriginCheck(t *testing.T) {
	ctx := context.Background()
	_, clones := stubFetchAndClone(t)

	ref := model.PRRef{Owner: "o", Repo: "r", Number: 42}
	cacheRoot := t.TempDir()

	got, err := ResolveClone(ctx, ref, "", cacheRoot)
	if err != nil {
		t.Fatalf("ResolveClone: %v", err)
	}
	want := filepath.Join(cacheRoot, "repos", "o", "r")
	if got != want {
		t.Errorf("ResolveClone = %q, want %q", got, want)
	}
	if len(*clones) != 1 {
		t.Errorf("clone calls = %v, want exactly 1", *clones)
	}
}
