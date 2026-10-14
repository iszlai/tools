package sidecar

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// TestIntegrationRealLlamaServer drives the actual pinned llama-server
// against a real model: start, a short completion, stop. Skipped unless both
// the fetched build and a model are present, so `make check` never needs
// network or a 100+ MB download.
func TestIntegrationRealLlamaServer(t *testing.T) {
	bin, err := filepath.Abs(filepath.Join("..", "..", "..", "third_party", "llama", "dist", "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("third_party/llama/dist/llama-server not present; run `make fetch-llama` to enable this test")
	}
	model := os.Getenv("FLOWREVIEW_TEST_MODEL")
	if model == "" {
		t.Skip("FLOWREVIEW_TEST_MODEL not set; point it at a .gguf to enable this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sc, err := Start(ctx, Options{
		DataRoot:  t.TempDir(),
		ModelPath: model,
		BinDir:    filepath.Dir(bin),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { sc.Stop() })

	p := sc.Provider("integration-test")
	resp, err := p.Complete(ctx, llm.Request{
		Messages:  []llm.Message{{Role: "user", Content: "Count from one to five, one number per line."}},
		MaxTokens: 20,
		Slot:      -1,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content == "" {
		t.Error("want a non-empty completion")
	}
	if resp.CompletionTokens == 0 {
		t.Error("want completion_tokens > 0")
	}
	t.Logf("completion (%d prompt tok, %d completion tok, %s): %q",
		resp.PromptTokens, resp.CompletionTokens, resp.Duration, resp.Content)

	if err := sc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
