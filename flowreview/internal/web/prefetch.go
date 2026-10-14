// Summary prefetch: once a review has asked for its first summaries, every
// other hunk is queued as background work in flow order, so by the time the
// reviewer scrolls or navigates to a hunk its summary is usually ready.
// Hunks on screen still go first (summary.BackgroundPriority).
package web

import (
	"context"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/order"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// prefetchOrderWait bounds how long prefetch waits for flow-order analysis
// before falling back to diff order: the visible hunks are being summarized
// meanwhile, so a short wait costs nothing.
const prefetchOrderWait = 30 * time.Second

var (
	prefetchMu      sync.Mutex
	prefetchStarted = map[*review.Workspace]bool{}
)

// maybePrefetch starts the background prefetch for the current workspace,
// once per workspace (review.Open hands out a fresh one per session).
func (s *Server) maybePrefetch() {
	ws := s.workspace()
	prefetchMu.Lock()
	started := prefetchStarted[ws]
	prefetchStarted[ws] = true
	prefetchMu.Unlock()
	if !started {
		go s.prefetch(ws)
	}
}

func (s *Server) prefetch(ws *review.Workspace) {
	pipeline := s.summariesPipeline()
	if pipeline == nil {
		return
	}
	ids := prefetchOrder(ws)
	// buildSummaryItems reads s.workspace(); stop if the session was
	// swapped while waiting for the order.
	if s.workspace() != ws {
		return
	}
	items := s.buildSummaryItems(context.Background(), ids)
	pipeline.Prefetch(items)
}

// prefetchOrder lists ws's hunk ids in flow order when the analysis finishes
// within prefetchOrderWait, in diff order otherwise.
func prefetchOrder(ws *review.Workspace) []string {
	_, files, _ := ws.Snapshot()
	byID := make(map[string]model.ChangedFile, len(files))
	for _, f := range files {
		byID[f.ID] = f
	}

	fileOrder := make([]string, 0, len(files))
	deadline := time.Now().Add(prefetchOrderWait)
	for time.Now().Before(deadline) {
		st := orderAnalysisFor(ws)
		if !st.ready {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		in := st.in
		in.Files = files
		if o, err := order.Build(order.KindFlow, in); err == nil {
			for _, g := range o.Groups {
				for _, fe := range g.Files {
					fileOrder = append(fileOrder, fe.FileID)
				}
			}
			for _, fe := range o.Unclassified {
				fileOrder = append(fileOrder, fe.FileID)
			}
		}
		break
	}

	seen := make(map[string]bool, len(files))
	var ids []string
	add := func(f model.ChangedFile) {
		if seen[f.ID] {
			return
		}
		seen[f.ID] = true
		for _, h := range f.Hunks {
			ids = append(ids, h.ID)
		}
	}
	for _, id := range fileOrder {
		if f, ok := byID[id]; ok {
			add(f)
		}
	}
	for _, f := range files { // anything flow order missed, and the fallback
		add(f)
	}
	return ids
}
