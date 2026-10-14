package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/llm"
	"github.com/iszlai/tools/flowreview/internal/llm/stub"
)

// inputFor builds an Input whose path encodes hunkID, so a stub
// CompleteJSONFunc that only sees the llm.Request (never the Pipeline's own
// Item) can still tell which job it is running (see hunkIDFromContent).
func inputFor(hunkID string) Input {
	return Input{Path: "pkg/" + hunkID + ".go", Language: "go", Hunk: "+line\n"}
}

// gate serializes a stub provider's CompleteJSON calls one at a time (via a
// buffered channel of size 1 acting as a mutex that a test can also hold),
// and records the order hunk ids were processed in -- the only way to check
// "the queue's priority order" against a Pipeline whose workers would
// otherwise race each other.
type gate struct {
	mu    sync.Mutex
	order []string
}

func (g *gate) record(hunkID string) {
	g.mu.Lock()
	g.order = append(g.order, hunkID)
	g.mu.Unlock()
}

func (g *gate) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.order...)
}

func jsonSummary(kind, what string) string {
	data, _ := json.Marshal(map[string]any{"kind": kind, "what": what, "confidence": 0.9})
	return string(data)
}

// hunkIDFromPathRE pulls the hunk id inputFor encoded into the prompt's
// "File: pkg/<id>.go" line back out of the request, so a stub
// CompleteJSONFunc can report which job it is running without the Pipeline
// exposing that itself.
var hunkIDFromPathRE = regexp.MustCompile(`File: pkg/([^.]+)\.go`)

func hunkIDFromContent(r llm.Request) string {
	if len(r.Messages) == 0 {
		return ""
	}
	m := hunkIDFromPathRE.FindStringSubmatch(r.Messages[len(r.Messages)-1].Content)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestPipelineServesFromCacheWithoutCallingProvider(t *testing.T) {
	calls := 0
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		calls++
		return llm.Response{Content: jsonSummary("behavior", "did a thing")}, nil
	}
	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()

	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	result := waitForReady(t, pipeline, item)
	if result.What != "did a thing" {
		t.Fatalf("What = %q", result.What)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// A second Request for the same input must hit the cache, not the
	// provider again.
	res := pipeline.Request([]Item{item})
	if _, ok := res.Ready["h1"]; !ok {
		t.Fatal("want h1 ready from cache")
	}
	if calls != 1 {
		t.Fatalf("calls after cache hit = %d, want still 1", calls)
	}
}

// waitForReady polls Request until hunkID is ready or the test times out.
func waitForReady(t *testing.T, p *Pipeline, item Item) summarySnippet {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := p.Request([]Item{item})
		if s, ok := res.Ready[item.HunkID]; ok {
			return summarySnippet{What: s.What, Kind: s.Kind, Fallback: s.Fallback}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to become ready", item.HunkID)
	return summarySnippet{}
}

type summarySnippet struct {
	What     string
	Kind     string
	Fallback bool
}

func TestPipelineFallbackWhenProviderNil(t *testing.T) {
	pipeline := NewPipeline(NewCache(""), nil, "", 1)
	defer pipeline.Close()
	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	s := waitForReady(t, pipeline, item)
	if !s.Fallback {
		t.Error("want Fallback=true with no provider")
	}
}

func TestPipelineFallbackOnProviderError(t *testing.T) {
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		return llm.Response{}, fmt.Errorf("boom")
	}
	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()
	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	s := waitForReady(t, pipeline, item)
	if !s.Fallback {
		t.Error("want Fallback=true when CompleteJSON errors")
	}
}

func TestPipelineFallbackOnInvalidJSON(t *testing.T) {
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		return llm.Response{Content: "not json"}, nil
	}
	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()
	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	s := waitForReady(t, pipeline, item)
	if !s.Fallback {
		t.Error("want Fallback=true when the model's output is not valid JSON")
	}
}

func TestPipelineFallbackOnUnknownKind(t *testing.T) {
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		return llm.Response{Content: jsonSummary("not-a-real-kind", "something")}, nil
	}
	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()
	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	s := waitForReady(t, pipeline, item)
	if !s.Fallback {
		t.Error("want Fallback=true when kind is not one of the schema's enum values")
	}
}

