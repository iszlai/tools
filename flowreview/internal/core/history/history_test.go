package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testOptions keeps MaxFilesPerCommit low enough that the fixture's
// mechanical commit (which touches 5 files) is skipped, and Since generous
// enough to cover the fixture's fixed-clock commits.
func testOptions() Options {
	return Options{
		Since:             24 * time.Hour,
		MaxCommits:        100,
		MinSupport:        5,
		MinConfidence:     0.5,
		MaxFilesPerCommit: 4,
	}
}

func TestCompanionsFindsFileNotInDiff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, t.TempDir(), testOptions())
	stats, err := m.Mine(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Commits == 0 {
		t.Fatal("expected commits to be mined")
	}
	if stats.Degraded != "" {
		t.Errorf("unexpected degradation: %q", stats.Degraded)
	}

	// Only a.go is in the diff; a_test.go always changes with it (5/5
	// commits) and must come back as a companion.
	got := m.Companions([]string{"a.go"})
	if len(got) != 1 {
		t.Fatalf("Companions(a.go) = %+v, want exactly one companion", got)
	}
	c := got[0]
	if c.Path != "a_test.go" || c.Of != "a.go" {
		t.Errorf("got Companion{Path:%q, Of:%q}, want {a_test.go, a.go}", c.Path, c.Of)
	}
	if c.Support != 5 {
		t.Errorf("Support = %d, want 5", c.Support)
	}
	if c.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", c.Confidence)
	}
	want := "usually changes with a.go, not in this diff"
	if got := c.Reason(); got != want {
		t.Errorf("Reason() = %q, want %q", got, want)
	}
}

func TestCompanionsExcludesFilesAlreadyInDiff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, t.TempDir(), testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	// Both files already in the diff: nothing new to surface.
	got := m.Companions([]string{"a.go", "a_test.go"})
	if len(got) != 0 {
		t.Errorf("Companions(a.go, a_test.go) = %+v, want none", got)
	}
}

func TestCompanionsThresholdsAreConfigured(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// c.go/c_test.go only co-change twice: below the default-like MinSupport
	// of 5 used by testOptions, so no companion yet.
	strict := New(f.repo, t.TempDir(), testOptions())
	if _, err := strict.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}
	if got := strict.Companions([]string{"c.go"}); len(got) != 0 {
		t.Errorf("Companions(c.go) with MinSupport=5 = %+v, want none", got)
	}

	// Lower the threshold: now it must show up.
	loose := testOptions()
	loose.MinSupport = 2
	loose.MinConfidence = 0.5
	m := New(f.repo, t.TempDir(), loose)
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}
	got := m.Companions([]string{"c.go"})
	if len(got) != 1 || got[0].Path != "c_test.go" {
		t.Fatalf("Companions(c.go) with MinSupport=2 = %+v, want [c_test.go]", got)
	}
}

func TestMechanicalCommitIsSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, t.TempDir(), testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	// The mechanical commit touches a.go an extra time; if it had been
	// counted, a.go would show 6 commits instead of 5 and the companion
	// confidence (support/commits) would drop below 1.0.
	got := m.Companions([]string{"a.go"})
	if len(got) != 1 || got[0].Confidence != 1.0 {
		t.Fatalf("Companions(a.go) = %+v, want confidence 1.0 (mechanical commit must be excluded)", got)
	}
}

func TestHotspotRanksActiveFileHigher(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, t.TempDir(), testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	active := m.Hotspot("a.go")        // 5 commits
	quiet := m.Hotspot("unrelated.go") // 1 commit

	if active.Commits != 5 {
		t.Errorf("a.go Commits = %d, want 5", active.Commits)
	}
	if quiet.Commits != 1 {
		t.Errorf("unrelated.go Commits = %d, want 1", quiet.Commits)
	}
	if active.Score <= quiet.Score {
		t.Errorf("a.go Score (%v) should exceed unrelated.go Score (%v)", active.Score, quiet.Score)
	}
	if active.Score != 1.0 {
		t.Errorf("a.go Score = %v, want 1.0 (it is the most active file in the fixture)", active.Score)
	}
	if quiet.Score != 0.0 {
		t.Errorf("unrelated.go Score = %v, want 0.0 (it is the least active file in the fixture)", quiet.Score)
	}
}

func TestHotspotUnknownFileIsZero(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, t.TempDir(), testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	got := m.Hotspot("never-existed.go")
	if got != (Score{}) {
		t.Errorf("Hotspot(never-existed.go) = %+v, want the zero Score", got)
	}
}

func TestMineDegradesWhenCappedBelowHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	opts := testOptions()
	opts.MaxCommits = 3 // the fixture has 8 commits; this must trigger degradation
	m := New(f.repo, t.TempDir(), opts)
	stats, err := m.Mine(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Commits != 3 {
		t.Errorf("Commits = %d, want 3 (capped)", stats.Commits)
	}
	if stats.Degraded == "" {
		t.Error("expected a Degraded note when MaxCommits caps the mined window")
	}
}

func TestMineCachesAndAvoidsGitOnSecondCall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	first := New(f.repo, cacheDir, testOptions())
	if _, err := first.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	// Exactly one cache file must exist after the first Mine.
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache dir has %d entries, want 1", len(entries))
	}

	// Break the repo so any further git invocation fails: rename .git away.
	gitDir := filepath.Join(f.dir, ".git")
	brokenDir := filepath.Join(f.dir, ".git.broken")
	if err := os.Rename(gitDir, brokenDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(brokenDir, gitDir) })

	// A fresh Miner against the same cacheDir and the same head must still
	// succeed: it has no reason to touch git since the headSHA is passed in
	// directly, and the cache already answers the question.
	second := New(f.repo, cacheDir, testOptions())
	stats, err := second.Mine(ctx, head)
	if err != nil {
		t.Fatalf("Mine with a broken .git should hit cache, got error: %v", err)
	}
	if stats.HeadSHA != head {
		t.Errorf("HeadSHA = %q, want %q", stats.HeadSHA, head)
	}
	got := second.Companions([]string{"a.go"})
	if len(got) != 1 || got[0].Path != "a_test.go" {
		t.Fatalf("Companions after cache hit = %+v, want [a_test.go]", got)
	}
}

func TestMineDifferentHeadMissesCache(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	m := New(f.repo, cacheDir, testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}

	// A different (fabricated) head sha must not match the cached file, so
	// mining runs again rather than silently reusing a stale answer. With a
	// healthy repo this just means a second git invocation, which must still
	// succeed.
	stats, err := m.Mine(ctx, "0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if stats.HeadSHA != "0000000000000000000000000000000000000000" {
		t.Errorf("HeadSHA = %q, want the fabricated sha", stats.HeadSHA)
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cache dir has %d entries, want 2 (one per head)", len(entries))
	}
}

func TestMineRequiresHeadSHA(t *testing.T) {
	f := newFixture(t)
	m := New(f.repo, t.TempDir(), testOptions())
	if _, err := m.Mine(context.Background(), ""); err == nil {
		t.Error("Mine(\"\") should error")
	}
}

func TestMineWithoutCacheDirAlwaysMines(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	head, err := f.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m := New(f.repo, "", testOptions())
	if _, err := m.Mine(ctx, head); err != nil {
		t.Fatal(err)
	}
	got := m.Companions([]string{"a.go"})
	if len(got) != 1 {
		t.Fatalf("Companions(a.go) = %+v, want exactly one companion", got)
	}
}
