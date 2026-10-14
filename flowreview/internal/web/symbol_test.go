package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/core/symbol"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// symbolOldPkgGo/symbolNewPkgGo are FR-18's fixture package: a worktree edit
// changes both Helper's body (line 13) and Caller's call site (line 18), so
// the diff's one hunk covers exactly line 18 -- Caller's call to Helper --
// without reaching into cmd/app/main.go at all. Line numbers are load-
// bearing; keep them in sync with any edit.
const symbolOldPkgGo = `package pkg

type Greeter interface {
	Greet() string
}

type English struct{}

func (English) Greet() string {
	return "hello"
}

func Helper() int {
	return 1
}

func Caller() int {
	return Helper() + 1
}
`

const symbolNewPkgGo = `package pkg

type Greeter interface {
	Greet() string
}

type English struct{}

func (English) Greet() string {
	return "hello"
}

func Helper() int {
	return 2
}

func Caller() int {
	return Helper() + 2
}
`

// symbolMainGo is cmd/app/main.go, a reverse importer of pkg left identical
// across both revisions -- always "outside the diff" for FR-18's own such
// test case, while still being part of golang.Loader's loaded scope (it
// imports pkg, see loader.go's own "plus every package inside the same
// module that imports them").
const symbolMainGo = `package main

import "webfixture.example/app/pkg"

func main() {
	pkg.Caller()
}
`

func newSymbolFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFile(t, dir, "go.mod", []byte("module webfixture.example/app\n\ngo 1.25\n"))
	writeFile(t, dir, "pkg/pkg.go", []byte(symbolOldPkgGo))
	writeFile(t, dir, "cmd/app/main.go", []byte(symbolMainGo))
	// A non-Go pair, committed here (not part of the dirty edit below) so
	// neither is ever part of the diff file list -- FR-18 must still answer
	// GET /api/symbol for them (the tree-sitter heuristic tier) and
	// GET /api/blob must be able to read them as "outside the diff" content.
	writeFile(t, dir, "scripts/util.js", []byte(symbolUtilJS))
	writeFile(t, dir, "scripts/main.js", []byte(symbolMainJS))
	runGit(t, dir, nil, "add", "-A")
	commit(t, dir, clock, "root: add pkg, cmd/app and scripts")

	writeFile(t, dir, "pkg/pkg.go", []byte(symbolNewPkgGo))
	return dir
}

const symbolUtilJS = `function helper() {
  return 1;
}

module.exports = { helper };
`

const symbolMainJS = `const { helper } = require('./util');

function run() {
  return helper() + 1;
}
`

func newSymbolTestServer(t *testing.T) testServer {
	t.Helper()
	dir := newSymbolFixtureRepo(t)
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

// pollSymbol polls GET /api/symbol until it stops reporting {"pending":
// true}, the same backoff shape TestHandleTests_PendingThenReport already
// uses for GET /api/tests.
func pollSymbol(t *testing.T, url string) symbolResponse {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var resp symbolResponse
		status := getJSON(t, url, &resp)
		if status.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d", url, status.StatusCode)
		}
		if !resp.Pending {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s stayed pending for 30s", url)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHandleSymbol_GoCalleesAndCallers(t *testing.T) {
	hs := newSymbolTestServer(t)

	// Caller is declared at line 17; its one callee, Helper, at line 13.
	resp := pollSymbol(t, hs.URL+"/api/symbol?path=pkg/pkg.go&side=new&line=17")
	if resp.Report == nil || resp.Report.Symbol.Name != "Caller" {
		t.Fatalf("report = %+v, want Caller", resp.Report)
	}
	if len(resp.Report.Callees) != 1 || resp.Report.Callees[0].Name != "Helper" || resp.Report.Callees[0].Path != "pkg/pkg.go" || resp.Report.Callees[0].Line != 13 {
		t.Errorf("Callees = %+v, want Helper at pkg/pkg.go:13", resp.Report.Callees)
	}

	// Helper's one caller, Caller, calls it from line 18 -- the diff's only
	// changed line, so this should come back in_diff: true.
	resp = pollSymbol(t, hs.URL+"/api/symbol?path=pkg/pkg.go&side=new&line=13")
	if resp.Report == nil || resp.Report.Symbol.Name != "Helper" {
		t.Fatalf("report = %+v, want Helper", resp.Report)
	}
	if len(resp.Report.Callers) != 1 {
		t.Fatalf("Callers = %+v, want exactly one (Caller)", resp.Report.Callers)
	}
	c := resp.Report.Callers[0]
	if c.Name != "Caller" || c.Path != "pkg/pkg.go" || c.Line != 18 || !c.InDiff {
		t.Errorf("Callers[0] = %+v, want Caller at pkg/pkg.go:18, in_diff", c)
	}
}

// TestHandleSymbol_TargetOutsideDiff resolves Caller's own caller: main(),
// in cmd/app/main.go -- a file the diff never touches at all.
func TestHandleSymbol_TargetOutsideDiff(t *testing.T) {
	hs := newSymbolTestServer(t)

	resp := pollSymbol(t, hs.URL+"/api/symbol?path=pkg/pkg.go&side=new&line=17")
	if resp.Report == nil {
		t.Fatal("report is nil, want Caller")
	}
	if len(resp.Report.Callers) != 1 {
		t.Fatalf("Callers = %+v, want exactly one (main)", resp.Report.Callers)
	}
	main := resp.Report.Callers[0]
	if main.Name != "main" || main.Path != "cmd/app/main.go" {
		t.Errorf("Callers[0] = %+v, want main in cmd/app/main.go", main)
	}
	if main.InDiff {
		t.Error("Callers[0].InDiff = true, want false: cmd/app/main.go is outside the diff")
	}
}

func TestHandleSymbol_Implementations(t *testing.T) {
	hs := newSymbolTestServer(t)

	// English.Greet is declared at line 9.
	resp := pollSymbol(t, hs.URL+"/api/symbol?path=pkg/pkg.go&side=new&line=9")
	if resp.Report == nil || resp.Report.Symbol.Name != "Greet" {
		t.Fatalf("report = %+v, want Greet", resp.Report)
	}
	if len(resp.Report.Implementations) != 1 || resp.Report.Implementations[0].Name != "Greeter.Greet" {
		t.Errorf("Implementations = %+v, want Greeter.Greet", resp.Report.Implementations)
	}
}

// TestHandleSymbol_NonGoFile exercises the tree-sitter heuristic tier: run()
// in scripts/main.js calls helper() in scripts/util.js, a different file,
// neither of them ever part of this diff.
func TestHandleSymbol_NonGoFile(t *testing.T) {
	hs := newSymbolTestServer(t)

	var resp symbolResponse
	status := getJSON(t, hs.URL+"/api/symbol?path=scripts/main.js&side=new&line=3", &resp)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", status.StatusCode)
	}
	if resp.Pending {
		t.Fatal("the structural tier must never report pending")
	}
	if resp.Report == nil || resp.Report.Symbol.Name != "run" {
		t.Fatalf("report = %+v, want run", resp.Report)
	}
	if resp.Report.Tier != "structural" {
		t.Errorf("Tier = %q, want structural", resp.Report.Tier)
	}
	if len(resp.Report.Callees) != 1 || resp.Report.Callees[0].Name != "helper" || resp.Report.Callees[0].Path != "scripts/util.js" {
		t.Errorf("Callees = %+v, want helper in scripts/util.js", resp.Report.Callees)
	}
}

