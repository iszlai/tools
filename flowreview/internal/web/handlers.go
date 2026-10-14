package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// SessionView is what GET /api/session returns: the session's own metadata
// plus derived progress counts the UI would otherwise have to compute itself
// from the file list, every hunk's review status (FR-7, so the diff pane can
// dim reviewed hunks without a second round trip) and every comment draft on
// the session (FR-9, so a reload can redraw them without a dedicated list
// endpoint).
type SessionView struct {
	ID         string                      `json:"id"`
	RepoKey    string                      `json:"repo_key"`
	Title      string                      `json:"title"`
	Source     model.Source                `json:"source"`
	Progress   Progress                    `json:"progress"`
	HunkStates map[string]model.HunkStatus `json:"hunk_states,omitempty"`
	Comments   []model.CommentDraft        `json:"comments,omitempty"`
}

// Progress summarizes how much of a session's diff has been reviewed.
type Progress struct {
	TotalFiles    int `json:"total_files"`
	ReviewedFiles int `json:"reviewed_files"` // every hunk in the file is reviewed
	TotalHunks    int `json:"total_hunks"`
	ReviewedHunks int `json:"reviewed_hunks"`
}

// FileView is one entry of GET /api/files: enough to render the file queue
// without fetching every file's rows.
type FileView struct {
	ID        string           `json:"id"`
	Path      string           `json:"path"`
	OldPath   string           `json:"old_path,omitempty"`
	Status    model.FileStatus `json:"status"`
	Language  string           `json:"language,omitempty"`
	Additions int              `json:"additions"`
	Deletions int              `json:"deletions"`
	Hunks     int              `json:"hunks"`
	Reviewed  int              `json:"reviewed"`
}

