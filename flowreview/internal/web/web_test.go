package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// testServer bundles the pieces web_test's helpers need: the Server itself
// (to call SetWindow, etc.) and the base URL its real Listen/Serve are
// actually answering on.
type testServer struct {
	*Server
	URL string
}

func newTestServer(t *testing.T) (*Server, testServer) {
	t.Helper()
	ws, store := newTestWorkspace(t)
	srv := New(store, ws, nil)

	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })

	return srv, testServer{Server: srv, URL: srv.URL()}
}

func getJSON(t *testing.T, url string, out any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("GET %s: decode: %v", url, err)
		}
	}
	return resp
}

func postJSON(t *testing.T, url string, body any, out any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("POST %s: decode: %v", url, err)
		}
	}
	return resp
}

func doDelete(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHealth(t *testing.T) {
	_, hs := newTestServer(t)
	var out map[string]bool
	resp := getJSON(t, hs.URL+"/api/health", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !out["ok"] {
		t.Errorf("body = %v, want ok:true", out)
	}
}

func TestSession(t *testing.T) {
	_, hs := newTestServer(t)
	var sess SessionView
	resp := getJSON(t, hs.URL+"/api/session", &sess)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if sess.ID != "worktree" {
		t.Errorf("ID = %q, want worktree", sess.ID)
	}
	if sess.Progress.TotalFiles != 1 || sess.Progress.TotalHunks != 1 {
		t.Errorf("progress = %+v, want 1 file, 1 hunk", sess.Progress)
	}
	if sess.Progress.ReviewedHunks != 0 {
		t.Errorf("ReviewedHunks = %d, want 0 before anything is marked", sess.Progress.ReviewedHunks)
	}
}

func TestFiles(t *testing.T) {
	_, hs := newTestServer(t)
	var files []FileView
	resp := getJSON(t, hs.URL+"/api/files", &files)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want exactly one", files)
	}
	f := files[0]
	if f.Path != "a.txt" || f.Status != model.FileModified {
		t.Errorf("file = %+v, want a.txt/modified", f)
	}
	if f.Hunks != 1 || f.Reviewed != 0 {
		t.Errorf("file hunks/reviewed = %d/%d, want 1/0", f.Hunks, f.Reviewed)
	}
}

func fileID(t *testing.T, hs testServer) string {
	t.Helper()
	var files []FileView
	getJSON(t, hs.URL+"/api/files", &files)
	if len(files) == 0 {
		t.Fatal("no files in fixture")
	}
	return files[0].ID
}

func TestFileRows(t *testing.T) {
	_, hs := newTestServer(t)
	id := fileID(t, hs)

	var rows []render.Row
	resp := getJSON(t, hs.URL+"/api/file/"+id+"/rows", &rows)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(rows) == 0 {
		t.Fatal("rows is empty")
	}
	if rows[0].Kind != render.KindHunk {
		t.Errorf("rows[0].Kind = %q, want hunk", rows[0].Kind)
	}
	var sawChange bool
	for _, r := range rows[1:] {
		if r.Kind != render.KindChange {
			continue
		}
		sawChange = true
		if r.OldPosition == 0 || r.NewPosition == 0 {
			t.Errorf("change row missing a position: %+v", r)
		}
		if r.OldHTML == "" || r.NewHTML == "" {
			t.Errorf("change row missing highlighted html: %+v", r)
		}
	}
	if !sawChange {
		t.Errorf("rows = %+v, want a paired change row (the fixture's one-word edit)", rows)
	}
}

// TestFileRowsUnifiedLayout checks the ?layout=unified fallback kept for
// anything that still wants flowreview's pre-FR-6 flat row shape.
func TestFileRowsUnifiedLayout(t *testing.T) {
	_, hs := newTestServer(t)
	id := fileID(t, hs)

	var rows []unifiedRow
	resp := getJSON(t, hs.URL+"/api/file/"+id+"/rows?layout=unified", &rows)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(rows) == 0 || rows[0].Kind != "header" {
		t.Errorf("rows = %+v, want a leading header row", rows)
	}
	var sawAdd, sawDel bool
	for _, r := range rows[1:] {
		switch r.Kind {
		case "add":
			sawAdd = true
			if r.Position == 0 {
				t.Errorf("add row has no position: %+v", r)
			}
		case "del":
			sawDel = true
		}
	}
	if !sawAdd || !sawDel {
		t.Errorf("rows = %+v, want at least one add and one del", rows)
	}
}

func TestFileRowsNotFound(t *testing.T) {
	_, hs := newTestServer(t)
	resp := getJSON(t, hs.URL+"/api/file/does-not-exist/rows", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] == "" {
		t.Error("expected an error message in the body")
	}
}

func hunkID(t *testing.T, hs testServer, fid string) string {
	t.Helper()
	var rows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fid+"/rows", &rows)
	for _, r := range rows {
		if r.HunkID != "" {
			return r.HunkID
		}
	}
	t.Fatal("no hunk id found in rows")
	return ""
}

