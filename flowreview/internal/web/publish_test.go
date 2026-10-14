package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// changeRowPosition returns the GitHub diff position of the fixture's one
// changed line (a.txt line 2, new side) -- a publishable anchor needs a
// non-zero Position, same as handleUpsertComment requires (see
// comment_anchor_test.go).
func changeRowPosition(t *testing.T, hs testServer, fid string) int {
	t.Helper()
	var rows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fid+"/rows", &rows)
	for _, r := range rows {
		if r.Kind == render.KindChange {
			return r.NewPosition
		}
	}
	t.Fatal("no change row found")
	return 0
}

// withPublishHooks installs fake review.FetchPR/SubmitReview/PublishComment
// hooks for the duration of the test, restoring the previous (nil) ones
// after. These are global package vars, so tests touching them cannot run in
// parallel with each other.
func withPublishHooks(t *testing.T,
	fetchPR func(context.Context, model.Source) (model.Source, []byte, error),
	submit func(context.Context, model.PRRef, host.Review) (*host.ReviewResult, error),
	publish func(context.Context, model.PRRef, string, model.CommentDraft) (*host.CommentResult, error),
) {
	t.Helper()
	oldFetch, oldSubmit, oldPublish := review.FetchPR, review.SubmitReview, review.PublishComment
	review.FetchPR, review.SubmitReview, review.PublishComment = fetchPR, submit, publish
	t.Cleanup(func() { review.FetchPR, review.SubmitReview, review.PublishComment = oldFetch, oldSubmit, oldPublish })
}

// newTestPRServer opens a PR session over the same single-file fixture repo
// newTestWorkspace uses (a.txt, one hunk), via a fake FetchPR hook that
// re-reads the worktree diff fresh on every call -- so Preflight's re-fetch
// (Reload) sees whatever the test has done to the repo since Open.
func newTestPRServer(t *testing.T) (*Server, testServer, string) {
	t.Helper()
	dir := newFixtureRepo(t)
	store := session.New(t.TempDir())

	withPublishHooks(t, func(ctx context.Context, src model.Source) (model.Source, []byte, error) {
		resolved := src
		resolved.RepoPath = dir
		resolved.HeadSHA = "head-sha-fixed"
		resolved.PR = &model.PRRef{Owner: "o", Repo: "r", Number: 1}
		raw := runGit(t, dir, nil, "diff", "HEAD")
		return resolved, []byte(raw), nil
	}, nil, nil)

	ws, err := review.Open(context.Background(), store, model.Source{
		Kind: model.SourcePR,
		PR:   &model.PRRef{Owner: "o", Repo: "r", Number: 1},
	})
	if err != nil {
		t.Fatalf("review.Open: %v", err)
	}
	srv := New(store, ws, nil)
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return srv, testServer{Server: srv, URL: srv.URL()}, dir
}

func TestHandlePublish_nonPRSessionIs400(t *testing.T) {
	_, hs := newTestServer(t) // the worktree-session helper from web_test.go
	var body map[string]string
	resp := postJSON(t, hs.URL+"/api/publish", publishRequest{Event: "COMMENT"}, &body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if body["error"] == "" {
		t.Fatal("expected an error message")
	}
}

func TestHandlePublishPreflight_nonPRSessionIs400(t *testing.T) {
	_, hs := newTestServer(t)
	resp := postJSON(t, hs.URL+"/api/publish/preflight", publishRequest{}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlePublish_invalidEventIs400(t *testing.T) {
	_, hs, _ := newTestPRServer(t)
	resp := postJSON(t, hs.URL+"/api/publish", publishRequest{Event: "BOGUS"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlePublishPreflight_classifiesDrafts(t *testing.T) {
	_, hs, _ := newTestPRServer(t)
	fid := fileID(t, hs)
	hid := hunkID(t, hs, fid)
	pos := changeRowPosition(t, hs, fid)

	var saved model.CommentDraft
	postJSON(t, hs.URL+"/api/comments", commentRequest{
		Body:   "inline note",
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hid, Position: pos},
	}, &saved)
	postJSON(t, hs.URL+"/api/comments", commentRequest{Body: "general note"}, nil)

	var plan review.PublishPlan
	resp := postJSON(t, hs.URL+"/api/publish/preflight", publishRequest{}, &plan)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(plan.Inline) != 1 || plan.Inline[0].ID != saved.ID {
		t.Errorf("Inline = %+v, want the one anchored draft", plan.Inline)
	}
	if len(plan.General) != 1 {
		t.Errorf("General = %+v, want the one unanchored draft", plan.General)
	}
	if len(plan.Excluded) != 0 {
		t.Errorf("Excluded = %+v, want none (nothing went stale)", plan.Excluded)
	}
}

func TestHandlePublish_successMarksDraftsPublished(t *testing.T) {
	_, hs, _ := newTestPRServer(t)
	fid := fileID(t, hs)
	hid := hunkID(t, hs, fid)
	pos := changeRowPosition(t, hs, fid)

	var saved model.CommentDraft
	postJSON(t, hs.URL+"/api/comments", commentRequest{
		Body:   "inline note",
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hid, Position: pos},
	}, &saved)

	withPublishHooks(t, review.FetchPR, func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
		results := make([]host.CommentResult, len(r.Comments))
		for i, c := range r.Comments {
			results[i] = host.CommentResult{DraftID: c.ID, OK: true, RemoteID: 99, HTMLURL: "https://example/comment/99"}
		}
		return &host.ReviewResult{ID: 1, HTMLURL: "https://example/review/1", Comments: results}, nil
	}, nil)

	var result review.PublishResult
	resp := postJSON(t, hs.URL+"/api/publish", publishRequest{Event: "COMMENT", Body: "lgtm"}, &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if result.Review == nil || result.Review.ID != 1 {
		t.Errorf("Review = %+v", result.Review)
	}
	if len(result.Results) != 1 || !result.Results[0].OK {
		t.Fatalf("Results = %+v, want one successful result", result.Results)
	}

	var sess SessionView
	getJSON(t, hs.URL+"/api/session", &sess)
	if len(sess.Comments) != 1 || sess.Comments[0].Status != model.CommentPublished || sess.Comments[0].RemoteID != 99 {
		t.Errorf("session comments = %+v, want the draft marked published with RemoteID 99", sess.Comments)
	}
}
