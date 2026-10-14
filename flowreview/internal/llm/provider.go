// Package llm is how flowreview talks to a language model. The only
// implementation that ships is the embedded llama-server sidecar; a stub
// provider stands in for tests and builds without the archive.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrUnavailable is returned when no model is running or configured. Callers
// fall back to deterministic text.
var ErrUnavailable = errors.New("llm: no model available")

// Capabilities says what a provider can do.
type Capabilities struct {
	Name       string `json:"name"` // "llama-server", "stub"
	ModelID    string `json:"model_id"`
	Local      bool   `json:"local"` // false shows the remote badge in the UI
	JSONSchema bool   `json:"json_schema"`
	Embeddings bool   `json:"embeddings"`
}

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Request is one completion request.
type Request struct {
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
	// Slot pins the request to a llama-server slot so consecutive requests
	// sharing a prefix reuse its prompt cache; -1 lets the server choose.
	Slot int `json:"slot"`
}

// Response is one completion.
type Response struct {
	Content          string        `json:"content"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	Duration         time.Duration `json:"duration"`
}

// ModelProvider is a language model.
type ModelProvider interface {
	Capabilities() Capabilities
	Complete(ctx context.Context, r Request) (Response, error)
	// CompleteJSON constrains the output to schema (a JSON Schema document);
	// Response.Content is then valid JSON matching it.
	CompleteJSON(ctx context.Context, r Request, schema json.RawMessage) (Response, error)
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	Health(ctx context.Context) error
}
