package review

import (
	"context"
	"errors"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// withPublishHooks installs fake SubmitReview/PublishComment hooks for the
// duration of the test, mirroring withPRHooks in review_test.go. These are
// global vars too, so tests touching them cannot run in parallel.
func withPublishHooks(t *testing.T,
	submit func(context.Context, model.PRRef, host.Review) (*host.ReviewResult, error),
	publish func(context.Context, model.PRRef, string, model.CommentDraft) (*host.CommentResult, error),
) {
	t.Helper()
	oldSubmit, oldPublish := SubmitReview, PublishComment
	SubmitReview, PublishComment = submit, publish
	t.Cleanup(func() { SubmitReview, PublishComment = oldSubmit, oldPublish })
}

// openPRFixtureForPublish opens a two-file PR session (a.txt and b.txt, each
// with one worktree-edited hunk) via a FetchPR hook that re-reads the
// worktree diff fresh on every call, the way a real host would after a push:
// mutating repo.dir between calls and calling Reload/Preflight again picks up
// the change.
func openPRFixtureForPublish(t *testing.T) (ws *Workspace, repo *fixtureRepo, store interface {
	Get(repoKey, id string) (*model.ReviewSession, error)
}) {
	t.Helper()
	repo = newFixtureRepo(t)
	writeFile(t, repo.dir, "b.txt", []byte("line A\nline B\nline C\n"))
	runGit(t, repo.dir, nil, "add", "b.txt")
	commit(t, repo.dir, repo.clock, "add b.txt")

	writeFile(t, repo.dir, "a.txt", []byte("line one\nline TWO\nline three\n"))
	writeFile(t, repo.dir, "b.txt", []byte("line A\nline BEE\nline C\n"))

	st := newStore(t)
	withPRHooks(t, func(ctx context.Context, src model.Source) (model.Source, []byte, error) {
		resolved := src
		resolved.RepoPath = repo.dir
		resolved.HeadSHA = "head-sha-fixed"
		resolved.PR = &model.PRRef{Owner: "o", Repo: "r", Number: 1}
		raw := runGit(t, repo.dir, nil, "diff", "HEAD")
		return resolved, []byte(raw), nil
	}, func(ctx context.Context, src model.Source) ([]host.RemoteComment, error) {
		return nil, nil
	})

	var err error
	ws, err = Open(ctx(t), st, model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "o", Repo: "r", Number: 1}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ws, repo, st
}

func hunkIDFor(t *testing.T, ws *Workspace, path string) string {
	t.Helper()
	_, files, _ := ws.Snapshot()
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		if len(f.Hunks) == 0 {
			t.Fatalf("file %s has no hunks", path)
		}
		return f.Hunks[0].ID
	}
	t.Fatalf("file %s not found in %+v", path, files)
	return ""
}

func addDraft(t *testing.T, ws *Workspace, c model.CommentDraft) model.CommentDraft {
	t.Helper()
	sess, _, _ := ws.Snapshot()
	saved, err := ws.store.UpsertComment(sess.RepoKey, sess.ID, c)
	if err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if err := ws.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return saved
}

