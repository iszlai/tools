// FR-17/FR-24: GET /api/order, the HTTP surface for flow ordering. Building
// the Go and Gherkin analysis Flow uses (a go/packages Load plus an
// SSA-based call graph) can take seconds on a large repository, so this
// never blocks a request on it: the first request for the session's current
// Workspace starts that analysis in the background and every request
// answers {"pending": true} until it finishes, after which the cached
// result is reused for the life of that Workspace. web.go's only change for
// this file is the one route-registration line in routes().
package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/order"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// orderAnalysisTimeout bounds the background go/packages Load + call graph
// build: long enough for a real repo (see golang/BENCHMARK.md), short
// enough that a pathological one does not pin a goroutine forever.
const orderAnalysisTimeout = 2 * time.Minute

// orderState is one Workspace's Go/Gherkin analysis for flow ordering, once
// computed. A value is never mutated after publishing -- see
// computeOrderAnalysis -- so reading one without holding orderMu is safe.
type orderState struct {
	ready bool
	in    order.Inputs // Go and Gherkin only; Files is filled in fresh per request
}

// pendingState is the shared, never-mutated placeholder every caller sees
// while analysis for a Workspace is still running.
var pendingState = &orderState{}

var (
	orderMu    sync.Mutex
	orderCache = map[*review.Workspace]*orderState{}
)

// orderAnalysisFor returns ws's cached analysis, starting it in the
// background on the first call for this Workspace (review.Open hands out a
// fresh *Workspace per session, so a new one -- including after
// POST /api/open swaps the session -- always starts its own analysis).
func orderAnalysisFor(ws *review.Workspace) *orderState {
	orderMu.Lock()
	defer orderMu.Unlock()
	if st, ok := orderCache[ws]; ok {
		return st
	}
	orderCache[ws] = pendingState
	go computeOrderAnalysis(ws)
	return pendingState
}

func computeOrderAnalysis(ws *review.Workspace) {
	_, files, repo := ws.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), orderAnalysisTimeout)
	defer cancel()

	in := buildAnalysisInputs(ctx, files, repo)

	orderMu.Lock()
	orderCache[ws] = &orderState{ready: true, in: in}
	orderMu.Unlock()
}

// buildAnalysisInputs runs Go and Gherkin analysis for files inside repo's
// module, when there is a repo at all (a patch reviewed outside any git
// working tree has none, and Flow degrades to file-only ordering without
// either). A failure in either is not fatal: Flow treats a nil Go or
// Gherkin input as "not available", so it is simply left unset.
func buildAnalysisInputs(ctx context.Context, files []model.ChangedFile, repo *git.Repo) order.Inputs {
	var in order.Inputs
	if repo == nil {
		return in
	}

	var goFiles []string
	for _, f := range files {
		if strings.HasSuffix(f.Path(), ".go") {
			goFiles = append(goFiles, f.Path())
		}
	}
	if len(goFiles) > 0 {
		l := &golang.Loader{Dir: repo.Root()}
		if res, err := l.Load(ctx, goFiles); err == nil {
			in.Go = res
		}
		if cfg, err := golang.LoadConfig(repo.Root()); err == nil {
			in.GoConfig = cfg
		}
	}

	if ix, err := gherkin.Build(ctx, repo.Root()); err == nil {
		in.Gherkin = ix
	}

	return in
}

// handleOrder serves GET /api/order?kind=flow|alpha|dir|diff. Only "flow"
// needs the background analysis above; the other Kinds answer immediately
// from the diff alone, but still trigger it (if not already running) so a
// later "flow" request on the same Workspace does not start from scratch.
func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace()
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = order.KindFlow
	}

	st := orderAnalysisFor(ws)
	if kind == order.KindFlow && !st.ready {
		writeJSON(w, http.StatusOK, map[string]bool{"pending": true})
		return
	}

	_, files, _ := ws.Snapshot()
	in := st.in
	in.Files = files

	o, err := order.Build(kind, in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}