func TestMarkAndUnmarkReviewed(t *testing.T) {
	_, hs := newTestServer(t)
	fid := fileID(t, hs)
	hid := hunkID(t, hs, fid)

	var sess SessionView
	resp := postJSON(t, hs.URL+"/api/state", stateRequest{HunkID: hid, Reviewed: true}, &sess)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if sess.Progress.ReviewedHunks != 1 {
		t.Errorf("ReviewedHunks = %d, want 1 after marking", sess.Progress.ReviewedHunks)
	}
	if sess.Progress.ReviewedFiles != 1 {
		t.Errorf("ReviewedFiles = %d, want 1 (its only hunk is reviewed)", sess.Progress.ReviewedFiles)
	}

	resp = postJSON(t, hs.URL+"/api/state", stateRequest{HunkID: hid, Reviewed: false}, &sess)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if sess.Progress.ReviewedHunks != 0 {
		t.Errorf("ReviewedHunks = %d, want 0 after unmarking", sess.Progress.ReviewedHunks)
	}
}

func TestMarkReviewedUnknownHunk(t *testing.T) {
	_, hs := newTestServer(t)
	resp := postJSON(t, hs.URL+"/api/state", stateRequest{HunkID: "nope", Reviewed: true}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStateMissingHunkID(t *testing.T) {
	_, hs := newTestServer(t)
	resp := postJSON(t, hs.URL+"/api/state", stateRequest{Reviewed: true}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCommentLifecycle(t *testing.T) {
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
		Body:   "looks fine",
		Anchor: &model.Anchor{Path: "a.txt", Side: model.SideNew, Line: 2, HunkID: hid, Position: anchorPos},
	}
	var saved model.CommentDraft
	resp := postJSON(t, hs.URL+"/api/comments", req, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", resp.StatusCode)
	}
	if saved.ID == "" || saved.Status != model.CommentDraftStatus || !saved.Publishable {
		t.Fatalf("saved comment = %+v, want an id, draft status, publishable", saved)
	}

	// Edit it.
	req.ID = saved.ID
	req.Body = "edited"
	var edited model.CommentDraft
	resp = postJSON(t, hs.URL+"/api/comments", req, &edited)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", resp.StatusCode)
	}
	if edited.Body != "edited" || edited.ID != saved.ID {
		t.Fatalf("edited comment = %+v, want body=edited same id", edited)
	}

	var sess SessionView
	getJSON(t, hs.URL+"/api/session", &sess)

	resp = doDelete(t, hs.URL+"/api/comments/"+saved.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	}
}

func TestCommentRequiresBody(t *testing.T) {
	_, hs := newTestServer(t)
	resp := postJSON(t, hs.URL+"/api/comments", commentRequest{Body: "  "}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEditUnknownCommentIsNotFound(t *testing.T) {
	_, hs := newTestServer(t)
	resp := postJSON(t, hs.URL+"/api/comments", commentRequest{ID: "c-missing", Body: "x"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGeneralCommentIsPublishable(t *testing.T) {
	_, hs := newTestServer(t)
	var saved model.CommentDraft
	resp := postJSON(t, hs.URL+"/api/comments", commentRequest{Body: "general note"}, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if saved.Anchor != nil || !saved.Publishable {
		t.Errorf("saved = %+v, want nil anchor and publishable", saved)
	}
}

// fakeWindow records Focus/Navigate calls so handleOpen's injected callback
// can be verified without a real native window.
type fakeWindow struct {
	focused   bool
	navigated string
}

func (f *fakeWindow) Focus()            { f.focused = true }
func (f *fakeWindow) Navigate(u string) { f.navigated = u }

func TestOpenSwapsWorkspaceAndFocusesWindow(t *testing.T) {
	srv, hs := newTestServer(t)
	win := &fakeWindow{}
	srv.SetWindow(win)

	otherDir := newFixtureRepo(t)
	resp := postJSON(t, hs.URL+"/api/open", openRequest{Source: model.Source{
		Kind:     model.SourceWorktree,
		RepoPath: otherDir,
	}}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if !win.focused {
		t.Error("window was not focused")
	}
	if win.navigated == "" {
		t.Error("window was not navigated")
	}

	var sess SessionView
	getJSON(t, hs.URL+"/api/session", &sess)
	if sess.Source.RepoPath == "" {
		t.Fatal("session after open has no RepoPath")
	}
}

func TestIndexIsServed(t *testing.T) {
	_, hs := newTestServer(t)
	resp, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		t.Error("no Content-Type for index.html")
	}
}
