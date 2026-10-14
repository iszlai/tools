package web

import (
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestReloadMarksCommentStale drives the exact scenario FR-9's smoke test
// mimics in the browser: a comment is drafted against a hunk, the fixture
// repo changes under it (so the hunk's content hash changes and the old
// hunk id disappears from the diff), and POST /api/reload is what notices --
// the server does not otherwise re-read the working tree between requests.
func TestReloadMarksCommentStale(t *testing.T) {
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

	var saved model.CommentDraft
	postJSON(t, hs.URL+"/api/comments", commentRequest{
		Body:   "looks fine",
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hid, Position: anchorPos},
	}, &saved)

	// The fixture's working tree lives in the repo opened by newTestServer;
	// dig it out of the session source the way a real caller would have to.
	var sess SessionView
	getJSON(t, hs.URL+"/api/session", &sess)
	dir := sess.Source.RepoPath
	if dir == "" {
		t.Fatal("session has no RepoPath to edit")
	}
	writeFile(t, dir, "a.txt", []byte("line one\nline TWO\nline THREE\n")) // a second, different edit

	var reloaded SessionView
	resp := postJSON(t, hs.URL+"/api/reload", map[string]string{}, &reloaded)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload status = %d, want 200", resp.StatusCode)
	}
	if reloaded.Progress.TotalHunks == 0 {
		t.Fatalf("reloaded progress = %+v, want at least one hunk in the new diff", reloaded.Progress)
	}
	if _, stillThere := reloaded.HunkStates[hid]; stillThere {
		// present is fine only if its status moved off "reviewed" -- the new
		// hunk content means it is a different hunk id in practice, but guard
		// the assumption explicitly rather than relying on hash behavior.
		t.Errorf("HunkStates[%s] = %q, want the old hunk id gone or no longer reviewed", hid, reloaded.HunkStates[hid])
	}

	var afterSess SessionView
	getJSON(t, hs.URL+"/api/session", &afterSess)
	if len(afterSess.Comments) != 1 {
		t.Fatalf("Comments = %+v, want exactly the one drafted", afterSess.Comments)
	}
	if got := afterSess.Comments[0].Status; got != model.CommentStale {
		t.Errorf("comment status = %q, want %q after the hunk it was anchored to changed", got, model.CommentStale)
	}
}
