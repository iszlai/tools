package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// goFixtureSource builds a Go file with one function of 20 body statements
// "v0 := 0" .. "v19 := 19", with v10 set to tenValue: 10 for the fixture's
// committed ("old") revision, 100 for its worktree-edited ("new") one, so
// the two revisions differ by exactly one line in the middle of the
// function's body -- enough surrounding, unchanged body on both sides of a
// default 3-line-context hunk to actually exercise FR-8's expansion, unlike
// newFixtureRepo's one-line a.txt.
func goFixtureSource(tenValue int) string {
	lines := []string{"package sample", "", "func Compute() int {"}
	for i := 0; i < 20; i++ {
		v := i
		if i == 10 {
			v = tenValue
		}
		lines = append(lines, fmt.Sprintf("\tv%d := %d", i, v))
	}
	lines = append(lines, "\treturn v0", "}")
	return strings.Join(lines, "\n") + "\n"
}

// newGoFixtureRepo commits goFixtureSource(10), then edits the worktree to
// goFixtureSource(100): a single-hunk worktree diff whose hunk sits in the
// middle of Compute's 23-line body (lines 3..25, 1-based).
func newGoFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFile(t, dir, "sample.go", []byte(goFixtureSource(10)))
	runGit(t, dir, nil, "add", "sample.go")
	commit(t, dir, clock, "root: add sample.go")

	writeFile(t, dir, "sample.go", []byte(goFixtureSource(100)))
	return dir
}

