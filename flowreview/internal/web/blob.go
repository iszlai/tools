// FR-18's read-only file view: GET /api/blob?path=&rev=new serves a target
// the current diff never rendered a row for, highlighted the same way the
// diff itself is (internal/core/render), plus the same declaration-line list
// GET /api/file/{id}/symbols gives a diff file, so the inline nav button
// keeps working once the user has hopped out of the diff.
package web

import (
	"fmt"
	"net/http"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/lang"
)

// blobView is GET /api/blob's response.
type blobView struct {
	Path      string       `json:"path"`
	Language  string       `json:"language,omitempty"`
	Rows      []render.Row `json:"rows"`
	DeclLines []int        `json:"decl_lines"`
}

// handleBlob serves GET /api/blob?path=&rev=new. Only rev=new is accepted:
// the read-only view only ever shows the session's current head (worktree
// content for a worktree review, the fetched head commit otherwise, exactly
// what readSideContent's "new" side already means) -- see PLAN.md FR-18's
// "the rest of FR-18" scope.
func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	rev := r.URL.Query().Get("rev")
	if rev == "" {
		rev = "new"
	}
	if path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("blob: path is required"))
		return
	}
	if rev != "new" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("blob: only rev=%q is supported", "new"))
		return
	}

	ws := s.workspace()
	sess, _, repo := ws.Snapshot()
	if repo == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("blob: no repository to read %q from", path))
		return
	}
	src, err := readSideContent(r.Context(), repo, sess, path, "new")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, blobView{
		Path:      path,
		Language:  lang.Detect(path),
		Rows:      render.Blob(path, src),
		DeclLines: declLines(r.Context(), repo.Root(), path, src),
	})
}