func TestPreflight_nonPRSessionErrors(t *testing.T) {
	repo := newFixtureRepo(t)
	store := newStore(t)
	ws, err := Open(ctx(t), store, model.Source{Kind: model.SourceWorktree, RepoPath: repo.dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := ws.Preflight(ctx(t), nil); !errors.Is(err, ErrNotAPRSession) {
		t.Errorf("Preflight error = %v, want ErrNotAPRSession", err)
	}
}

func TestPreflight_classifiesInlineGeneralAndStale(t *testing.T) {
	ws, repo, _ := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")
	bHunk := hunkIDFor(t, ws, "b.txt")

	inline := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:        "inline note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})
	willGoStale := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "b.txt", Side: model.SideNew, Line: 2, HunkID: bHunk},
		Body:        "about to go stale",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})
	general := addDraft(t, ws, model.CommentDraft{
		Body:        "general note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})

	// Change b.txt's hunk content so its hunk id no longer appears in the
	// next fetch -- a.txt is untouched, so only the b.txt draft should go
	// stale.
	writeFile(t, repo.dir, "b.txt", []byte("line A\nline BEE PRIME\nline C\n"))

	plan, err := ws.Preflight(ctx(t), nil)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if plan.HeadSHA != "head-sha-fixed" {
		t.Errorf("HeadSHA = %q, want the hook's resolved head", plan.HeadSHA)
	}
	if len(plan.Inline) != 1 || plan.Inline[0].ID != inline.ID {
		t.Errorf("Inline = %+v, want exactly the a.txt draft", plan.Inline)
	}
	if len(plan.General) != 1 || plan.General[0].ID != general.ID {
		t.Errorf("General = %+v, want exactly the general draft", plan.General)
	}
	if len(plan.Excluded) != 1 || plan.Excluded[0].DraftID != willGoStale.ID || plan.Excluded[0].Reason == "" {
		t.Errorf("Excluded = %+v, want exactly the b.txt draft with a reason", plan.Excluded)
	}
}

func TestPreflight_draftIDsScopesClassification(t *testing.T) {
	ws, _, _ := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")

	inline := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:        "inline note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})
	addDraft(t, ws, model.CommentDraft{Body: "general note", Status: model.CommentDraftStatus, Publishable: true})

	plan, err := ws.Preflight(ctx(t), []string{inline.ID})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(plan.Inline) != 1 || plan.Inline[0].ID != inline.ID {
		t.Errorf("Inline = %+v, want only the requested draft", plan.Inline)
	}
	if len(plan.General) != 0 {
		t.Errorf("General = %+v, want none (not in draftIDs)", plan.General)
	}
}

func TestPublish_submitsReviewAndRecordsResults(t *testing.T) {
	ws, _, store := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")

	inline := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:        "inline note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})
	general := addDraft(t, ws, model.CommentDraft{Body: "general note", Status: model.CommentDraftStatus, Publishable: true})

	var gotReview host.Review
	withPublishHooks(t, func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
		gotReview = r
		if ref.Owner != "o" || ref.Repo != "r" || ref.Number != 1 {
			t.Errorf("ref = %+v", ref)
		}
		return &host.ReviewResult{
			ID: 1, HTMLURL: "https://example/review/1",
			Comments: []host.CommentResult{{DraftID: inline.ID, OK: true, RemoteID: 42, HTMLURL: "https://example/comment/42"}},
		}, nil
	}, nil)

	res, err := ws.Publish(ctx(t), host.EventComment, "top level body", nil)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Review == nil || res.Review.ID != 1 {
		t.Errorf("Review = %+v", res.Review)
	}
	if gotReview.CommitID != "head-sha-fixed" {
		t.Errorf("CommitID = %q, want the preflight head", gotReview.CommitID)
	}
	if gotReview.Body != "top level body\n\n---\n\ngeneral note" {
		t.Errorf("Body = %q, want the typed body plus the general comment, separated", gotReview.Body)
	}
	if len(gotReview.Comments) != 1 || gotReview.Comments[0].ID != inline.ID {
		t.Errorf("Comments sent = %+v, want just the inline draft", gotReview.Comments)
	}

	if len(res.Results) != 2 {
		t.Fatalf("Results = %+v, want 2 (inline + general)", res.Results)
	}

	sess, err := store.Get(ws.Session.RepoKey, ws.Session.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	byID := map[string]model.CommentDraft{}
	for _, c := range sess.Comments {
		byID[c.ID] = c
	}
	if got := byID[inline.ID]; got.Status != model.CommentPublished || got.RemoteID != 42 {
		t.Errorf("inline after publish = %+v, want published with RemoteID 42", got)
	}
	if got := byID[general.ID]; got.Status != model.CommentPublished || got.RemoteURL != "https://example/review/1" {
		t.Errorf("general after publish = %+v, want published with the review's URL", got)
	}
}

func TestPublish_totalFailureMarksDraftsFailed(t *testing.T) {
	ws, _, store := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")
	inline := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:        "inline note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})

	withPublishHooks(t, func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
		return nil, errors.New("network down")
	}, nil)

	res, err := ws.Publish(ctx(t), host.EventComment, "", nil)
	if err != nil {
		t.Fatalf("Publish: want no error (failures are reported per-draft), got %v", err)
	}
	if res.Review != nil {
		t.Errorf("Review = %+v, want nil on total failure", res.Review)
	}
	if len(res.Results) != 1 || res.Results[0].OK || res.Results[0].Error == "" {
		t.Fatalf("Results = %+v, want one failed result with an error message", res.Results)
	}

	sess, err := store.Get(ws.Session.RepoKey, ws.Session.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	for _, c := range sess.Comments {
		if c.ID == inline.ID && (c.Status != model.CommentFailed || c.Error == "") {
			t.Errorf("draft after failed publish = %+v, want status failed with an Error", c)
		}
	}
}

func TestPublish_retryUsesPublishCommentNotSubmitReview(t *testing.T) {
	ws, _, store := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")
	failed := addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:        "retry me",
		Status:      model.CommentFailed,
		Error:       "previous failure",
		Publishable: true,
	})

	submitCalled := false
	var publishedCommitID string
	withPublishHooks(t, func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
		submitCalled = true
		return nil, errors.New("should not be called")
	}, func(ctx context.Context, ref model.PRRef, commitID string, c model.CommentDraft) (*host.CommentResult, error) {
		publishedCommitID = commitID
		return &host.CommentResult{DraftID: c.ID, OK: true, RemoteID: 7, HTMLURL: "https://example/comment/7"}, nil
	})

	res, err := ws.Publish(ctx(t), host.EventComment, "", []string{failed.ID})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if submitCalled {
		t.Error("SubmitReview was called for a pure retry, want only PublishComment")
	}
	if publishedCommitID != "head-sha-fixed" {
		t.Errorf("commitID given to PublishComment = %q", publishedCommitID)
	}
	if res.Review != nil {
		t.Errorf("Review = %+v, want nil for a retry (no review object is created)", res.Review)
	}
	if len(res.Results) != 1 || !res.Results[0].OK || res.Results[0].RemoteID != 7 {
		t.Fatalf("Results = %+v, want one successful result with the remote id", res.Results)
	}

	sess, err := store.Get(ws.Session.RepoKey, ws.Session.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	for _, c := range sess.Comments {
		if c.ID == failed.ID && (c.Status != model.CommentPublished || c.RemoteID != 7) {
			t.Errorf("draft after retry = %+v, want published with RemoteID 7", c)
		}
	}
}

