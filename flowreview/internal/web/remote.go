package web

import (
	"net/http"

	"github.com/iszlai/tools/flowreview/internal/host"
)

// handleRemoteComments is GET /api/remote-comments (FR-12): the comments
// already on the PR this session reviews, as fetched once when the PR was
// opened (internal/core/review.Workspace.Remote). Always a JSON array, empty
// rather than null, and empty (not an error) for any non-PR session.
func (s *Server) handleRemoteComments(w http.ResponseWriter, r *http.Request) {
	remote := s.workspace().RemoteComments()
	if remote == nil {
		remote = []host.RemoteComment{}
	}
	writeJSON(w, http.StatusOK, remote)
}