func newGoTestServer(t *testing.T) testServer {
	t.Helper()
	dir := newGoFixtureRepo(t)
	store := session.New(t.TempDir())
	ws, err := review.Open(context.Background(), store, model.Source{
		Kind:     model.SourceWorktree,
		RepoPath: dir,
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
	return testServer{Server: srv, URL: srv.URL()}
}

func fileIDByPath(t *testing.T, hs testServer, path string) string {
	t.Helper()
	var files []FileView
	getJSON(t, hs.URL+"/api/files", &files)
	for _, f := range files {
		if f.Path == path {
			return f.ID
		}
	}
	t.Fatalf("%q not found in %+v", path, files)
	return ""
}

// TestHandleExpandHunk_AddsSurroundingContext covers the main path: a hunk
// well inside a function gets real, unchanged lines spliced in before and
// after it, and the hunk's own rows -- in particular its "change" row's
// anchors -- are exactly what GET /api/file/{id}/rows already served.
func TestHandleExpandHunk_AddsSurroundingContext(t *testing.T) {
	hs := newGoTestServer(t)
	fileID := fileIDByPath(t, hs, "sample.go")

	var plainRows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fileID+"/rows", &plainRows)

	var hunkID string
	var changeRow render.Row
	for _, r := range plainRows {
		if r.Kind == "hunk" {
			hunkID = r.HunkID
		}
		if r.Kind == "change" {
			changeRow = r
		}
	}
	if hunkID == "" {
		t.Fatalf("fixture produced no hunk: %+v", plainRows)
	}
	if changeRow.Kind == "" {
		t.Fatalf("fixture produced no change row: %+v", plainRows)
	}

	var expanded []render.Row
	resp := getJSON(t, hs.URL+"/api/file/"+fileID+"/expand?hunk="+hunkID, &expanded)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(expanded) <= len(plainRows) {
		t.Fatalf("expanded has %d rows, want more than the %d plain rows", len(expanded), len(plainRows))
	}

	var expandedChange render.Row
	found := false
	for _, r := range expanded {
		if r.Kind == "change" {
			expandedChange = r
			found = true
		}
	}
	if !found {
		t.Fatalf("expanded response has no change row: %+v", expanded)
	}
	if expandedChange != changeRow {
		t.Errorf("expanded change row = %+v, want identical to plain %+v", expandedChange, changeRow)
	}

	var sawBefore, sawAfter bool
	for _, r := range expanded {
		if !r.Expanded {
			continue
		}
		if r.Kind != "context" {
			t.Errorf("expanded row has Expanded=true but Kind=%q, want context", r.Kind)
		}
		if r.OldPosition != 0 || r.NewPosition != 0 {
			t.Errorf("expanded context row has a nonzero position: %+v", r)
		}
		if r.OldNo == 0 || r.NewNo == 0 {
			t.Errorf("expanded context row missing a line number: %+v", r)
		}
		if r.OldNo != 0 && r.OldNo < changeRow.OldNo {
			sawBefore = true
		}
		if r.OldNo != 0 && r.OldNo > changeRow.OldNo {
			sawAfter = true
		}
	}
	if !sawBefore {
		t.Error("expanded response has no context row before the hunk")
	}
	if !sawAfter {
		t.Error("expanded response has no context row after the hunk")
	}

	// The enclosing function spans the whole file body (lines 3..25); expand
	// should reach both ends: the signature line and the closing brace.
	var oldLines []int
	for _, r := range expanded {
		if r.OldNo != 0 {
			oldLines = append(oldLines, r.OldNo)
		}
	}
	min, max := oldLines[0], oldLines[0]
	for _, n := range oldLines {
		if n < min {
			min = n
		}
		if n > max {
			max = n
		}
	}
	if min != 3 {
		t.Errorf("earliest old line in the expansion = %d, want 3 (the func line)", min)
	}
	if max != 25 {
		t.Errorf("latest old line in the expansion = %d, want 25 (the closing brace)", max)
	}
}

// TestHandleExpandHunk_FallbackNoExtraContext covers a file with no
// structural provider (a.txt) whose whole content already fits inside the
// hunk's own default diff context: the +/-15 fallback window still resolves
// (no error), but clips down to exactly the hunk already shown, adding no
// rows -- the honest answer when there is nothing left to expand into.
func TestHandleExpandHunk_FallbackNoExtraContext(t *testing.T) {
	ws, store := newTestWorkspace(t)
	srv := New(store, ws, nil)
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	hs := testServer{Server: srv, URL: srv.URL()}

	fileID := fileIDByPath(t, hs, "a.txt")
	var plainRows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fileID+"/rows", &plainRows)
	var hunkID string
	for _, r := range plainRows {
		if r.Kind == "hunk" {
			hunkID = r.HunkID
		}
	}
	if hunkID == "" {
		t.Fatalf("fixture produced no hunk: %+v", plainRows)
	}

	var expanded []render.Row
	resp := getJSON(t, hs.URL+"/api/file/"+fileID+"/expand?hunk="+hunkID, &expanded)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(expanded) != len(plainRows) {
		t.Errorf("expanded has %d rows, want exactly the %d plain rows (nothing left to expand into)", len(expanded), len(plainRows))
	}
	for _, r := range expanded {
		if r.Expanded {
			t.Errorf("unexpected expanded row: %+v", r)
		}
	}
}

func TestHandleExpandHunk_UnknownHunk(t *testing.T) {
	ws, store := newTestWorkspace(t)
	srv := New(store, ws, nil)
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	hs := testServer{Server: srv, URL: srv.URL()}

	fileID := fileIDByPath(t, hs, "a.txt")
	resp := getJSON(t, hs.URL+"/api/file/"+fileID+"/expand?hunk=does-not-exist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSplitRangeRef(t *testing.T) {
	cases := []struct {
		ref           string
		left, right   string
		wantMergeBase bool
	}{
		{"main..feature", "main", "feature", false},
		{"main...feature", "main", "feature", true},
		{"abc123..def456", "abc123", "def456", false},
	}
	for _, c := range cases {
		left, right, mergeBase := splitRangeRef(c.ref)
		if left != c.left || right != c.right || mergeBase != c.wantMergeBase {
			t.Errorf("splitRangeRef(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.ref, left, right, mergeBase, c.left, c.right, c.wantMergeBase)
		}
	}
}
