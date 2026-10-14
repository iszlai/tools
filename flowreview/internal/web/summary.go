// FR-28/FR-29: POST /api/summaries (or GET with ?ids=) answers from the
// summary.Pipeline's cache and queues whatever is missing; POST
// /api/summaries/regenerate forces a fresh run for one hunk. Kept in its own
// file for the same reason expand.go is (see that file's header comment):
// it never touches handlers.go, so it cannot collide with concurrent FR-7/
// FR-9 work there.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/llm/summary"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// summariesRequest is POST /api/summaries's body: the hunk ids the client
// currently wants, in the order it wants them served (its viewport order --
// see summary.Pipeline.Request's doc comment on how that becomes priority).
type summariesRequest struct {
	HunkIDs []string `json:"hunk_ids"`
}

// summariesResponse is both endpoints' response shape: every hunk id from the
// request lands in exactly one of Ready or Pending (or neither, if the id
// did not resolve to a hunk in the current diff at all).
type summariesResponse struct {
	Ready   map[string]model.SnippetSummary `json:"ready"`
	Pending []string                        `json:"pending"`
}

// handleSummaries serves POST /api/summaries {hunk_ids: [...]} and
// GET /api/summaries?ids=a,b,c identically, so a client can either poll with
// a plain GET or send a fresh batch with POST. No summaries.Pipeline wired up
// (summaries off, or no model and fallback was not wanted at all -- today
// Pipeline is always set once a Server is constructed by main.go, but tests
// and other callers of web.New are not required to call SetSummaries)
// answers every id as Pending forever rather than erroring: the UI already
// treats "still pending" as "keep the shimmer," which is the right look for
// "no pipeline at all" too.
func (s *Server) handleSummaries(w http.ResponseWriter, r *http.Request) {
	ids, err := summaryIDsFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pipeline := s.summariesPipeline()
	if pipeline == nil {
		writeJSON(w, http.StatusOK, summariesResponse{Ready: map[string]model.SnippetSummary{}, Pending: ids})
		return
	}

	items := s.buildSummaryItems(r.Context(), ids)
	result := pipeline.Request(items)
	s.maybePrefetch()
	if result.Ready == nil {
		result.Ready = map[string]model.SnippetSummary{}
	}
	writeJSON(w, http.StatusOK, summariesResponse{Ready: result.Ready, Pending: result.Pending})
}

// handleRegenerateSummary serves POST /api/summaries/regenerate
// {hunk_id: "..."}.
func (s *Server) handleRegenerateSummary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HunkID string `json:"hunk_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HunkID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("hunk_id is required"))
		return
	}

	pipeline := s.summariesPipeline()
	if pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("summaries are not enabled"))
		return
	}

	items := s.buildSummaryItems(r.Context(), []string{req.HunkID})
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("hunk %q not found", req.HunkID))
		return
	}
	pipeline.Regenerate(items[0])
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// summaryIDsFromRequest reads hunk ids from a POST body or a GET's ?ids=
// query parameter.
func summaryIDsFromRequest(r *http.Request) ([]string, error) {
	if r.Method == http.MethodGet {
		q := r.URL.Query().Get("ids")
		if q == "" {
			return nil, nil
		}
		return strings.Split(q, ","), nil
	}
	var req summariesRequest
	if r.Body == nil {
		return nil, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	return req.HunkIDs, nil
}

// buildSummaryItems resolves each hunk id to a summary.Item: its Input (file
// path, language, hunk body and best-effort enclosing function) and a
// Priority equal to its position in ids, so the order the client already
// lists its visible hunks in (top of viewport to bottom) becomes "visible
// hunks first, in flow/file order" without the server needing to recompute
// flow order itself. An id that does not resolve to any hunk in the current
// diff is silently skipped -- a client racing a file switch against an
// in-flight summary request should not have the whole batch fail over one
// stale id.
func (s *Server) buildSummaryItems(ctx context.Context, ids []string) []summary.Item {
	sess, files, repo := s.workspace().Snapshot()
	items := make([]summary.Item, 0, len(ids))
	for i, id := range ids {
		file, hunk, ok := findHunkByID(files, id)
		if !ok {
			continue
		}
		in := summary.Input{
			Path:     file.Path(),
			Language: lang.Detect(file.Path()),
			Hunk:     model.HunkBody(hunk.Lines),
		}
		in.EnclosingFunction = enclosingFunctionBestEffort(ctx, repo, sess, file, hunk)
		items = append(items, summary.Item{HunkID: id, Priority: i, Input: in})
	}
	return items
}

// findHunkByID searches every file's hunks for id, since the summaries
// endpoints take bare hunk ids with no file id alongside them (a hunk id
// already determines its file uniquely -- see model.HunkID).
func findHunkByID(files []model.ChangedFile, id string) (model.ChangedFile, model.DiffHunk, bool) {
	for _, f := range files {
		for _, h := range f.Hunks {
			if h.ID == id {
				return f, h, true
			}
		}
	}
	return model.ChangedFile{}, model.DiffHunk{}, false
}

// enclosingFunctionBestEffort resolves hunk's enclosing function text,
// preferring the new side of the diff (the "after" state a summary should
// describe) and falling back to the old side for a deleted file, or when no
// repository is available at all. Any error, or no repository, just means an
// empty EnclosingFunction -- prompt.go's Input already treats that as fine.
func enclosingFunctionBestEffort(ctx context.Context, repo *git.Repo, sess *model.ReviewSession, file model.ChangedFile, hunk model.DiffHunk) string {
	if repo == nil {
		return ""
	}
	oldSrc, newSrc, err := expandContent(ctx, repo, sess, file)
	if err != nil {
		return ""
	}
	src, line := newSrc, hunk.NewStart
	if file.Status == model.FileDeleted || src == nil {
		src, line = oldSrc, hunk.OldStart
	}
	if src == nil {
		return ""
	}
	return summary.EnclosingFunction(ctx, file.Path(), src, line)
}
