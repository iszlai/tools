// FR-19's web exposure: GET /api/radius. A go/packages Load plus the go/ssa
// call graph build Compute needs (internal/core/radius) are too slow to do
// inline in a request handler, so the first request for a given head starts
// the computation in the background and every request returns immediately:
// {"pending": true} until it finishes, the report after. This cache is
// deliberately small and self-contained (see radiusCache's doc comment); if
// FR-17 gives review.Workspace a shared background-analysis mechanism before
// this merges, the coordinator will fold the two together.
package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/radius"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// radiusResult is one workspace snapshot's computed (or in-flight) blast
// radius.
type radiusResult struct {
	done   chan struct{}
	report radius.Report
	err    error
}

// radiusCache holds one radiusResult per (session, head) pair ever requested
// in this process's lifetime. It is a plain package-level cache, not
// attached to review.Workspace, which has no analysis-cache hook yet — kept
// this way on purpose, see this file's package doc. It is never evicted: a
// long-running flowreview process accumulates one entry per head a session
// is opened against, which in practice is small (a handful of heads across
// one review sitting).
var (
	radiusMu    sync.Mutex
	radiusCache = map[string]*radiusResult{}
)

type radiusResponse struct {
	Pending bool           `json:"pending"`
	Error   string         `json:"error,omitempty"`
	Report  *radius.Report `json:"report,omitempty"`
}

// handleRadius serves GET /api/radius: FR-19's blast radius for the current
// workspace's diff.
func (s *Server) handleRadius(w http.ResponseWriter, r *http.Request) {
	sess, files, repo := s.workspace().Snapshot()
	if repo == nil {
		// A patch reviewed outside any git working tree: there is no old
		// revision to read a pre-diff file from, so there is nothing to
		// compute — not pending, just empty.
		writeJSON(w, http.StatusOK, radiusResponse{Report: &radius.Report{}})
		return
	}

	rev, ok := oldRevision(r.Context(), repo, sess.Source)
	if !ok {
		writeJSON(w, http.StatusOK, radiusResponse{Error: "radius: old revision not resolvable for this source"})
		return
	}

	key := sess.ID + "@" + headSHA(r.Context(), sess, repo)
	radiusMu.Lock()
	res, exists := radiusCache[key]
	if !exists {
		res = &radiusResult{done: make(chan struct{})}
		radiusCache[key] = res
		go computeRadius(res, repo, rev, files)
	}
	radiusMu.Unlock()

	select {
	case <-res.done:
		if res.err != nil {
			writeJSON(w, http.StatusOK, radiusResponse{Error: res.err.Error()})
			return
		}
		report := res.report
		writeJSON(w, http.StatusOK, radiusResponse{Report: &report})
	default:
		writeJSON(w, http.StatusOK, radiusResponse{Pending: true})
	}
}

// computeRadius runs on its own background context — never the request's,
// which is cancelled the moment handleRadius returns, long before this is
// likely to finish.
func computeRadius(res *radiusResult, repo *git.Repo, rev string, files []model.ChangedFile) {
	defer close(res.done)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	changed := changedGoFiles(files)
	if len(changed) == 0 {
		return // nothing for golang.Loader to load; an empty report, not an error
	}

	l := &golang.Loader{Dir: repo.Root()}
	loaded, err := l.Load(ctx, changed)
	if err != nil {
		res.err = err
		return
	}

	old := func(path string) ([]byte, error) { return repo.Show(ctx, rev, path) }
	report, err := radius.Compute(loaded, files, old)
	if err != nil {
		res.err = err
		return
	}
	res.report = report
}

func changedGoFiles(files []model.ChangedFile) []string {
	var out []string
	for _, f := range files {
		if f.Status != model.FileDeleted && strings.HasSuffix(f.Path(), ".go") {
			out = append(out, f.Path())
		}
	}
	return out
}

// oldRevision picks the git revision Compute should read pre-diff file
// content from, for the source kinds that have one in plain git syntax
// already: a worktree review's old side is HEAD; a commit's is its first
// parent (rev^); a range's is the range's base, honoring "..." as the
// merge-base the way git itself would (see internal/core/git's RangeDiff). A
// patch or PR source needs src.BaseSHA, which nothing resolves yet (see
// model.Source) — those report !ok rather than guess at a revision.
func oldRevision(ctx context.Context, repo *git.Repo, src model.Source) (string, bool) {
	switch src.Kind {
	case model.SourceWorktree:
		return "HEAD", true
	case model.SourceCommit:
		sha, err := repo.ResolveSHA(ctx, src.Ref)
		if err != nil {
			return "", false
		}
		return sha + "^", true
	case model.SourceRange:
		return rangeBase(ctx, repo, src.Ref)
	default:
		return src.BaseSHA, src.BaseSHA != ""
	}
}

func rangeBase(ctx context.Context, repo *git.Repo, rangeExpr string) (string, bool) {
	if a, b, ok := strings.Cut(rangeExpr, "..."); ok {
		mb, err := repo.MergeBase(ctx, a, b)
		if err != nil {
			return "", false
		}
		return mb, true
	}
	if a, _, ok := strings.Cut(rangeExpr, ".."); ok {
		return a, true
	}
	return "", false
}
