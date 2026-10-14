package summary

import (
	"container/heap"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/llm"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// DefaultWorkers is "2 in flight" from PLAN.md §5 FR-28.
const DefaultWorkers = 2

// requestTimeout bounds one CompleteJSON call: long enough for a slow local
// model on a cold cache-prompt slot, short enough that a wedged request
// cannot hold a worker forever and starve the queue behind it.
const requestTimeout = 45 * time.Second

// Item is one hunk a caller wants a summary for.
type Item struct {
	HunkID   string
	Priority int // lower runs sooner: the caller's visible-hunks-first, then flow/file order
	Input    Input
}

// Result is Pipeline.Request's answer: Ready holds every summary already
// cached (served immediately, nothing to wait for), Pending names the rest,
// now queued.
type Result struct {
	Ready   map[string]model.SnippetSummary
	Pending []string
}

// Pipeline computes, caches and serves hunk summaries: Request answers from
// cache and queues the rest; a fixed pool of workers drains the queue,
// calling Provider when one is configured and falling back to a
// deterministic summary otherwise or on any failure (PLAN.md §5 FR-28).
// Provider may be nil, meaning "fallback only" -- every job's answer is then
// Fallback's deterministic text, computed without ever touching the network.
type Pipeline struct {
	cache    *Cache
	provider llm.ModelProvider
	modelID  string

	mu     sync.Mutex
	cond   *sync.Cond
	pq     jobQueue
	byHunk map[string]*job
	seq    int
	closed bool
}

// NewPipeline builds a Pipeline over cache, calling provider (nil for
// fallback-only) as modelID, with workers goroutines draining the queue
// (DefaultWorkers if workers <= 0). Workers run for the Pipeline's lifetime;
// call Close to stop them.
func NewPipeline(cache *Cache, provider llm.ModelProvider, modelID string, workers int) *Pipeline {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	p := &Pipeline{cache: cache, provider: provider, modelID: modelID, byHunk: make(map[string]*job)}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

// Close stops every worker once the queue drains no further jobs will start.
// Jobs already running finish normally; Close does not wait for them.
func (p *Pipeline) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

// Request answers immediately from cache and queues whatever is missing,
// with Item.Priority (and, within an equal priority, the order items were
// given in) deciding how soon a queued job runs. Any job currently queued
// (not yet running) for a hunk id that items no longer names is dropped --
// "cancellation of queued items the client no longer wants" (FR-28): a
// client is expected to call Request with its whole current visible set each
// time the viewport moves, so anything missing from the new set is, by
// definition, no longer wanted.
func (p *Pipeline) Request(items []Item) Result {
	ready := make(map[string]model.SnippetSummary, len(items))
	pending := make([]string, 0, len(items))

	wanted := make(map[string]bool, len(items))
	for _, it := range items {
		wanted[it.HunkID] = true
	}
	p.cancelUnwanted(wanted)

	for _, it := range items {
		key := model.SummaryKey(p.modelID, PromptVersion, it.Input.Hunk, it.Input.EnclosingFunction, "")
		if s, ok := p.cache.Get(key); ok {
			ready[it.HunkID] = s
			continue
		}
		pending = append(pending, it.HunkID)
		p.enqueue(it, key, false)
	}
	return Result{Ready: ready, Pending: pending}
}

// Regenerate forces a fresh run for item, bypassing whatever is cached under
// its key: the existing cache entry (if any) is left in place until the new
// run completes and overwrites it, so a concurrent reader never sees a gap.
func (p *Pipeline) Regenerate(item Item) {
	key := model.SummaryKey(p.modelID, PromptVersion, item.Input.Hunk, item.Input.EnclosingFunction, "")
	p.enqueue(item, key, true)
}

// cancelUnwanted removes every queued (not running) job whose hunk id is not
// in wanted.
func (p *Pipeline) cancelUnwanted(wanted map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, j := range p.byHunk {
		if wanted[id] || j.state != stateQueued {
			continue
		}
		heap.Remove(&p.pq, j.index)
		delete(p.byHunk, id)
	}
}

// enqueue adds item to the queue under key. A hunk already queued just has
// its priority raised to the better of the two (never lowered -- a hunk a
// second caller also wants should not run later than originally scheduled);
// force skips that and always starts a fresh job, for Regenerate. A hunk
// already running is left alone either way: enqueue never interrupts a job
// in flight, only ones still waiting.
func (p *Pipeline) enqueue(item Item, key string, force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if j, ok := p.byHunk[item.HunkID]; ok {
		if !force {
			if item.Priority < j.priority && j.state == stateQueued {
				j.priority = item.Priority
				heap.Fix(&p.pq, j.index)
			}
			return
		}
		if j.state == stateQueued {
			heap.Remove(&p.pq, j.index)
		}
		delete(p.byHunk, item.HunkID)
	}

	p.seq++
	j := &job{hunkID: item.HunkID, key: key, input: item.Input, priority: item.Priority, seq: p.seq, state: stateQueued}
	heap.Push(&p.pq, j)
	p.byHunk[item.HunkID] = j
	p.cond.Signal()
}

// worker drains the queue one job at a time for the Pipeline's lifetime;
// NewPipeline starts DefaultWorkers of these, which is what bounds how many
// CompleteJSON calls are ever in flight at once.
func (p *Pipeline) worker() {
	for {
		p.mu.Lock()
		for p.pq.Len() == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.pq.Len() == 0 {
			p.mu.Unlock()
			return
		}
		j := heap.Pop(&p.pq).(*job)
		j.state = stateRunning
		p.mu.Unlock()

		s := p.run(j)

		p.mu.Lock()
		delete(p.byHunk, j.hunkID)
		p.mu.Unlock()

		p.cache.Put(s)
	}
}

// run produces j's summary: Fallback when there is no provider, when
// CompleteJSON errors, or when its output does not parse into a valid
// SnippetSummary (PLAN.md §5 FR-28's "deterministic fallback ... when the
// model fails or returns invalid JSON").
func (p *Pipeline) run(j *job) model.SnippetSummary {
	if p.provider == nil {
		return Fallback(j.key, j.hunkID, j.input)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req := llm.Request{Messages: Messages(j.input), Temperature: 0.2, Slot: -1}
	resp, err := p.provider.CompleteJSON(ctx, req, Schema)
	if err != nil {
		return Fallback(j.key, j.hunkID, j.input)
	}

	var parsed struct {
		Kind        string   `json:"kind"`
		What        string   `json:"what"`
		InferredWhy string   `json:"inferred_why"`
		Impact      string   `json:"impact"`
		RiskFlags   []string `json:"risk_flags"`
		Confidence  float64  `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil || !validKind(parsed.Kind) || parsed.What == "" {
		return Fallback(j.key, j.hunkID, j.input)
	}

	return model.SnippetSummary{
		Key:           j.key,
		HunkID:        j.hunkID,
		ModelID:       p.modelID,
		PromptVersion: PromptVersion,
		Kind:          parsed.Kind,
		What:          parsed.What,
		InferredWhy:   parsed.InferredWhy,
		Impact:        parsed.Impact,
		RiskFlags:     parsed.RiskFlags,
		Confidence:    parsed.Confidence,
		CreatedAt:     time.Now().UTC(),
	}
}

func validKind(k string) bool {
	switch k {
	case "behavior", "refactor", "rename", "test", "config":
		return true
	default:
		return false
	}
}
