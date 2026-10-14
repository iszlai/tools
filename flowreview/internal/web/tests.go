package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/testreach"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// testsState is one Server's FR-21 cache. Compute can take real seconds
// (FR-13's go/packages load, FR-15's SSA/VTA build), so handleTests must
// never block a request on it: the first request for a given diff kicks off
// a background computeTests and every request -- that one included -- gets
// {"pending": true} until it finishes. Keyed by a fingerprint of the
// session's changed files, so a Reload that changes nothing Go-related
// reuses the same result and one that does recomputes.
//
// This is FR-21's own cache; the coordinator will unify it with FR-17's
// order and FR-19's radius once those land, since all three want the same
// "warm golang.Result for this Workspace" story.
type testsState struct {
	mu      sync.Mutex
	key     string
	pending bool
	report  *testreach.Report
	err     error
}

// testsStates maps each live Server to its own testsState, keyed by the
// Server's own pointer identity rather than a new struct field on Server --
// FR-21 only touches web.go for the one route line in routes().
var (
	testsStatesMu sync.Mutex
	testsStates   = map[*Server]*testsState{}
)

func (s *Server) testsStateFor() *testsState {
	testsStatesMu.Lock()
	defer testsStatesMu.Unlock()
	st := testsStates[s]
	if st == nil {
		st = &testsState{}
		testsStates[s] = st
	}
	return st
}

// testsFingerprint identifies one diff's Go-relevant content well enough to
// know when to recompute: every changed .go path plus its hunks' new-side
// line ranges. Two diffs that touch the same lines of the same files always
// get the same Report, so this need not hash file content.
func testsFingerprint(files []model.ChangedFile) string {
	var b strings.Builder
	for _, f := range files {
		p := f.Path()
		if !isGoFile(p) {
			continue
		}
		b.WriteString(p)
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, ":%d,%d", h.NewStart, h.NewLines)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func isGoFile(path string) bool {
	return len(path) > 3 && path[len(path)-3:] == ".go"
}

// handleTests is GET /api/tests (FR-21): which of the review's changed
// functions are reachable from a test. A patch reviewed outside any git
// working tree, or a diff that touches no .go file, has nothing for
// golang.Loader to load, so it resolves immediately to an empty, non-pending
// Report instead of ever going through the cache.
func (s *Server) handleTests(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace()
	_, files, repo := ws.Snapshot()
	if repo == nil || testsFingerprint(files) == "" {
		writeJSON(w, http.StatusOK, testreach.Report{})
		return
	}

	st := s.testsStateFor()
	key := testsFingerprint(files)

	st.mu.Lock()
	if st.key != key {
		st.key = key
		st.pending = true
		st.report = nil
		st.err = nil
		go computeTests(st, key, repo.Root(), files)
	}
	pending, report, err := st.pending, st.report, st.err
	st.mu.Unlock()

	switch {
	case pending:
		writeJSON(w, http.StatusOK, map[string]bool{"pending": true})
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, report)
	}
}

// computeTests runs golang.Loader + testreach.Compute for files and stores
// the result on st, unless key has since been superseded by a newer diff
// (another handleTests call started a fresher computeTests before this one
// finished).
func computeTests(st *testsState, key, moduleDir string, files []model.ChangedFile) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var changedGoFiles []string
	for _, f := range files {
		if p := f.Path(); isGoFile(p) {
			changedGoFiles = append(changedGoFiles, p)
		}
	}

	loader := &golang.Loader{Dir: moduleDir}
	res, err := loader.Load(ctx, changedGoFiles)
	var report testreach.Report
	if err == nil {
		report, err = testreach.Compute(res, files)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.key != key {
		return // a newer diff started its own computeTests before this one finished
	}
	st.pending = false
	st.report = &report
	st.err = err
}
