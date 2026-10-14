package web

import (
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestCommentAnchorHeadSHAIsStamped checks that the server fills in
// Anchor.HeadSHA when the client leaves it blank -- the client can read
// path/side/line/hunk_id/position straight off a row's data attributes, but
// has no way to know the repo's HEAD the way the server does (headSHA,
// handlers.go), the same helper handleState already uses for ReviewState.
func TestCommentAnchorHeadSHAIsStamped(t *testing.T) {
	_, hs := newTestServer(t)
	fid := fileID(t, hs)
	hid := hunkID(t, hs, fid)

	var rows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fid+"/rows", &rows)
	var anchorPos int
	for _, r := range rows {
		if r.Kind == render.KindChange {
			anchorPos = r.NewPosition
			break
		}
	}

	req := commentRequest{
		Body:   "needs a head sha",
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hid, Position: anchorPos},
	}
	var saved model.CommentDraft
	resp := postJSON(t, hs.URL+"/api/comments", req, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if saved.Anchor == nil || saved.Anchor.HeadSHA == "" {
		t.Fatalf("saved.Anchor = %+v, want a non-empty HeadSHA", saved.Anchor)
	}
}

// TestGeneralCommentAnchorNeverStamped covers the nil-anchor path next to the
// one above: a general, review-level comment has no anchor to stamp a head
// SHA onto, and handleUpsertComment must not panic trying.
func TestGeneralCommentAnchorNeverStamped(t *testing.T) {
	_, hs := newTestServer(t)
	var saved model.CommentDraft
	resp := postJSON(t, hs.URL+"/api/comments", commentRequest{Body: "general"}, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if saved.Anchor != nil {
		t.Errorf("saved.Anchor = %+v, want nil", saved.Anchor)
	}
}
