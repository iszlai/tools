package session

import (
	"sort"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// RefreshReport lists the hunk and comment ids Refresh changed the status of.
// Nothing is ever deleted by Refresh, so an empty report means every id
// already recorded in the session is still accounted for in files.
type RefreshReport struct {
	Stale         []string
	NeedsReReview []string
	StaleComments []string
}

// Refresh reconciles s's stored state against a fresh diff. A hunk whose id
// is no longer present in files has its state moved to stale -- state is
// never deleted, so review history survives a rebase that only moves lines.
// When the hunk's path still has at least one hunk in the new diff, the state
// becomes needs-re-review instead of stale: something changed at that
// location too, so it deserves a second look rather than starting from
// scratch. Comments anchored to a hunk that disappeared always become stale;
// there is no needs-re-review equivalent for comments.
//
// This is a path-level proxy for "overlapping location", not a line-range
// one: ReviewState does not carry the old hunk's line numbers, so the exact
// overlap FR-22 (delta re-review) describes is left to that slice. Keeping it
// to "same path" here is deliberate simplicity, not an oversight.
func Refresh(s *model.ReviewSession, files []model.ChangedFile) RefreshReport {
	present := map[string]bool{}
	pathHasHunks := map[string]bool{}
	for _, f := range files {
		p := f.Path()
		for _, h := range f.Hunks {
			present[h.ID] = true
			pathHasHunks[p] = true
		}
	}

	var report RefreshReport
	for hunkID, st := range s.States {
		if present[hunkID] {
			continue
		}
		if pathHasHunks[st.Path] {
			if st.Status != model.HunkNeedsReReview {
				st.Status = model.HunkNeedsReReview
				report.NeedsReReview = append(report.NeedsReReview, hunkID)
			}
		} else if st.Status != model.HunkStale {
			st.Status = model.HunkStale
			report.Stale = append(report.Stale, hunkID)
		}
	}

	for i := range s.Comments {
		c := &s.Comments[i]
		if c.Anchor == nil || c.Status == model.CommentStale || present[c.Anchor.HunkID] {
			continue
		}
		c.Status = model.CommentStale
		report.StaleComments = append(report.StaleComments, c.ID)
	}

	sort.Strings(report.Stale)
	sort.Strings(report.NeedsReReview)
	sort.Strings(report.StaleComments)
	return report
}
