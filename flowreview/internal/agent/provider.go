// Package agent runs opt-in agentic review: a provider reads the diff with its
// context and emits findings, each backed by evidence that is verified before
// it reaches the UI.
package agent

import (
	"context"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Capabilities says what an agent provider can do.
type Capabilities struct {
	Name  string `json:"name"`
	Local bool   `json:"local"`
}

// Limits bound one run.
type Limits struct {
	Timeout      time.Duration `json:"timeout"`
	MaxTokens    int           `json:"max_tokens"`
	MaxFunctions int           `json:"max_functions"`
	MaxFindings  int           `json:"max_findings,omitempty"` // 0 = unlimited; Runner stops forwarding past this
}

// Scope says what to review. Empty slices mean the whole session.
type Scope struct {
	Paths   []string `json:"paths,omitempty"`
	HunkIDs []string `json:"hunk_ids,omitempty"`
	Limits  Limits   `json:"limits"`
}

// AgentProvider reviews a session and streams findings to emit as they are
// found. Review returns when the run ends, the limits are hit or ctx is done.
type AgentProvider interface {
	Capabilities() Capabilities
	Review(ctx context.Context, s *model.ReviewSession, scope Scope, emit func(model.AgentFinding)) error
}