func TestHandleFileSymbols(t *testing.T) {
	hs := newSymbolTestServer(t)
	id := fileIDByPath(t, hs, "pkg/pkg.go")

	var view fileSymbolsView
	status := getJSON(t, hs.URL+"/api/file/"+id+"/symbols", &view)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", status.StatusCode)
	}
	wantLines := []int{9, 13, 17} // Greet, Helper, Caller
	if !sameInts(view.Old, wantLines) {
		t.Errorf("Old = %v, want %v", view.Old, wantLines)
	}
	if !sameInts(view.New, wantLines) {
		t.Errorf("New = %v, want %v", view.New, wantLines)
	}
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestHandleBlob_OutsideDiffTarget renders cmd/app/main.go -- a file the
// diff never touches -- as FR-18's read-only view, and checks its one
// declaration line is reported so the view's own nav button would work.
func TestHandleBlob_OutsideDiffTarget(t *testing.T) {
	hs := newSymbolTestServer(t)

	var view blobView
	status := getJSON(t, hs.URL+"/api/blob?path=cmd/app/main.go&rev=new", &view)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", status.StatusCode)
	}
	if view.Path != "cmd/app/main.go" {
		t.Errorf("Path = %q, want cmd/app/main.go", view.Path)
	}
	if view.Language != "go" {
		t.Errorf("Language = %q, want go", view.Language)
	}
	if len(view.Rows) != 7 {
		t.Fatalf("Rows has %d entries, want 7 (one per source line)", len(view.Rows))
	}
	if view.Rows[0].NewNo != 1 || view.Rows[6].NewNo != 7 {
		t.Errorf("Rows[0].NewNo/Rows[6].NewNo = %d/%d, want 1/7", view.Rows[0].NewNo, view.Rows[6].NewNo)
	}
	if !sameInts(view.DeclLines, []int{5}) {
		t.Errorf("DeclLines = %v, want [5] (main)", view.DeclLines)
	}
}

func TestHandleBlob_RejectsOldRev(t *testing.T) {
	hs := newSymbolTestServer(t)
	resp := getJSON(t, hs.URL+"/api/blob?path=cmd/app/main.go&rev=old", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unsupported rev", resp.StatusCode)
	}
}

// symbolResponseIsReport is a tiny sanity check that symbol.Report round-
// trips through JSON the way the client expects (non-nil slices, not
// "null", for Callers/Callees/Implementations -- app.js iterates them
// without a null check).
func TestSymbolResponse_EncodesEmptySlicesNotNull(t *testing.T) {
	rep := symbol.Report{Symbol: model.CodeSymbol{Name: "X"}, Callers: []symbol.Target{}, Callees: []symbol.Target{}, Implementations: []symbol.Target{}}
	b, err := json.Marshal(symbolResponse{Report: &rep})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal(raw["report"], &report); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"callers", "callees", "implementations"} {
		if string(report[field]) == "null" {
			t.Errorf("%s encoded as null, want []", field)
		}
	}
}
