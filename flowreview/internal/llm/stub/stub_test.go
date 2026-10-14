package stub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

func TestCapabilities(t *testing.T) {
	p := New()
	caps := p.Capabilities()
	if caps.Name != "stub" || !caps.Local || !caps.JSONSchema || !caps.Embeddings {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
}

func TestComplete_Deterministic(t *testing.T) {
	p := New()
	r := llm.Request{Messages: []llm.Message{{Role: "user", Content: "hello there"}}}
	a, err := p.Complete(context.Background(), r)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b, err := p.Complete(context.Background(), r)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if a != b {
		t.Errorf("not deterministic: %+v != %+v", a, b)
	}
	if a.Content == "" {
		t.Error("want non-empty content")
	}
}

func TestComplete_Cancellation(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestComplete_Injected(t *testing.T) {
	want := llm.Response{Content: "injected"}
	p := &Provider{CompleteFunc: func(ctx context.Context, r llm.Request) (llm.Response, error) {
		return want, nil
	}}
	got, err := p.Complete(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCompleteJSON_MinimalValidObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"kind": {"type": "string", "enum": ["behavior", "refactor"]},
			"what": {"type": "string"},
			"confidence": {"type": "number"},
			"risk_flags": {"type": "array"}
		},
		"required": ["kind", "what", "confidence", "risk_flags"]
	}`)
	p := New()
	resp, err := p.CompleteJSON(context.Background(), llm.Request{}, schema)
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, resp.Content)
	}
	for _, field := range []string{"kind", "what", "confidence", "risk_flags"} {
		if _, ok := got[field]; !ok {
			t.Errorf("missing required field %q in %v", field, got)
		}
	}
	if got["kind"] != "behavior" {
		t.Errorf("kind = %v, want the first enum value", got["kind"])
	}
}

func TestCompleteJSON_InvalidSchema(t *testing.T) {
	p := New()
	if _, err := p.CompleteJSON(context.Background(), llm.Request{}, json.RawMessage(`not json`)); err == nil {
		t.Error("want an error for an invalid schema")
	}
}

func TestCompleteJSON_Injected(t *testing.T) {
	want := llm.Response{Content: `{"x":1}`}
	p := &Provider{CompleteJSONFunc: func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
		return want, nil
	}}
	got, err := p.CompleteJSON(context.Background(), llm.Request{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestEmbed_Deterministic(t *testing.T) {
	p := New()
	a, err := p.Embed(context.Background(), []string{"same", "different"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, err := p.Embed(context.Background(), []string{"same", "different"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			t.Fatalf("vector length changed between calls")
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				t.Errorf("vector %d not deterministic: %v != %v", i, a[i], b[i])
			}
		}
	}
	if fmtVec(a[0]) == fmtVec(a[1]) {
		t.Error("different inputs produced the same vector")
	}
}

func TestHealth(t *testing.T) {
	if err := New().Health(context.Background()); err != nil {
		t.Errorf("Health: %v", err)
	}
}

func fmtVec(v []float32) string {
	b, _ := json.Marshal(v)
	return string(b)
}
