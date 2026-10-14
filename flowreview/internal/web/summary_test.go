package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/llm"
	"github.com/iszlai/tools/flowreview/internal/llm/stub"
	"github.com/iszlai/tools/flowreview/internal/llm/summary"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// firstHunkID returns the id of the fixture's first (and, for newFixtureRepo,
// only) hunk.
func firstHunkID(t *testing.T, hs testServer, fileID string) string {
	t.Helper()
	var rows []render.Row
	getJSON(t, hs.URL+"/api/file/"+fileID+"/rows", &rows)
	for _, r := range rows {
		if r.Kind == "hunk" {
			return r.HunkID
		}
	}
	t.Fatalf("fixture produced no hunk in %+v", rows)
	return ""
}

func jsonCompleteOK(kind, what string) func(context.Context, llm.Request, json.RawMessage) (llm.Response, error) {
	return func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		data, _ := json.Marshal(map[string]any{"kind": kind, "what": what, "confidence": 0.8})
		return llm.Response{Content: string(data)}, nil
	}
}

// TestHandleSummaries_NoPipelineReportsEverythingPending checks the
// "summaries not wired up at all" case (web.New's default): every requested
// id comes back pending, never an error, so the UI's shimmer just keeps
// showing rather than erroring.
func TestHandleSummaries_NoPipelineReportsEverythingPending(t *testing.T) {
	_, hs := newTestServer(t)
	fileID := fileIDByPath(t, hs, "a.txt")
	hunkID := firstHunkID(t, hs, fileID)

	var resp summariesResponse
	status := postJSON(t, hs.URL+"/api/summaries", summariesRequest{HunkIDs: []string{hunkID}}, &resp)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", status.StatusCode)
	}
	if len(resp.Ready) != 0 {
		t.Errorf("Ready = %+v, want empty with no pipeline", resp.Ready)
	}
	if len(resp.Pending) != 1 || resp.Pending[0] != hunkID {
		t.Errorf("Pending = %v, want [%s]", resp.Pending, hunkID)
	}
}

// TestHandleSummaries_StubProviderEndToEnd drives the full FR-28 pipeline
// through the HTTP handler with a stub provider standing in for the sidecar:
// POST .../summaries queues the hunk, a client poll via GET picks up the
// answer once the worker finishes it.
func TestHandleSummaries_StubProviderEndToEnd(t *testing.T) {
	srv, hs := newTestServer(t)
	fileID := fileIDByPath(t, hs, "a.txt")
	hunkID := firstHunkID(t, hs, fileID)

	p := stub.New()
	p.CompleteJSONFunc = jsonCompleteOK("behavior", "changed line two")
	pipeline := summary.NewPipeline(summary.NewCache(t.TempDir()), p, "test-model", 1)
	srv.SetSummaries(pipeline)

	var first summariesResponse
	postJSON(t, hs.URL+"/api/summaries", summariesRequest{HunkIDs: []string{hunkID}}, &first)
	if len(first.Ready) != 0 {
		t.Fatalf("Ready on first request = %+v, want empty (nothing cached yet)", first.Ready)
	}
	if len(first.Pending) != 1 || first.Pending[0] != hunkID {
		t.Fatalf("Pending = %v, want [%s]", first.Pending, hunkID)
	}

	deadline := time.Now().Add(5 * time.Second)
	var ready model.SnippetSummary
	for time.Now().Before(deadline) {
		var poll summariesResponse
		getJSON(t, hs.URL+"/api/summaries?ids="+hunkID, &poll)
		if s, ok := poll.Ready[hunkID]; ok {
			ready = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ready.What != "changed line two" {
		t.Fatalf("What = %q, want %q (ready=%+v)", ready.What, "changed line two", ready)
	}
	if ready.Fallback {
		t.Error("want Fallback=false from a successful stub completion")
	}
}

// TestHandleSummaries_UnknownHunkIDIsSkipped checks that an id not in the
// current diff is silently dropped rather than failing the whole batch.
func TestHandleSummaries_UnknownHunkIDIsSkipped(t *testing.T) {
	srv, hs := newTestServer(t)
	pipeline := summary.NewPipeline(summary.NewCache(""), nil, "", 1)
	srv.SetSummaries(pipeline)

	var resp summariesResponse
	postJSON(t, hs.URL+"/api/summaries", summariesRequest{HunkIDs: []string{"does-not-exist"}}, &resp)
	if len(resp.Ready) != 0 || len(resp.Pending) != 0 {
		t.Errorf("got Ready=%+v Pending=%v, want both empty for an unknown id", resp.Ready, resp.Pending)
	}
}

// TestHandleRegenerateSummary_ForcesFreshRun checks that regenerate bypasses
// whatever is cached and the new answer, once ready, replaces it.
func TestHandleRegenerateSummary_ForcesFreshRun(t *testing.T) {
	srv, hs := newTestServer(t)
	fileID := fileIDByPath(t, hs, "a.txt")
	hunkID := firstHunkID(t, hs, fileID)

	answer := "first answer"
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		data, _ := json.Marshal(map[string]any{"kind": "behavior", "what": answer, "confidence": 0.5})
		return llm.Response{Content: string(data)}, nil
	}
	pipeline := summary.NewPipeline(summary.NewCache(t.TempDir()), p, "test-model", 1)
	srv.SetSummaries(pipeline)

	waitForHunkReady(t, hs, hunkID, "first answer")

	answer = "regenerated answer"
	status := postJSON(t, hs.URL+"/api/summaries/regenerate", map[string]string{"hunk_id": hunkID}, nil)
	if status.StatusCode != http.StatusAccepted {
		t.Fatalf("regenerate status = %d, want 202", status.StatusCode)
	}

	waitForHunkReady(t, hs, hunkID, "regenerated answer")
}

// TestHandleRegenerateSummary_UnknownHunkID404s checks the error path.
func TestHandleRegenerateSummary_UnknownHunkID404s(t *testing.T) {
	srv, hs := newTestServer(t)
	pipeline := summary.NewPipeline(summary.NewCache(""), nil, "", 1)
	srv.SetSummaries(pipeline)

	status := postJSON(t, hs.URL+"/api/summaries/regenerate", map[string]string{"hunk_id": "does-not-exist"}, nil)
	if status.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status.StatusCode)
	}
}

func waitForHunkReady(t *testing.T, hs testServer, hunkID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var poll summariesResponse
		getJSON(t, hs.URL+"/api/summaries?ids="+hunkID, &poll)
		if s, ok := poll.Ready[hunkID]; ok && s.What == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for hunk %s to read %q", hunkID, want)
}
