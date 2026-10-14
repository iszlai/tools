// FR-8: GET /api/file/{id}/expand splices a hunk's enclosing function into
// its side-by-side rows. Kept in its own file (handlers.go already has a
// FR-8 forward-reference in handleUpsertComment's Publishable comment) so it
// never collides with FR-7/FR-9's changes to the same handlers.
package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/expand"
	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// handleExpandHunk is GET /api/file/{id}/expand?hunk=<hunk_id>: it finds
// hunk's enclosing function (or method, or class member) on each side of
// the diff and returns the full side-by-side row list for that region --
// the hunk's own, already-rendered rows (same HunkID, positions and HTML as
// GET /api/file/{id}/rows, so the UI's existing anchors splice back in
// unchanged) with render.ContextRows's unchanged-line context spliced in
// before and after, for whatever expand.Align finds between the hunk and
// the ends of the enclosing declaration.
func (s *Server) handleExpandHunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hunkID := r.URL.Query().Get("hunk")
	if hunkID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("hunk is required"))
		return
	}

	sess, files, repo := s.workspace().Snapshot()
	file, ok := fileByID(files, id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("file %q not found", id))
		return
	}
	hunk, ok := hunkByID(file.Hunks, hunkID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("hunk %q not found", hunkID))
		return
	}

	oldSrc, newSrc, err := expandContent(r.Context(), repo, sess, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	hr, err := expand.ForHunk(r.Context(), file.Path(), hunk, oldSrc, newSrc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	before, after := expand.Align(hunk, file.Hunks, hr.OldRange, hr.NewRange)

	rows := make([]render.Row, 0, len(before)+len(after)+4)
	rows = append(rows, render.ContextRows(file.Path(), hunk.ID, oldSrc, newSrc, oldLinesOf(before), newLinesOf(before))...)
	rows = append(rows, hunkOwnRows(s.renderCache.Rows(file), hunk.ID)...)
	rows = append(rows, render.ContextRows(file.Path(), hunk.ID, oldSrc, newSrc, oldLinesOf(after), newLinesOf(after))...)

	writeJSON(w, http.StatusOK, rows)
}

func fileByID(files []model.ChangedFile, id string) (model.ChangedFile, bool) {
	for _, f := range files {
		if f.ID == id {
			return f, true
		}
	}
	return model.ChangedFile{}, false
}

func hunkByID(hunks []model.DiffHunk, id string) (model.DiffHunk, bool) {
	for _, h := range hunks {
		if h.ID == id {
			return h, true
		}
	}
	return model.DiffHunk{}, false
}

// hunkOwnRows picks hunkID's own rows -- its header row plus every line row
// -- out of a file's full, already-rendered row list, unchanged: FR-8 never
// recomputes a hunk's own rows, only splices context around them, so their
// HunkID, positions and HTML are exactly what GET /api/file/{id}/rows
// already served (and, in particular, exactly what the UI has already drawn
// data-* attributes from).
func hunkOwnRows(all []render.Row, hunkID string) []render.Row {
	out := make([]render.Row, 0, 4)
	for _, row := range all {
		if row.HunkID == hunkID {
			out = append(out, row)
		}
	}
	return out
}

func oldLinesOf(ctx []expand.Context) []int {
	out := make([]int, len(ctx))
	for i, c := range ctx {
		out[i] = c.OldLine
	}
	return out
}

func newLinesOf(ctx []expand.Context) []int {
	out := make([]int, len(ctx))
	for i, c := range ctx {
		out[i] = c.NewLine
	}
	return out
}

// expandContent resolves file's full content on both sides of sess's diff,
// for expand.ForHunk. The old side always comes from git (there is no
// uncommitted "old" to read from disk); the new side is the live worktree
// file for a model.SourceWorktree review, and git content for every other
// kind (a historical commit, a range's head, or a PR's fetched head). A side
// is left nil, not an error, exactly when the file does not exist there
// (FileAdded has no old side, FileDeleted has no new side) or when the
// repository itself doesn't have the old revision (a root commit has no
// parent) -- expand.ForHunk treats a nil side as "nothing to expand into,"
// which is the correct, honest answer in both cases.
func expandContent(ctx context.Context, repo *git.Repo, sess *model.ReviewSession, file model.ChangedFile) (oldSrc, newSrc []byte, err error) {
	if repo == nil {
		return nil, nil, fmt.Errorf("expand: %q has no repository to read from", file.Path())
	}
	oldRev, newRev, newIsWorktree, ok := expandRevisions(ctx, repo, sess.Source)
	if !ok {
		return nil, nil, fmt.Errorf("expand: cannot resolve revisions for a %q source", sess.Source.Kind)
	}

	if file.Status != model.FileAdded {
		if b, showErr := repo.Show(ctx, oldRev, file.OldPath); showErr == nil {
			oldSrc = b
		}
	}
	if file.Status != model.FileDeleted {
		if newIsWorktree {
			b, readErr := os.ReadFile(filepath.Join(repo.Root(), file.Path()))
			if readErr != nil {
				return nil, nil, fmt.Errorf("expand: read %q: %w", file.Path(), readErr)
			}
			newSrc = b
		} else {
			b, showErr := repo.Show(ctx, newRev, file.Path())
			if showErr != nil {
				return nil, nil, fmt.Errorf("expand: read %q at %s: %w", file.Path(), newRev, showErr)
			}
			newSrc = b
		}
	}
	return oldSrc, newSrc, nil
}

// expandRevisions resolves src's old and new git revisions for
// expandContent. newIsWorktree is true exactly for a worktree review, where
// the new side is uncommitted and must be read from disk instead of git
// Show. ok is false when src's Kind carries no revision this function knows
// how to resolve (a patch file has neither a repo-relative base nor a
// fetched head) -- the caller treats that the same as "no repository,"
// since expansion needs a revision to read either side from either way.
func expandRevisions(ctx context.Context, repo *git.Repo, src model.Source) (oldRev, newRev string, newIsWorktree, ok bool) {
	switch src.Kind {
	case model.SourceWorktree:
		return "HEAD", "", true, true
	case model.SourceCommit:
		// git understands "<rev>^" as the first parent directly; a root
		// commit has none, which Show simply fails to resolve -- handled by
		// expandContent treating a failed old-side Show as "no old side."
		return src.Ref + "^", src.Ref, false, true
	case model.SourceRange:
		left, right, mergeBase := splitRangeRef(src.Ref)
		if left == "" || right == "" {
			return "", "", false, false
		}
		if !mergeBase {
			return left, right, false, true
		}
		base, err := repo.MergeBase(ctx, left, right)
		if err != nil {
			return "", "", false, false
		}
		return base, right, false, true
	case model.SourcePR:
		if src.BaseSHA == "" || src.HeadSHA == "" {
			return "", "", false, false
		}
		return src.BaseSHA, src.HeadSHA, false, true
	default:
		return "", "", false, false
	}
}

// splitRangeRef splits a model.SourceRange's Ref ("A..B" or "A...B") into
// its two endpoints, matching how `git diff` itself parses the two forms:
// ".." diffs the endpoints directly; "..." diffs the right endpoint against
// the merge-base of the two (mergeBase reports which this was). An empty
// result means ref had neither form, which should not happen for a
// well-formed SourceRange.
func splitRangeRef(ref string) (left, right string, mergeBase bool) {
	if i := strings.Index(ref, "..."); i >= 0 {
		return ref[:i], ref[i+3:], true
	}
	if i := strings.Index(ref, ".."); i >= 0 {
		return ref[:i], ref[i+2:], false
	}
	return "", "", false
}
