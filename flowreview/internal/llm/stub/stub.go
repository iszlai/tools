// Package stub is a deterministic llm.ModelProvider for tests, and for
// builds of flowreview made without the llama.cpp archive (see
// third_party/llama). It never calls a model: text output is derived from the
// request, JSON output is a minimal value satisfying the given schema, and
// embeddings are a stable hash of the input. Tests that need specific output
// set CompleteFunc / CompleteJSONFunc instead.
package stub

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// Provider is the stub llm.ModelProvider.
type Provider struct {
	ModelID string

	// CompleteFunc and CompleteJSONFunc, when set, replace the deterministic
	// default so a test can inject specific output.
	CompleteFunc     func(ctx context.Context, r llm.Request) (llm.Response, error)
	CompleteJSONFunc func(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error)
}

// New returns a stub provider with the default deterministic behaviour.
func New() *Provider {
	return &Provider{ModelID: "stub"}
}

// Capabilities reports the stub as local with both JSON-schema and
// embeddings support, so callers above it exercise the same code paths they
// would against a real sidecar.
func (p *Provider) Capabilities() llm.Capabilities {
	return llm.Capabilities{Name: "stub", ModelID: p.ModelID, Local: true, JSONSchema: true, Embeddings: true}
}

// Complete returns deterministic text derived from the last message, unless
// CompleteFunc is set.
func (p *Provider) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	if p.CompleteFunc != nil {
		return p.CompleteFunc(ctx, r)
	}
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	content := deterministicText(r)
	return llm.Response{
		Content:          content,
		PromptTokens:     promptTokenEstimate(r),
		CompletionTokens: len(strings.Fields(content)),
	}, nil
}

// CompleteJSON returns the minimal value satisfying schema (every required
// property present with a zero value of its declared type, or its first enum
// value), unless CompleteJSONFunc is set.
func (p *Provider) CompleteJSON(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
	if p.CompleteJSONFunc != nil {
		return p.CompleteJSONFunc(ctx, r, schema)
	}
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	value, err := minimalValid(schema)
	if err != nil {
		return llm.Response{}, fmt.Errorf("stub: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return llm.Response{}, fmt.Errorf("stub: %w", err)
	}
	return llm.Response{
		Content:          string(data),
		PromptTokens:     promptTokenEstimate(r),
		CompletionTokens: len(data),
	}, nil
}

// Embed returns one deterministic vector per input: same input, same vector,
// every time, with no model and no randomness.
func (p *Provider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		out[i] = deterministicVector(s)
	}
	return out, nil
}

// Health always succeeds: the stub is never "down".
func (p *Provider) Health(ctx context.Context) error { return ctx.Err() }

func deterministicText(r llm.Request) string {
	if len(r.Messages) == 0 {
		return "stub: no messages"
	}
	last := r.Messages[len(r.Messages)-1]
	return fmt.Sprintf("stub: %s", last.Content)
}

func promptTokenEstimate(r llm.Request) int {
	n := 0
	for _, m := range r.Messages {
		n += len(strings.Fields(m.Content))
	}
	return n
}

const embedDims = 8

// deterministicVector hashes s into a fixed-length vector in [0, 1). Not a
// real embedding — just stable and input-sensitive, enough for tests that
// check indexing, caching or "same input, same vector", not semantics.
func deterministicVector(s string) []float32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	seed := h.Sum32()
	out := make([]float32, embedDims)
	for i := range out {
		seed = seed*1664525 + 1013904223 // LCG; deterministic, no package-level rand state
		out[i] = float32(seed%1000) / 1000
	}
	return out
}
