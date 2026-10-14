package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func TestProviderCapabilities(t *testing.T) {
	p := NewProvider("http://127.0.0.1:1", "qwen")
	caps := p.Capabilities()
	if caps.Name != "llama-server" || caps.ModelID != "qwen" || !caps.Local || !caps.JSONSchema || !caps.Embeddings {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
}

func TestProviderComplete_RequestShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		got = decodeBody(t, r)
		w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	resp, err := p.Complete(context.Background(), llm.Request{
		Messages:    []llm.Message{{Role: "user", Content: "hello"}},
		MaxTokens:   20,
		Temperature: 0.2,
		Slot:        -1,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi" || resp.PromptTokens != 3 || resp.CompletionTokens != 1 {
		t.Errorf("response = %+v", resp)
	}

	if _, ok := got["id_slot"]; ok {
		t.Error("id_slot must be omitted when Slot < 0")
	}
	if got["max_tokens"] != float64(20) {
		t.Errorf("max_tokens = %v", got["max_tokens"])
	}
	if got["temperature"] != 0.2 {
		t.Errorf("temperature = %v", got["temperature"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", got["messages"])
	}
}

func TestProviderComplete_SlotPinning(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		w.Write([]byte(`{"choices":[{"message":{"content":""}}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	if _, err := p.Complete(context.Background(), llm.Request{Slot: 1}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got["id_slot"] != float64(1) {
		t.Errorf("id_slot = %v, want 1", got["id_slot"])
	}
}

func TestProviderCompleteJSON_RequestShape(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"k":{"type":"string"}},"required":["k"]}`)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"k\":\"v\"}"}}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	resp, err := p.CompleteJSON(context.Background(), llm.Request{Slot: -1}, schema)
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if resp.Content != `{"k":"v"}` {
		t.Errorf("content = %q", resp.Content)
	}

	rf, ok := got["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong shape: %v", got["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema wrong shape: %v", rf["json_schema"])
	}
	if js["name"] == nil || js["name"] == "" {
		t.Error("response_format.json_schema.name must be set")
	}
	if _, ok := js["schema"].(map[string]any); !ok {
		t.Errorf("response_format.json_schema.schema wrong shape: %v", js["schema"])
	}
	if _, ok := got["json_schema"]; ok {
		t.Error("bare json_schema must not be sent alongside response_format")
	}
}

// TestProviderCompleteJSON_FallbackOnUnsupported covers a llama-server build
// that rejects response_format: CompleteJSON must retry once with the schema
// under the bare top-level json_schema field instead of failing outright.
func TestProviderCompleteJSON_FallbackOnUnsupported(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			if _, ok := body["response_format"]; !ok {
				t.Error("first call must use response_format")
			}
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(`{"error":{"code":501,"message":"This server does not support response_format","type":"not_supported_error"}}`))
			return
		}
		if _, ok := body["response_format"]; ok {
			t.Error("retry must not include response_format")
		}
		if _, ok := body["json_schema"]; !ok {
			t.Error("retry must include the bare json_schema field")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	resp, err := p.CompleteJSON(context.Background(), llm.Request{Slot: -1}, schema)
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if resp.Content != "{}" {
		t.Errorf("content = %q", resp.Content)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestProviderComplete_Cancellation(t *testing.T) {
	// The handler never reads r.Body, so the server has no occasion to
	// notice the client dropped the connection and r.Context() may never
	// cancel; bound the wait so the handler (and so srv.Close, which waits
	// for it) returns regardless of whether that happens.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Complete(ctx, llm.Request{Slot: -1})
		done <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the request reach the (blocked) handler
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Complete did not return after context cancellation")
	}
}

func TestProviderEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body := decodeBody(t, r)
		inputs, _ := body["input"].([]any)
		if len(inputs) != 2 {
			t.Fatalf("input = %v", body["input"])
		}
		// Respond out of order to verify Index puts vectors back in place.
		w.Write([]byte(`{"data":[{"embedding":[0.2],"index":1},{"embedding":[0.1],"index":0}]}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][0] != 0.2 {
		t.Errorf("vecs = %v", vecs)
	}
}

func TestProviderHealth(t *testing.T) {
	ok := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	p := NewProvider(srv.URL, "m")
	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health: %v", err)
	}

	ok = false
	if err := p.Health(context.Background()); err == nil {
		t.Error("Health: want error when the server reports unhealthy")
	}
}

func TestProviderHealth_Unreachable(t *testing.T) {
	// Nothing listens here; the request must fail fast, not hang, and the
	// error must identify this as "no model available".
	p := NewProvider("http://127.0.0.1:1", "m")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := p.Health(ctx)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, llm.ErrUnavailable) {
		t.Errorf("err = %v, want it to wrap llm.ErrUnavailable", err)
	}
}
