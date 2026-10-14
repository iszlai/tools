package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// Provider speaks llm.ModelProvider to one running llama-server over its
// OpenAI-compatible HTTP API.
type Provider struct {
	BaseURL string
	ModelID string
	// Client overrides the HTTP client; nil uses http.DefaultClient.
	Client *http.Client
}

// NewProvider wraps a running llama-server at baseURL (e.g.
// "http://127.0.0.1:8080"). Sidecar.Provider is the usual way to get one;
// this constructor also lets tests point at an httptest server.
func NewProvider(baseURL, modelID string) *Provider {
	return &Provider{BaseURL: strings.TrimRight(baseURL, "/"), ModelID: modelID}
}

func (p *Provider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// Capabilities reports what this provider supports. JSONSchema and
// Embeddings describe the protocol, not whether the loaded model is suited to
// them — a chat model will still answer /v1/embeddings with an error unless
// the server was started with --embeddings.
func (p *Provider) Capabilities() llm.Capabilities {
	return llm.Capabilities{Name: "llama-server", ModelID: p.ModelID, Local: true, JSONSchema: true, Embeddings: true}
}

type chatCompletionRequest struct {
	Messages       []llm.Message   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	IDSlot         *int            `json:"id_slot,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	// JSONSchema is llama.cpp's own top-level field, predating OpenAI's
	// response_format; see CompleteJSON.
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema jsonSchemaBody `json:"json_schema"`
}

type jsonSchemaBody struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func requestFromLLM(r llm.Request) chatCompletionRequest {
	req := chatCompletionRequest{Messages: r.Messages, MaxTokens: r.MaxTokens, Temperature: r.Temperature}
	if r.Slot >= 0 {
		slot := r.Slot
		req.IDSlot = &slot
	}
	return req
}

// Complete asks llama-server for one chat completion.
func (p *Provider) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	return p.chatCompletion(ctx, requestFromLLM(r))
}

// CompleteJSON constrains output with the OpenAI-compatible
// response_format: {type: json_schema}, which this llama-server version
// accepts (verified against the pinned build — see
// third_party/llama/README.md). If the server rejects it, this retries once
// with the schema under llama.cpp's own bare top-level json_schema field,
// which this and older builds also accept, instead of failing the request.
func (p *Provider) CompleteJSON(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
	req := requestFromLLM(r)
	req.ResponseFormat = &responseFormat{Type: "json_schema", JSONSchema: jsonSchemaBody{Name: "response", Schema: schema}}

	resp, err := p.chatCompletion(ctx, req)
	if err != nil && unsupportedResponseFormat(err) {
		req.ResponseFormat = nil
		req.JSONSchema = schema
		resp, err = p.chatCompletion(ctx, req)
	}
	return resp, err
}

func (p *Provider) chatCompletion(ctx context.Context, req chatCompletionRequest) (llm.Response, error) {
	start := time.Now()
	body, err := json.Marshal(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("sidecar: %w", err)
	}
	var out chatCompletionResponse
	if err := p.do(ctx, http.MethodPost, "/v1/chat/completions", body, &out); err != nil {
		return llm.Response{}, err
	}
	var content string
	if len(out.Choices) > 0 {
		content = out.Choices[0].Message.Content
	}
	return llm.Response{
		Content:          content,
		PromptTokens:     out.Usage.PromptTokens,
		CompletionTokens: out.Usage.CompletionTokens,
		Duration:         time.Since(start),
	}, nil
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed returns one vector per input, in input order (the server may answer
// out of order; Index puts each one back in place).
func (p *Provider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Input []string `json:"input"`
	}{inputs})
	if err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	var out embeddingsResponse
	if err := p.do(ctx, http.MethodPost, "/v1/embeddings", body, &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(inputs))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

// Health calls llama-server's /health.
func (p *Provider) Health(ctx context.Context) error {
	return p.do(ctx, http.MethodGet, "/health", nil, nil)
}

func (p *Provider) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("sidecar: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client().Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %s %s: %v", llm.ErrUnavailable, method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("sidecar: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("sidecar: decode %s: %w", path, err)
	}
	return nil
}

// apiError is a llama-server {"error": {...}} response.
type apiError struct {
	Status  int
	Message string
	Type    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("llama-server: %s (status %d)", e.Message, e.Status)
}

func parseAPIError(status int, body []byte) error {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error.Message != "" {
		return &apiError{Status: status, Message: wrapped.Error.Message, Type: wrapped.Error.Type}
	}
	return &apiError{Status: status, Message: string(bytes.TrimSpace(body))}
}

func unsupportedResponseFormat(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusNotImplemented || strings.Contains(ae.Message, "response_format")
}
