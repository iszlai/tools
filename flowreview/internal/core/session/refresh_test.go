package session

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func reviewedState(hunkID, path string) *model.ReviewState {
	return &model.ReviewState{HunkID: hunkID, Path: path, Status: model.HunkReviewed}
}

func TestRefreshMarksVanishedHunkStale(t *testing.T) {
	sess := &model.ReviewSession{
		States: map[string]*model.ReviewState{
			"h-a": reviewedState("h-a", "a.go"),
		},
	}
	// a.go no longer appears in the diff at all.
	report := Refresh(sess, nil)

	if sess.States["h-a"].Status != model.HunkStale {
		t.Errorf("want stale, got %s", sess.States["h-a"].Status)
	}
	if len(report.Stale) != 1 || report.Stale[0] != "h-a" {
		t.Errorf("want Stale=[h-a], got %v", report.Stale)
	}
	if len(report.NeedsReReview) != 0 {
		t.Errorf("want no needs-re-review, got %v", report.NeedsReReview)
	}
}

func TestRefreshMarksOverlappingHunkNeedsReReview(t *testing.T) {
	sess := &model.ReviewSession{
		States: map[string]*model.ReviewState{
			"h-old": reviewedState("h-old", "a.go"),
		},
	}
	// a.go is still in the diff, but with a different hunk id (content moved
	// or changed) -- the old reviewed mark is not simply gone, it needs a
	// second look.
	files := []model.ChangedFile{
		{NewPath: "a.go", Status: model.FileModified, Hunks: []model.DiffHunk{{ID: "h-new"}}},
	}
	report := Refresh(sess, files)

	if sess.States["h-old"].Status != model.HunkNeedsReReview {
		t.Errorf("want needs-re-review, got %s", sess.States["h-old"].Status)
	}
	if len(report.NeedsReReview) != 1 || report.NeedsReReview[0] != "h-old" {
		t.Errorf("want NeedsReReview=[h-old], got %v", report.NeedsReReview)
	}
	if len(report.Stale) != 0 {
		t.Errorf("want no stale, got %v", report.Stale)
	}
}

func TestRefreshKeepsPresentHunkUntouched(t *testing.T) {
	sess := &model.ReviewSession{
		States: map[string]*model.ReviewState{
			"h-a": reviewedState("h-a", "a.go"),
		},
	}
	files := []model.ChangedFile{
		{NewPath: "a.go", Status: model.FileModified, Hunks: []model.DiffHunk{{ID: "h-a"}}},
	}
	report := Refresh(sess, files)

	if sess.States["h-a"].Status != model.HunkReviewed {
		t.Errorf("a present hunk's status must not change, got %s", sess.States["h-a"].Status)
	}
	if len(report.Stale) != 0 || len(report.NeedsReReview) != 0 {
		t.Errorf("want an empty report, got %+v", report)
	}
}

func TestRefreshNeverDeletesState(t *testing.T) {
	sess := &model.ReviewSession{
		States: map[string]*model.ReviewState{
			"h-a": reviewedState("h-a", "a.go"),
		},
	}
	Refresh(sess, nil)
	if _, ok := sess.States["h-a"]; !ok {
		t.Fatal("Refresh must never delete a state, only change its status")
	}
}

func TestRefreshIsIdempotent(t *testing.T) {
	sess := &model.ReviewSession{
		States: map[string]*model.ReviewState{
			"h-a": reviewedState("h-a", "a.go"),
		},
	}
	Refresh(sess, nil)           // first pass: h-a becomes stale
	report := Refresh(sess, nil) // second pass: already stale, nothing new
	if len(report.Stale) != 0 {
		t.Errorf("a second Refresh must not re-report an already-stale hunk, got %v", report.Stale)
	}
}

func TestRefreshMarksCommentStaleWhenAnchorHunkVanishes(t *testing.T) {
	sess := &model.ReviewSession{
		Comments: []model.CommentDraft{
			{
				ID:     "c1",
				Status: model.CommentDraftStatus,
				Anchor: &model.Anchor{Path: "a.go", HunkID: "h-a"},
			},
			{
				ID:     "c2",
				Status: model.CommentDraftStatus,
				// General comment, no anchor: must never be touched.
			},
		},
	}
	// a.go's hunk is gone entirely from the new diff.
	report := Refresh(sess, nil)

	if sess.Comments[0].Status != model.CommentStale {
		t.Errorf("want c1 stale, got %s", sess.Comments[0].Status)
	}
	if sess.Comments[1].Status != model.CommentDraftStatus {
		t.Errorf("a general comment must be untouched, got %s", sess.Comments[1].Status)
	}
	if len(report.StaleComments) != 1 || report.StaleComments[0] != "c1" {
		t.Errorf("want StaleComments=[c1], got %v", report.StaleComments)
	}
}

func TestRefreshKeepsCommentWhenAnchorHunkPresent(t *testing.T) {
	sess := &model.ReviewSession{
		Comments: []model.CommentDraft{
			{ID: "c1", Status: model.CommentDraftStatus, Anchor: &model.Anchor{Path: "a.go", HunkID: "h-a"}},
		},
	}
	files := []model.ChangedFile{
		{NewPath: "a.go", Status: model.FileModified, Hunks: []model.DiffHunk{{ID: "h-a"}}},
	}
	report := Refresh(sess, files)
	if sess.Comments[0].Status != model.CommentDraftStatus {
		t.Errorf("want unchanged, got %s", sess.Comments[0].Status)
	}
	if len(report.StaleComments) != 0 {
		t.Errorf("want no stale comments, got %v", report.StaleComments)
	}
}
