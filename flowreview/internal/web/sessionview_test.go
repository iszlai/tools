package web

import (
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestSessionViewIncludesHunkStatesAndComments covers the two additions FR-7
// made to GET /api/session's response: a hunk's review status by id (so the
// diff pane can dim it without a second request) and the session's comment
// drafts (so the summary panel can show a count).
func TestSessionViewIncludesHunkStatesAndComments(t *testing.T) {
	_, hs := newTestServer(t)
	fid := fileID(t, hs)
	hid := hunkID(t, hs, fid)

	postJSON(t, hs.URL+"/api/state", stateRequest{HunkID: hid, Reviewed: true}, nil)
	postJSON(t, hs.URL+"/api/comments", commentRequest{Body: "general note"}, nil)

	var sess SessionView
	resp := getJSON(t, hs.URL+"/api/session", &sess)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := sess.HunkStates[hid]; got != model.HunkReviewed {
		t.Errorf("HunkStates[%s] = %q, want %q", hid, got, model.HunkReviewed)
	}
	if len(sess.Comments) != 1 || sess.Comments[0].Body != "general note" {
		t.Errorf("Comments = %+v, want one comment with body %q", sess.Comments, "general note")
	}
}
