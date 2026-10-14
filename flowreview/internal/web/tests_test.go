package web

import (
	"context"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/core/testreach"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// newGoFixtureRepo builds a one-package Go module on "main" (Add, called by
// TestAdd), then dirties Add's body uncommitted, so a worktree review's diff
// has exactly one changed function for handleTests to find.
func newTestReachFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clock := &fixtureTime{t: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}

	runGit(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGit(t, dir, nil, "config", "user.name", "Fixture")
	runGit(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFile(t, dir, "go.mod", []byte("module webfixture.example/app\n\ngo 1.25\n"))
	writeFile(t, dir, "pkg/pkg.go", []byte("package pkg\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"))
	writeFile(t, dir, "pkg/pkg_test.go", []byte(
		"package pkg\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
	))
	runGit(t, dir, nil, "add", "-A")
	commit(t, dir, clock, "root: add pkg")

	writeFile(t, dir, "pkg/pkg.go", []byte("package pkg\n\nfunc Add(a, b int) int {\n\treturn a + b + 0\n}\n"))
	return dir
}

func newGoTestWorkspace(t *testing.T) (*review.Workspace, *session.Store) {
	t.Helper()
	dir := newTestReachFixtureRepo(t)
	store := session.New(t.TempDir())
	ws, err := review.Open(context.Background(), store, model.Source{
		Kind:     model.SourceWorktree,
		RepoPath: dir,
	})
	if err != nil {
		t.Fatalf("review.Open: %v", err)
	}
	return ws, store
}

// testsResponse decodes either of handleTests' two JSON shapes: {"pending":
// true} while computeTests is running, or a full testreach.Report once it is
// done -- the latter never sets "pending", so Pending is reliably false for
// it.
type testsResponse struct {
	Pending   bool                       `json:"pending"`
	Functions []testreach.FunctionReach  `json:"functions"`
	Unreached []model.CodeSymbol         `json:"unreached"`
	Commands  []testreach.PackageCommand `json:"commands"`
}

func TestHandleTests_PendingThenReport(t *testing.T) {
	ws, store := newGoTestWorkspace(t)
	srv := New(store, ws, nil)
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })

	var first testsResponse
	getJSON(t, srv.URL()+"/api/tests", &first)
	if !first.Pending {
		t.Fatalf("first GET /api/tests = %+v, want pending", first)
	}

	deadline := time.Now().Add(30 * time.Second)
	var resp testsResponse
	for {
		resp = testsResponse{} // encoding/json leaves fields absent from the new payload untouched, so a stale Pending=true from a prior poll must not survive into this one
		getJSON(t, srv.URL()+"/api/tests", &resp)
		if !resp.Pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GET /api/tests stayed pending for 30s")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(resp.Functions) != 1 {
		t.Fatalf("Functions = %+v, want exactly one (Add)", resp.Functions)
	}
	fr := resp.Functions[0]
	if !fr.Reached {
		t.Error("Add.Reached = false, want true (TestAdd calls it)")
	}
	if len(fr.By) != 1 || fr.By[0] != "webfixture.example/app/pkg.TestAdd" {
		t.Errorf("Add.By = %v, want [webfixture.example/app/pkg.TestAdd]", fr.By)
	}
	if len(resp.Unreached) != 0 {
		t.Errorf("Unreached = %+v, want none", resp.Unreached)
	}
	if len(resp.Commands) != 1 {
		t.Fatalf("Commands = %+v, want exactly one", resp.Commands)
	}
	want := "go test ./pkg -run '^(TestAdd)$'"
	if resp.Commands[0].Command != want {
		t.Errorf("Command = %q, want %q", resp.Commands[0].Command, want)
	}
}

func TestHandleTests_NoRepo(t *testing.T) {
	_, srv := newTestServer(t) // a.txt fixture, no .go files in the diff
	var resp testsResponse
	getJSON(t, srv.URL+"/api/tests", &resp)
	if resp.Pending {
		t.Error("a non-Go diff should never be pending")
	}
	if len(resp.Functions) != 0 {
		t.Errorf("Functions = %+v, want none for a non-Go diff", resp.Functions)
	}
}
