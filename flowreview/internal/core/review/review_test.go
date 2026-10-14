package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func newStore(t *testing.T) *session.Store {
	t.Helper()
	return session.New(t.TempDir())
}

func TestOpenWorktree(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.dirty(t)
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ws.Session.ID != "worktree" {
		t.Errorf("session id = %q, want %q", ws.Session.ID, "worktree")
	}
	if ws.Session.Source.RepoPath != repo.root {
		t.Errorf("session RepoPath = %q, want %q (normalized to toplevel)", ws.Session.Source.RepoPath, repo.root)
	}
	if len(ws.Files) != 1 || ws.Files[0].Path() != "a.txt" {
		t.Fatalf("Files = %+v, want one changed file a.txt", ws.Files)
	}
	if ws.Repo == nil {
		t.Error("Repo is nil, want a repo handle")
	}

	// Reopening the same source resumes the same session instead of making a
	// new one.
	ws2, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if ws2.Session.ID != ws.Session.ID {
		t.Errorf("second open got a different session: %q vs %q", ws2.Session.ID, ws.Session.ID)
	}
}

func TestOpenCommit(t *testing.T) {
	repo := newFixtureRepo(t)
	sha := repo.addCommit(t, "b.txt", "new file\n", "add b.txt")
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceCommit, Ref: sha, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(ws.Files) != 1 || ws.Files[0].Path() != "b.txt" {
		t.Fatalf("Files = %+v, want one changed file b.txt", ws.Files)
	}
	wantID := session.NewID(model.Source{Kind: model.SourceCommit, Ref: sha})
	if ws.Session.ID != wantID {
		t.Errorf("session id = %q, want %q", ws.Session.ID, wantID)
	}
}

func TestOpenRange(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.addCommit(t, "b.txt", "new file\n", "add b.txt")
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceRange, Ref: "main~1..main", RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(ws.Files) != 1 || ws.Files[0].Path() != "b.txt" {
		t.Fatalf("Files = %+v, want one changed file b.txt", ws.Files)
	}
}

func TestOpenPatch(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.dirty(t)
	raw := runGit(t, repo.dir, nil, "diff", "HEAD")

	patchPath := filepath.Join(t.TempDir(), "change.patch")
	if err := os.WriteFile(patchPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourcePatch, PatchPath: patchPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(ws.Files) != 1 || ws.Files[0].Path() != "a.txt" {
		t.Fatalf("Files = %+v, want one changed file a.txt", ws.Files)
	}
	if !filepath.IsAbs(ws.Session.Source.PatchPath) {
		t.Errorf("PatchPath = %q, want an absolute path", ws.Session.Source.PatchPath)
	}

	// Reopening the same patch path resumes the session.
	ws2, err := Open(ctx(t), store, model.Source{Kind: model.SourcePatch, PatchPath: patchPath})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if ws2.Session.ID != ws.Session.ID {
		t.Errorf("second open got a different session: %q vs %q", ws2.Session.ID, ws.Session.ID)
	}
}

func TestOpenPRWithoutHook(t *testing.T) {
	store := newStore(t)
	_, err := Open(ctx(t), store, model.Source{
		Kind: model.SourcePR,
		PR:   &model.PRRef{Owner: "o", Repo: "r", Number: 1},
	})
	if err == nil {
		t.Fatal("Open: want an error with no FetchPR hook installed")
	}
}

func TestRefreshMarksReviewedStateAcrossReopen(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.dirty(t)
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	hunk := ws.Files[0].Hunks[0]
	if err := store.MarkReviewed(ws.Session.RepoKey, ws.Session.ID, hunk, ws.Files[0].Path(), "deadbeef"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	// Reopen with the worktree unchanged: the hunk id is stable, so the
	// reviewed mark survives the Refresh that runs on every Open.
	ws2, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	st, ok := ws2.Session.States[hunk.ID]
	if !ok || st.Status != model.HunkReviewed {
		t.Fatalf("hunk state = %+v, want HunkReviewed", st)
	}

	// Now change the file again: Refresh should flag the old hunk id as
	// needing a second look rather than silently dropping it.
	writeFile(t, repo.dir, "a.txt", []byte("line one\nline TWO\nline THREE\n"))
	ws3, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("third Open: %v", err)
	}
	st3, ok := ws3.Session.States[hunk.ID]
	if !ok || st3.Status != model.HunkNeedsReReview {
		t.Fatalf("hunk state after further edit = %+v, want HunkNeedsReReview", st3)
	}
}

func TestWorkspaceSyncAndSnapshot(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.dirty(t)
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	hunk := ws.Files[0].Hunks[0]
	if err := store.MarkReviewed(ws.Session.RepoKey, ws.Session.ID, hunk, ws.Files[0].Path(), "deadbeef"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	sess, _, _ := ws.Snapshot()
	if _, ok := sess.States[hunk.ID]; ok {
		t.Fatal("Snapshot before Sync already shows the mutation; the Store write should not be visible yet")
	}

	if err := ws.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	sess, _, _ = ws.Snapshot()
	if st, ok := sess.States[hunk.ID]; !ok || st.Status != model.HunkReviewed {
		t.Fatalf("after Sync, hunk state = %+v, want HunkReviewed", st)
	}
}

func TestWorkspaceReload(t *testing.T) {
	repo := newFixtureRepo(t)
	store := newStore(t)

	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(ws.Files) != 0 {
		t.Fatalf("Files = %+v, want none before the worktree is dirtied", ws.Files)
	}

	repo.dirty(t)
	if err := ws.Reload(ctx(t)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	_, files, _ := ws.Snapshot()
	if len(files) != 1 || files[0].Path() != "a.txt" {
		t.Fatalf("Files after Reload = %+v, want one changed file a.txt", files)
	}
}
