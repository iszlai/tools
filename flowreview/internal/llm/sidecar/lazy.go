package sidecar

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// Lazy is an llm.ModelProvider that does not spawn its llama-server process
// until the first call (PLAN.md §5 FR-28: "started on first summary request,
// not at app launch"). Every method shares the one Sidecar it starts; Stop
// tears it down if it was ever started, and is a no-op otherwise.
type Lazy struct {
	opts    Options
	modelID string

	mu sync.Mutex
	sc *Sidecar
}

// NewLazy returns a provider over opts that starts its sidecar on first use,
// speaking to it as modelID.
func NewLazy(opts Options, modelID string) *Lazy {
	return &Lazy{opts: opts, modelID: modelID}
}

// ensure starts the sidecar if it has not started yet. A failed attempt is
// not cached -- the only sticky state is success, so a transient problem
// (a port race, disk pressure during extraction) can be retried by the next
// call rather than wedging every future request behind the first failure.
func (l *Lazy) ensure(ctx context.Context) (*Sidecar, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sc != nil {
		return l.sc, nil
	}
	sc, err := Start(ctx, l.opts)
	if err != nil {
		return nil, err
	}
	l.sc = sc
	return sc, nil
}

// Capabilities reports the sidecar's capabilities without starting it --
// they are fixed by the protocol (see Provider.Capabilities), not by
// anything the running process would tell us.
func (l *Lazy) Capabilities() llm.Capabilities {
	return llm.Capabilities{Name: "llama-server", ModelID: l.modelID, Local: true, JSONSchema: true, Embeddings: true}
}

// Complete starts the sidecar if needed, then completes through it.
func (l *Lazy) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	sc, err := l.ensure(ctx)
	if err != nil {
		return llm.Response{}, err
	}
	return sc.Provider(l.modelID).Complete(ctx, r)
}

// CompleteJSON starts the sidecar if needed, then completes through it.
func (l *Lazy) CompleteJSON(ctx context.Context, r llm.Request, schema json.RawMessage) (llm.Response, error) {
	sc, err := l.ensure(ctx)
	if err != nil {
		return llm.Response{}, err
	}
	return sc.Provider(l.modelID).CompleteJSON(ctx, r, schema)
}

// Embed starts the sidecar if needed, then embeds through it.
func (l *Lazy) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	sc, err := l.ensure(ctx)
	if err != nil {
		return nil, err
	}
	return sc.Provider(l.modelID).Embed(ctx, inputs)
}

// Health starts the sidecar if needed, then checks it. Calling Health just to
// find out whether a model is configured would itself trigger the lazy
// start it describes; callers that want to know "is this wired up" without
// spawning anything should inspect whether the Lazy exists, not call Health.
func (l *Lazy) Health(ctx context.Context) error {
	sc, err := l.ensure(ctx)
	if err != nil {
		return err
	}
	return sc.Provider(l.modelID).Health(ctx)
}

// Stop stops the sidecar if Started; otherwise it is a no-op.
func (l *Lazy) Stop() error {
	l.mu.Lock()
	sc := l.sc
	l.mu.Unlock()
	if sc == nil {
		return nil
	}
	return sc.Stop()
}