// TestPipelinePriorityOrder checks that with a single worker (so order is
// deterministic), queued jobs run in priority order -- lower Priority first
// -- regardless of the order Request listed them in.
func TestPipelinePriorityOrder(t *testing.T) {
	g := &gate{}
	started := make(chan struct{})
	release := make(chan struct{})
	first := true

	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		id := hunkIDFromContent(r)
		if first {
			first = false
			close(started)
			<-release // hold the only worker here until every item has been queued
		}
		g.record(id)
		return llm.Response{Content: jsonSummary("behavior", "x")}, nil
	}

	pipeline := NewPipeline(NewCache(""), p, "test-model", 1) // one worker: order is deterministic
	defer pipeline.Close()

	// This first item's job starts running immediately and blocks on
	// release, holding the one worker so the next Request's items are all
	// queued (not yet started) before anything else runs.
	pipeline.Request([]Item{{HunkID: "blocker", Priority: 0, Input: inputFor("blocker")}})
	<-started

	pipeline.Request([]Item{
		{HunkID: "low", Priority: 5, Input: inputFor("low")},
		{HunkID: "high", Priority: 0, Input: inputFor("high")},
		{HunkID: "mid", Priority: 2, Input: inputFor("mid")},
	})
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(g.snapshot()) < 4 {
		time.Sleep(10 * time.Millisecond)
	}

	got := g.snapshot()
	want := []string{"blocker", "high", "mid", "low"}
	if len(got) != len(want) {
		t.Fatalf("processed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("processed %v, want %v", got, want)
		}
	}
}

// TestPipelineCancelsUnwantedQueuedJob checks FR-28's "cancellation of
// queued items the client no longer wants": a job queued (not yet running)
// for a hunk that a later Request no longer names must not run.
func TestPipelineCancelsUnwantedQueuedJob(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	first := true

	g := &gate{}
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		id := hunkIDFromContent(r)
		if first {
			first = false
			close(started)
			<-release
		}
		g.record(id)
		return llm.Response{Content: jsonSummary("behavior", "x")}, nil
	}

	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()

	pipeline.Request([]Item{{HunkID: "blocker", Priority: 0, Input: inputFor("blocker")}})
	<-started

	// "unwanted" is queued here...
	pipeline.Request([]Item{
		{HunkID: "unwanted", Priority: 0, Input: inputFor("unwanted")},
	})
	// ...then a later Request for a disjoint set must cancel it before it
	// ever runs.
	pipeline.Request([]Item{
		{HunkID: "wanted", Priority: 0, Input: inputFor("wanted")},
	})
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(g.snapshot()) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // give a wrongly-not-cancelled job a chance to also run

	got := g.snapshot()
	for _, id := range got {
		if id == "unwanted" {
			t.Fatalf("processed %v: \"unwanted\" should have been cancelled", got)
		}
	}
	foundWanted := false
	for _, id := range got {
		if id == "wanted" {
			foundWanted = true
		}
	}
	if !foundWanted {
		t.Fatalf("processed %v, want \"wanted\" to have run", got)
	}
}

func TestPipelineRegenerateOverwritesCache(t *testing.T) {
	answer := "first answer"
	p := stub.New()
	p.CompleteJSONFunc = func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		return llm.Response{Content: jsonSummary("behavior", answer)}, nil
	}
	pipeline := NewPipeline(NewCache(""), p, "test-model", 1)
	defer pipeline.Close()

	item := Item{HunkID: "h1", Priority: 0, Input: inputFor("h1")}
	first := waitForReady(t, pipeline, item)
	if first.What != "first answer" {
		t.Fatalf("What = %q", first.What)
	}

	answer = "second answer"
	pipeline.Regenerate(item)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := pipeline.Request([]Item{item})
		if s, ok := res.Ready["h1"]; ok && s.What == "second answer" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for Regenerate's fresh answer to replace the cached one")
}