func TestPublish_mixedFailedAndDraftGoesThroughSubmitReview(t *testing.T) {
	ws, _, _ := openPRFixtureForPublish(t)
	aHunk := hunkIDFor(t, ws, "a.txt")
	bHunk := hunkIDFor(t, ws, "b.txt")
	addDraft(t, ws, model.CommentDraft{
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: aHunk},
		Body:   "already failed once", Status: model.CommentFailed, Publishable: true,
	})
	addDraft(t, ws, model.CommentDraft{
		Anchor: &model.Anchor{Path: "b.txt", Side: model.SideNew, Line: 2, HunkID: bHunk},
		Body:   "brand new", Status: model.CommentDraftStatus, Publishable: true,
	})

	submitCalled, publishCalled := false, false
	withPublishHooks(t, func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
		submitCalled = true
		results := make([]host.CommentResult, len(r.Comments))
		for i, c := range r.Comments {
			results[i] = host.CommentResult{DraftID: c.ID, OK: true}
		}
		return &host.ReviewResult{ID: 1, Comments: results}, nil
	}, func(ctx context.Context, ref model.PRRef, commitID string, c model.CommentDraft) (*host.CommentResult, error) {
		publishCalled = true
		return &host.CommentResult{DraftID: c.ID, OK: true}, nil
	})

	if _, err := ws.Publish(ctx(t), host.EventComment, "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !submitCalled {
		t.Error("SubmitReview was not called for a mix of failed and fresh drafts")
	}
	if publishCalled {
		t.Error("PublishComment was called, want the mix to go through SubmitReview only")
	}
}

// TestPublish_pendingReviewBlocksWithoutSending covers a user who has an
// unsubmitted review on the PR (e.g. started with "Start a review" on
// github.com): GitHub would attach the new comment to that invisible review
// and still answer 422, so Publish must refuse up front and send nothing.
func TestPublish_pendingReviewBlocksWithoutSending(t *testing.T) {
	ws, _, _ := openPRFixtureForPublish(t)
	addDraft(t, ws, model.CommentDraft{
		Anchor:      &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hunkIDFor(t, ws, "a.txt")},
		Body:        "inline note",
		Status:      model.CommentDraftStatus,
		Publishable: true,
	})

	sent := false
	withPublishHooks(t, func(context.Context, model.PRRef, host.Review) (*host.ReviewResult, error) {
		sent = true
		return &host.ReviewResult{}, nil
	}, func(context.Context, model.PRRef, string, model.CommentDraft) (*host.CommentResult, error) {
		sent = true
		return &host.CommentResult{}, nil
	})
	old := PendingReview
	PendingReview = func(context.Context, model.PRRef) (string, bool, error) {
		return "https://example/pull/1#pullrequestreview-9", true, nil
	}
	t.Cleanup(func() { PendingReview = old })

	_, err := ws.Publish(ctx(t), host.EventComment, "", nil)
	if !errors.Is(err, ErrPendingReview) {
		t.Fatalf("Publish error = %v, want ErrPendingReview", err)
	}
	if sent {
		t.Error("Publish sent something to the host despite the pending review")
	}
}