// unifiedRow is GET /api/file/{id}/rows's pre-FR-6 shape, kept behind
// ?layout=unified: a flat, non-highlighted view of a file's diff, one row
// per hunk header or diff line. Kind is "header" for a hunk's "@@ ... @@"
// line, otherwise one of model.LineKind's values ("context", "add", "del").
type unifiedRow struct {
	Kind     string `json:"kind"`
	OldNo    int    `json:"old_no,omitempty"`
	NewNo    int    `json:"new_no,omitempty"`
	Text     string `json:"text"`
	HunkID   string `json:"hunk_id,omitempty"`
	Position int    `json:"position,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Every API response reflects live review state (progress, hunk states,
	// comments); without this, a browser can serve a GET like /api/session
	// out of its HTTP cache instead of hitting the handler, showing reviewed
	// state from before the most recent change (FR-7 surfaced this: a page
	// reload right after marking a hunk could read a stale cached response).
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// statusFor maps a session.Store error to an HTTP status: the store reports
// "not found" conditions as plain errors with that phrase in them (see
// mutate.go's UpsertComment), so a bad id reads as 404 rather than 500.
func statusFor(err error) int {
	if strings.Contains(err.Error(), "not found") {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleChromaCSS serves the syntax-highlighting stylesheet render.Row's
// old_html/new_html classes refer to: light rules plus a prefers-color-scheme
// dark variant (see render.Stylesheet).
func (s *Server) handleChromaCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(render.Stylesheet())
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, files, _ := s.workspace().Snapshot()
	writeJSON(w, http.StatusOK, buildSessionView(sess, files))
}

func buildSessionView(sess *model.ReviewSession, files []model.ChangedFile) SessionView {
	return SessionView{
		ID:         sess.ID,
		RepoKey:    sess.RepoKey,
		Title:      sess.Title,
		Source:     sess.Source,
		Progress:   buildProgress(sess, files),
		HunkStates: buildHunkStates(sess),
		Comments:   sess.Comments,
	}
}

// buildHunkStates projects sess.States down to just the status every hunk is
// in, by hunk id -- the UI needs this to dim reviewed hunks and to drive the
// current-hunk toggle, but has no business seeing the stored body or
// timestamps the full ReviewState carries.
func buildHunkStates(sess *model.ReviewSession) map[string]model.HunkStatus {
	if len(sess.States) == 0 {
		return nil
	}
	out := make(map[string]model.HunkStatus, len(sess.States))
	for id, st := range sess.States {
		out[id] = st.Status
	}
	return out
}

func buildProgress(sess *model.ReviewSession, files []model.ChangedFile) Progress {
	var p Progress
	p.TotalFiles = len(files)
	for _, f := range files {
		fileDone := len(f.Hunks) > 0
		for _, h := range f.Hunks {
			p.TotalHunks++
			if reviewedCount(sess, h.ID) {
				p.ReviewedHunks++
			} else {
				fileDone = false
			}
		}
		if fileDone {
			p.ReviewedFiles++
		}
	}
	return p
}

func reviewedCount(sess *model.ReviewSession, hunkID string) bool {
	st, ok := sess.States[hunkID]
	return ok && st.Status == model.HunkReviewed
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	sess, files, _ := s.workspace().Snapshot()
	out := make([]FileView, 0, len(files))
	for _, f := range files {
		reviewed := 0
		for _, h := range f.Hunks {
			if reviewedCount(sess, h.ID) {
				reviewed++
			}
		}
		out = append(out, FileView{
			ID:        f.ID,
			Path:      f.Path(),
			OldPath:   f.OldPath,
			Status:    f.Status,
			Language:  f.Language,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Hunks:     len(f.Hunks),
			Reviewed:  reviewed,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFileRows renders a file's diff as side-by-side rows (render.Row),
// cached per file id and content so switching back to an already-rendered
// file is near-instant. ?layout=unified returns the flat pre-FR-6 shape
// instead, for anything that still wants one row per diff line.
func (s *Server) handleFileRows(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, files, _ := s.workspace().Snapshot()
	for _, f := range files {
		if f.ID != id {
			continue
		}
		if r.URL.Query().Get("layout") == "unified" {
			writeJSON(w, http.StatusOK, buildUnifiedRows(f))
			return
		}
		writeJSON(w, http.StatusOK, s.renderCache.Rows(f))
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("file %q not found", id))
}

func buildUnifiedRows(f model.ChangedFile) []unifiedRow {
	rows := make([]unifiedRow, 0, len(f.Hunks)*4)
	for _, h := range f.Hunks {
		rows = append(rows, unifiedRow{Kind: "header", Text: hunkHeaderText(h), HunkID: h.ID})
		for _, l := range h.Lines {
			rows = append(rows, unifiedRow{
				Kind:     string(l.Kind),
				OldNo:    l.OldNo,
				NewNo:    l.NewNo,
				Text:     l.Text,
				HunkID:   h.ID,
				Position: l.Position,
			})
		}
	}
	return rows
}

// hunkHeaderText renders a hunk's "@@ ... @@" line for the unified layout's
// "header" row. It need not be byte-exact with what git produced
// (diff.Parse/Emit own that).
func hunkHeaderText(h model.DiffHunk) string {
	text := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		text += " " + h.Section
	}
	return text
}

func findHunk(files []model.ChangedFile, hunkID string) (model.DiffHunk, string, bool) {
	for _, f := range files {
		for _, h := range f.Hunks {
			if h.ID == hunkID {
				return h, f.Path(), true
			}
		}
	}
	return model.DiffHunk{}, "", false
}

// headSHA is what a MarkReviewed call stores as the head the hunk was
// reviewed against: the source's own HeadSHA when it names one (a commit,
// range or PR), otherwise the repo's current HEAD for a worktree review.
// repo may be nil (a patch reviewed outside any git working tree).
func headSHA(ctx context.Context, sess *model.ReviewSession, repo *git.Repo) string {
	if sess.Source.HeadSHA != "" {
		return sess.Source.HeadSHA
	}
	if repo == nil {
		return ""
	}
	sha, err := repo.HeadSHA(ctx)
	if err != nil {
		return ""
	}
	return sha
}

type stateRequest struct {
	HunkID   string `json:"hunk_id"`
	Reviewed bool   `json:"reviewed"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	var req stateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.HunkID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("hunk_id is required"))
		return
	}

	ws := s.workspace()
	sess, files, repo := ws.Snapshot()
	hunk, path, found := findHunk(files, req.HunkID)
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("hunk %q not found", req.HunkID))
		return
	}

	var err error
	if req.Reviewed {
		err = s.store.MarkReviewed(sess.RepoKey, sess.ID, hunk, path, headSHA(r.Context(), sess, repo))
	} else {
		err = s.store.Unmark(sess.RepoKey, sess.ID, req.HunkID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := ws.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	sess, files, _ = ws.Snapshot()
	writeJSON(w, http.StatusOK, buildSessionView(sess, files))
}

type commentRequest struct {
	ID         string        `json:"id,omitempty"`
	Anchor     *model.Anchor `json:"anchor,omitempty"`
	Body       string        `json:"body"`
	AIAssisted bool          `json:"ai_assisted,omitempty"`
}

func (s *Server) handleUpsertComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("body is required"))
		return
	}

	ws := s.workspace()
	sess, _, _ := ws.Snapshot()
	draft := model.CommentDraft{
		ID:         req.ID,
		Anchor:     req.Anchor,
		Body:       req.Body,
		Status:     model.CommentDraftStatus,
		AIAssisted: req.AIAssisted,
		// A comment anchored to a row with no diff position (e.g. a header
		// row) cannot be published to a host; FR-8's expanded context rows
		// will need the same rule once they too carry no position.
		Publishable: req.Anchor == nil || req.Anchor.Position > 0,
	}

	saved, err := s.store.UpsertComment(sess.RepoKey, sess.ID, draft)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := ws.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws := s.workspace()
	sess, _, _ := ws.Snapshot()
	if err := s.store.DeleteComment(sess.RepoKey, sess.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := ws.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type openRequest struct {
	Source model.Source `json:"source"`
}

// handleOpen is what a second `flowreview` invocation calls on the running
// instance: swap the reviewed Workspace for src and bring the window forward.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	ws, err := review.Open(r.Context(), s.store, req.Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.mu.Lock()
	s.ws = ws
	window := s.window
	s.mu.Unlock()

	window.Navigate(s.URL())
	window.Focus()

	writeJSON(w, http.StatusOK, map[string]string{"session_id": ws.Session.ID})
}
