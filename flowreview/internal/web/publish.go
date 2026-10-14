package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/host"
)

// publishRequest is the body of both FR-11 publish endpoints: draft_ids
// scopes which of the session's own comment drafts to consider (nil/empty
// means every eligible one); event and body only matter for POST
// /api/publish, where they become the review's event and top-level body.
type publishRequest struct {
	Event    string   `json:"event,omitempty"`
	Body     string   `json:"body,omitempty"`
	DraftIDs []string `json:"draft_ids,omitempty"`
}

// statusForPublishErr maps review.Preflight/Publish's sentinel error to a
// client-correctable 400; anything else (host not wired, a store failure) is
// a 500.
func statusForPublishErr(err error) int {
	if errors.Is(err, review.ErrNotAPRSession) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// handlePublishPreflight is POST /api/publish/preflight (FR-11): a dry run
// that re-fetches the PR's current state and reports what a publish would
// send -- inline, general and excluded drafts -- without sending anything.
func (s *Server) handlePublishPreflight(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	plan, err := s.workspace().Preflight(r.Context(), req.DraftIDs)
	if err != nil {
		writeError(w, statusForPublishErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handlePublish is POST /api/publish (FR-11): preflight, then submit. Local
// (non-PR) sessions get a 400 with a message naming exactly why, per the
// slice's own accept criteria.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ev := host.ReviewEvent(req.Event)
	switch ev {
	case host.EventComment, host.EventApprove, host.EventRequestChanges:
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("event must be one of COMMENT, APPROVE, REQUEST_CHANGES, got %q", req.Event))
		return
	}

	res, err := s.workspace().Publish(r.Context(), ev, req.Body, req.DraftIDs)
	if err != nil {
		writeError(w, statusForPublishErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
