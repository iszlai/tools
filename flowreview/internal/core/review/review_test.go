package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/host"
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

// withPRHooks installs fake FetchPR/FetchRemoteComments hooks for the
// duration of the test, restoring the previous (nil) ones after -- these
// package vars are global, so tests touching them cannot run in parallel with
// each other.
func withPRHooks(t *testing.T, fetchPR func(context.Context, model.Source) (model.Source, []byte, error), fetchRemote func(context.Context, model.Source) ([]host.RemoteComment, error)) {
	t.Helper()
	oldPR, oldRemote := FetchPR, FetchRemoteComments
	FetchPR, FetchRemoteComments = fetchPR, fetchRemote
	t.Cleanup(func() { FetchPR, FetchRemoteComments = oldPR, oldRemote })
}

func TestOpenPRResolvesSourceFromHook(t *testing.T) {
	repo := newFixtureRepo(t)
	repo.dirty(t)
	raw := runGit(t, repo.dir, nil, "diff", "HEAD")
	store := newStore(t)

	withPRHooks(t, func(ctx context.Context, src model.Source) (model.Source, []byte, error) {
		if src.PR == nil || src.PR.Owner != "o" || src.PR.Repo != "r" || src.PR.Number != 1 {
			t.Fatalf("hook got src.PR = %+v, want o/r#1", src.PR)
		}
		resolved := src
		title := "Fixture PR title"
		resolved.PR = &model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1, Title: title}
		resolved.RepoPath = repo.dir
		resolved.BaseSHA = "base-sha"
		resolved.HeadSHA = "head-sha"
		return resolved, []byte(raw), nil
	}, func(ctx context.Context, src model.Source) ([]host.RemoteComment, error) {
		return []host.RemoteComment{{ID: 1, Author: "reviewer", Body: "looks fine"}}, nil
	})

	ws, err := Open(ctx(t), store, model.Source{
		Kind: model.SourcePR,
		PR:   &model.PRRef{Owner: "o", Repo: "r", Number: 1},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ws.Session.Title != "Fixture PR title" {
		t.Errorf("Title = %q, want the PR title from the hook", ws.Session.Title)
	}
	if ws.Session.Source.RepoPath != repo.dir || ws.Session.Source.BaseSHA != "base-sha" || ws.Session.Source.HeadSHA != "head-sha" {
		t.Errorf("Source = %+v, want the hook's resolved RepoPath/BaseSHA/HeadSHA", ws.Session.Source)
	}
	if len(ws.Files) != 1 || ws.Files[0].Path() != "a.txt" {
		t.Fatalf("Files = %+v, want one changed file a.txt", ws.Files)
	}
	remote := ws.RemoteComments()
	if len(remote) != 1 || remote[0].Body != "looks fine" {
		t.Errorf("RemoteComments = %+v, want the hook's one comment", remote)
	}
}

func TestOpenPRIgnoresRemoteCommentsError(t *testing.T) {
	repo := newFixtureRepo(t)
	raw := runGit(t, repo.dir, nil, "diff", "HEAD")
	store := newStore(t)

	withPRHooks(t, func(ctx context.Context, src model.Source) (model.Source, []byte, error) {
		resolved := src
		resolved.RepoPath = repo.dir
		return resolved, []byte(raw), nil
	}, func(ctx context.Context, src model.Source) ([]host.RemoteComment, error) {
		return nil, errors.New("host unreachable")
	})

	ws, err := Open(ctx(t), store, model.Source{
		Kind: model.SourcePR,
		PR:   &model.PRRef{Owner: "o", Repo: "r", Number: 1},
	})
	if err != nil {
		t.Fatalf("Open: want no error when only the supplementary comments fetch fails, got %v", err)
	}
	if remote := ws.RemoteComments(); remote != nil {
		t.Errorf("RemoteComments = %+v, want nil after a failed fetch", remote)
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
