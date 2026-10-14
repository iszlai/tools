package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_findsToplevelFromSubdir(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	writeFile(t, f.dir, "sub/dir/.keep", nil)
	repo, err := Open(ctx, filepath.Join(f.dir, "sub", "dir"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if repo.Root() != f.repo.Root() {
		t.Errorf("Root() = %q, want %q", repo.Root(), f.repo.Root())
	}
}

func TestOpen_notARepo(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, t.TempDir()); err == nil {
		t.Fatal("Open on a non-repo directory: want error, got nil")
	}
}

func TestWorktreeDiff_clean(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.WorktreeDiff(context.Background())
	if err != nil {
		t.Fatalf("WorktreeDiff: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("clean worktree: want empty diff, got %q", diff)
	}
}

func TestWorktreeDiff_trackedAndUntracked(t *testing.T) {
	f := newFixture(t)
	f.dirty(t)

	diff, err := f.repo.WorktreeDiff(context.Background())
	if err != nil {
		t.Fatalf("WorktreeDiff: %v", err)
	}
	out := string(diff)

	if !strings.Contains(out, "diff --git a/renamed.txt b/renamed.txt") {
		t.Errorf("missing tracked-file diff header:\n%s", out)
	}
	if !strings.Contains(out, "and now edited in the worktree") {
		t.Errorf("missing tracked-file edit content:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/untracked.txt b/untracked.txt") {
		t.Errorf("missing untracked-file diff header:\n%s", out)
	}
	if !strings.Contains(out, "new file mode") {
		t.Errorf("untracked file not shown as an addition:\n%s", out)
	}
	if !strings.Contains(out, "--- /dev/null") {
		t.Errorf("untracked file diff not synthesized against /dev/null:\n%s", out)
	}
	if !strings.Contains(out, "+not yet added") {
		t.Errorf("missing untracked file content:\n%s", out)
	}

	// Never touch the index: still nothing staged.
	status := runGit(t, f.dir, nil, "status", "--porcelain")
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '?' {
			t.Errorf("index was modified by WorktreeDiff: %q", line)
		}
	}
}

func TestWorktreeDiff_untrackedOnly(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.dir, "only_untracked.txt", []byte("hello\n"))

	diff, err := f.repo.WorktreeDiff(context.Background())
	if err != nil {
		t.Fatalf("WorktreeDiff: %v", err)
	}
	if !strings.Contains(string(diff), "diff --git a/only_untracked.txt b/only_untracked.txt") {
		t.Errorf("missing untracked-only diff:\n%s", diff)
	}
}

func TestRangeDiff_twoDot(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.RangeDiff(context.Background(), f.sha["root"]+".."+f.sha["modify"])
	if err != nil {
		t.Fatalf("RangeDiff: %v", err)
	}
	out := string(diff)
	if !strings.Contains(out, "diff --git a/a.txt b/a.txt") {
		t.Errorf("missing a.txt diff:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/b.txt b/b.txt") {
		t.Errorf("missing b.txt diff:\n%s", out)
	}
}

func TestRangeDiff_threeDotVsTwoDot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// main is at "binary", feature diverged from "modify" with one commit of
	// its own. Three-dot diffs feature against their merge-base (modify), so
	// it shows only feature's own change.
	threeDot, err := f.repo.RangeDiff(ctx, "main...feature")
	if err != nil {
		t.Fatalf("RangeDiff(three-dot): %v", err)
	}
	td := string(threeDot)
	if !strings.Contains(td, "diff --git a/feature.txt b/feature.txt") {
		t.Errorf("three-dot: missing feature.txt:\n%s", td)
	}
	if strings.Contains(td, "bin.dat") || strings.Contains(td, "renamed.txt") {
		t.Errorf("three-dot: should not include main-only changes:\n%s", td)
	}

	// Two-dot diffs the tips directly, so it also carries everything main did
	// that feature never picked up.
	twoDot, err := f.repo.RangeDiff(ctx, "main..feature")
	if err != nil {
		t.Fatalf("RangeDiff(two-dot): %v", err)
	}
	tdt := string(twoDot)
	if !strings.Contains(tdt, "feature.txt") {
		t.Errorf("two-dot: missing feature.txt:\n%s", tdt)
	}
	if !strings.Contains(tdt, "bin.dat") {
		t.Errorf("two-dot: want main-only changes included:\n%s", tdt)
	}
}

func TestCommitDiff_regular(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.CommitDiff(context.Background(), f.sha["modify"])
	if err != nil {
		t.Fatalf("CommitDiff: %v", err)
	}
	out := string(diff)
	if !strings.Contains(out, "diff --git a/a.txt b/a.txt") || !strings.Contains(out, "line TWO") {
		t.Errorf("missing a.txt edit:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/b.txt b/b.txt") || !strings.Contains(out, "new file mode") {
		t.Errorf("missing b.txt addition:\n%s", out)
	}
}

func TestCommitDiff_root(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.CommitDiff(context.Background(), f.sha["root"])
	if err != nil {
		t.Fatalf("CommitDiff(root): %v", err)
	}
	out := string(diff)
	if !strings.Contains(out, "diff --git a/a.txt b/a.txt") {
		t.Errorf("missing a.txt:\n%s", out)
	}
	if !strings.Contains(out, "new file mode") || !strings.Contains(out, "--- /dev/null") {
		t.Errorf("root commit not shown as a pure addition:\n%s", out)
	}
}

func TestCommitDiff_rename(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.CommitDiff(context.Background(), f.sha["rename"])
	if err != nil {
		t.Fatalf("CommitDiff(rename): %v", err)
	}
	out := string(diff)
	if !strings.Contains(out, "rename from b.txt") || !strings.Contains(out, "rename to renamed.txt") {
		t.Errorf("rename not detected:\n%s", out)
	}
	if strings.Contains(out, "diff --git a/renamed.txt b/renamed.txt\nnew file") {
		t.Errorf("rename shown as a fresh add instead of a rename:\n%s", out)
	}
}

func TestCommitDiff_delete(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.CommitDiff(context.Background(), f.sha["delete"])
	if err != nil {
		t.Fatalf("CommitDiff(delete): %v", err)
	}
	out := string(diff)
	if !strings.Contains(out, "diff --git a/a.txt b/a.txt") || !strings.Contains(out, "deleted file mode") {
		t.Errorf("deletion not shown:\n%s", out)
	}
	if !strings.Contains(out, "+++ /dev/null") {
		t.Errorf("deletion not diffed against /dev/null:\n%s", out)
	}
}

func TestCommitDiff_binary(t *testing.T) {
	f := newFixture(t)
	diff, err := f.repo.CommitDiff(context.Background(), f.sha["binary"])
	if err != nil {
		t.Fatalf("CommitDiff(binary): %v", err)
	}
	out := string(diff)
	found := false
	for _, line := range strings.Split(out, "\n") {
		if IsBinaryDiffLine(line) {
			found = true
		}
	}
	if !found {
		t.Errorf("binary marker not found or not recognized:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/bin.dat b/bin.dat") {
		t.Errorf("missing bin.dat header:\n%s", out)
	}
}

func TestIsBinaryDiffLine(t *testing.T) {
	cases := map[string]bool{
		"Binary files a/old.png and b/new.png differ": true,
		"Binary files /dev/null and b/new.png differ": true,
		"diff --git a/old.png b/new.png":              false,
		"":                                            false,
		"Binary files differ":                         false,
	}
	for line, want := range cases {
		if got := IsBinaryDiffLine(line); got != want {
			t.Errorf("IsBinaryDiffLine(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestShow(t *testing.T) {
	f := newFixture(t)
	content, err := f.repo.Show(context.Background(), f.sha["root"], "a.txt")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if string(content) != "line one\nline two\nline three\n" {
		t.Errorf("Show(root, a.txt) = %q", content)
	}
}

func TestShow_missingPath(t *testing.T) {
	f := newFixture(t)
	if _, err := f.repo.Show(context.Background(), f.sha["root"], "does-not-exist.txt"); err == nil {
		t.Fatal("Show on a missing path: want error, got nil")
	}
}

func TestResolveSHAAndHeadSHA(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	got, err := f.repo.ResolveSHA(ctx, "main")
	if err != nil {
		t.Fatalf("ResolveSHA(main): %v", err)
	}
	if got != f.sha["binary"] {
		t.Errorf("ResolveSHA(main) = %s, want %s", got, f.sha["binary"])
	}

	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if head != f.sha["binary"] {
		t.Errorf("HeadSHA = %s, want %s", head, f.sha["binary"])
	}
}

func TestResolveSHA_unknown(t *testing.T) {
	f := newFixture(t)
	if _, err := f.repo.ResolveSHA(context.Background(), "not-a-rev"); err == nil {
		t.Fatal("ResolveSHA on an unknown rev: want error, got nil")
	}
}

func TestMergeBase(t *testing.T) {
	f := newFixture(t)
	got, err := f.repo.MergeBase(context.Background(), "main", "feature")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if got != f.sha["modify"] {
		t.Errorf("MergeBase(main, feature) = %s, want %s", got, f.sha["modify"])
	}
}

func TestRemoteURL(t *testing.T) {
	f := newFixture(t)
	got, err := f.repo.RemoteURL(context.Background(), "origin")
	if err != nil {
		t.Fatalf("RemoteURL: %v", err)
	}
	want := "https://example.com/upvest/flowreview-fixture.git"
	if got != want {
		t.Errorf("RemoteURL(origin) = %s, want %s", got, want)
	}
}

func TestRemoteURL_missing(t *testing.T) {
	f := newFixture(t)
	if _, err := f.repo.RemoteURL(context.Background(), "upstream"); err == nil {
		t.Fatal("RemoteURL on a missing remote: want error, got nil")
	}
}

func TestCurrentBranch(t *testing.T) {
	f := newFixture(t)
	got, err := f.repo.CurrentBranch(context.Background())
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("CurrentBranch = %s, want main", got)
	}
}

func TestCurrentBranch_detached(t *testing.T) {
	f := newFixture(t)
	runGit(t, f.dir, nil, "checkout", "-q", f.sha["root"])
	got, err := f.repo.CurrentBranch(context.Background())
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != "HEAD" {
		t.Errorf("CurrentBranch (detached) = %s, want HEAD", got)
	}
}
