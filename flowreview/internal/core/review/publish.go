package review

// FR-11: publish a PR session's comment drafts to its host. Preflight and
// Publish live here, not in internal/web, for the same reason Open/Reload do:
// they need the Workspace's session, store and source together, and the web
// handler should be a thin JSON wrapper over them.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// SubmitReview and PublishComment are FR-11's hooks into a PR session's host,
// wired by the CLI the same way FetchPR and FetchRemoteComments are
// (cmd/flowreview/pr.go's init). Left nil until wired.
var SubmitReview func(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error)
var PublishComment func(ctx context.Context, ref model.PRRef, commitID string, c model.CommentDraft) (*host.CommentResult, error)

// ErrNotAPRSession is returned by Preflight and Publish for anything but a PR
// session: publishing is a GitHub concept, and there is nothing to send a
// local diff's comments to.
var ErrNotAPRSession = errors.New("review: publishing needs a PR session")

// errHostNotWired is returned when SubmitReview/PublishComment have not been
// set -- only possible if a caller reaches this package without going
// through the CLI's init (e.g. a test), since cmd/flowreview always wires
// both hooks alongside FetchPR.
var errHostNotWired = errors.New("review: publishing is not wired to a host")

// ExcludedDraft is a comment draft Preflight will not send, and why.
type ExcludedDraft struct {
	DraftID string `json:"draft_id"`
	Reason  string `json:"reason"`
}

// PublishPlan is what Preflight found when it checked the session's own
// comment drafts against the PR's current state: Inline drafts go out as
// anchored review comments, General drafts (no anchor) are folded into the
// review body, and Excluded drafts are left out, with the reason why -- never
// silently resent at a different line.
type PublishPlan struct {
	HeadSHA  string               `json:"head_sha"`
	Inline   []model.CommentDraft `json:"inline"`
	General  []model.CommentDraft `json:"general"`
	Excluded []ExcludedDraft      `json:"excluded"`
}

// Preflight re-fetches the PR's current head and diff (Reload, which runs
// the same FetchPR hook Open does, then session.Refresh) and classifies every
// eligible comment draft against the result: Refresh has just marked stale
// any draft whose hunk_id is no longer present in the fresh diff -- the PR
// moved under it, whether by a new push or a rebase -- and those are
// excluded here, not resent at a guessed new line. A hunk_id survives
// verbatim in a fresh diff only when that hunk's path and content are
// unchanged (model.HunkID hashes both), so this is a stronger check than
// comparing the anchor's stored head_sha against the new one: unrelated
// commits elsewhere in the PR do not invalidate a comment whose own hunk
// never moved.
//
// Eligible means not yet published: a fresh draft, or one that failed a
// previous publish attempt and is being retried. When draftIDs is non-empty,
// only those ids are considered; anything else in the session is left alone.
func (w *Workspace) Preflight(ctx context.Context, draftIDs []string) (*PublishPlan, error) {
	if w.src.Kind != model.SourcePR {
		return nil, ErrNotAPRSession
	}
	if err := w.Reload(ctx); err != nil {
		return nil, fmt.Errorf("review: preflight: %w", err)
	}

	sess, _, _ := w.Snapshot()
	want := draftIDSet(draftIDs)
	// Non-nil, not just non-empty: encoding/json renders a nil slice as JSON
	// null, and the UI (and these JSON fields' own doc comments) expect an
	// always-present array, same as handleRemoteComments does.
	plan := &PublishPlan{
		HeadSHA:  sess.Source.HeadSHA,
		Inline:   []model.CommentDraft{},
		General:  []model.CommentDraft{},
		Excluded: []ExcludedDraft{},
	}
	for _, c := range sess.Comments {
		if !eligibleToPublish(c, want) {
			continue
		}
		switch {
		case c.Anchor == nil:
			plan.General = append(plan.General, c)
		case c.Status == model.CommentStale:
			plan.Excluded = append(plan.Excluded, ExcludedDraft{
				DraftID: c.ID,
				Reason:  "this comment's anchor no longer matches the current diff (the file or hunk changed since it was drafted)",
			})
		case !c.Publishable:
			plan.Excluded = append(plan.Excluded, ExcludedDraft{
				DraftID: c.ID,
				Reason:  "not anchored to a line in the diff (e.g. expanded context), so it cannot be published",
			})
		default:
			plan.Inline = append(plan.Inline, c)
		}
	}
	return plan, nil
}

func eligibleToPublish(c model.CommentDraft, want map[string]bool) bool {
	if c.Status == model.CommentPublished {
		return false
	}
	if want == nil {
		return true
	}
	return want[c.ID]
}

func draftIDSet(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// PublishResult is the outcome of one Publish call: the review GitHub
// recorded (nil for a pure retry, which publishes comments individually with
// no review object -- see isRetry), one CommentResult per draft actually
// attempted, and the drafts Preflight excluded before anything was sent.
type PublishResult struct {
	Review   *host.ReviewResult   `json:"review,omitempty"`
	Results  []host.CommentResult `json:"results"`
	Excluded []ExcludedDraft      `json:"excluded"`
}

// Publish runs Preflight, then sends what it found. A pure retry -- every
// targeted inline draft already failed once and there is nothing general to
// fold into a review body -- is sent comment-by-comment via PublishComment,
// so retrying one straggler does not add another "commented" entry to the
// PR's timeline for drafts that already went out fine the first time.
// Everything else is submitted as one review via SubmitReview, which itself
// falls back to per-comment publishing internally if GitHub 422s the batch
// (see the github package).
//
// Every draft actually attempted is written back to the session store:
// published (with RemoteID/RemoteURL) on success, failed (with Error)
// otherwise. Drafts Preflight excluded are left untouched -- never marked
// published or failed for something that was never sent.
func (w *Workspace) Publish(ctx context.Context, ev host.ReviewEvent, body string, draftIDs []string) (*PublishResult, error) {
	plan, err := w.Preflight(ctx, draftIDs)
	if err != nil {
		return nil, err
	}
	sess, _, _ := w.Snapshot()
	if sess.Source.PR == nil {
		return nil, fmt.Errorf("review: publish: PR session has no PR reference")
	}
	ref := *sess.Source.PR

	out := &PublishResult{Excluded: plan.Excluded, Results: []host.CommentResult{}}
	if isRetry(plan) {
		if PublishComment == nil {
			return nil, errHostNotWired
		}
		for _, c := range plan.Inline {
			cr, err := PublishComment(ctx, ref, plan.HeadSHA, c)
			out.Results = append(out.Results, resultOrFailure(c.ID, cr, err))
		}
	} else {
		if SubmitReview == nil {
			return nil, errHostNotWired
		}
		res, err := SubmitReview(ctx, ref, host.Review{
			Event:    ev,
			Body:     composeReviewBody(body, plan.General),
			CommitID: plan.HeadSHA,
			Comments: plan.Inline,
		})
		if err != nil {
			// A total failure, not a per-comment 422 (SubmitReview already
			// turns that into a mixed-result ReviewResult): everything
			// targeted failed together.
			for _, c := range plan.Inline {
				out.Results = append(out.Results, host.CommentResult{DraftID: c.ID, OK: false, Error: err.Error()})
			}
			for _, c := range plan.General {
				out.Results = append(out.Results, host.CommentResult{DraftID: c.ID, OK: false, Error: err.Error()})
			}
		} else {
			out.Review = res
			out.Results = append(out.Results, res.Comments...)
			for _, c := range plan.General {
				out.Results = append(out.Results, host.CommentResult{DraftID: c.ID, OK: true, HTMLURL: res.HTMLURL})
			}
		}
	}

	if err := w.applyPublishResults(sess.RepoKey, sess.ID, out.Results); err != nil {
		return nil, err
	}
	return out, nil
}

// isRetry reports whether plan is a pure retry of previously-failed inline
// comments: nothing general to fold into a fresh review body, and every
// inline draft already failed once (a never-attempted draft always goes
// through a real review submission, even alongside failed ones, so it gets
// counted properly against the event being submitted).
func isRetry(plan *PublishPlan) bool {
	if len(plan.General) > 0 || len(plan.Inline) == 0 {
		return false
	}
	for _, c := range plan.Inline {
		if c.Status != model.CommentFailed {
			return false
		}
	}
	return true
}

func resultOrFailure(draftID string, cr *host.CommentResult, err error) host.CommentResult {
	if err != nil {
		return host.CommentResult{DraftID: draftID, OK: false, Error: err.Error()}
	}
	return *cr
}

// composeReviewBody appends every general (unanchored) draft's text to body,
// separated, so none of it is lost just because GitHub's review object only
// has one top-level body field for text that is not anchored to a line.
func composeReviewBody(body string, general []model.CommentDraft) string {
	if len(general) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(body)
	for _, c := range general {
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(c.Body)
	}
	return b.String()
}

// applyPublishResults writes every outcome back to the session store --
// published (with RemoteID/RemoteURL) on success, failed (with Error)
// otherwise -- and syncs the Workspace's own copy. A draft id with no match
// in the session (should not happen) is silently skipped rather than erroring
// the whole batch.
func (w *Workspace) applyPublishResults(repoKey, sessionID string, results []host.CommentResult) error {
	for _, r := range results {
		status := model.CommentFailed
		if r.OK {
			status = model.CommentPublished
		}
		if err := w.store.Update(repoKey, sessionID, func(s *model.ReviewSession) error {
			for i := range s.Comments {
				if s.Comments[i].ID != r.DraftID {
					continue
				}
				s.Comments[i].Status = status
				s.Comments[i].RemoteID = r.RemoteID
				s.Comments[i].RemoteURL = r.HTMLURL
				s.Comments[i].Error = r.Error
				break
			}
			return nil
		}); err != nil {
			return fmt.Errorf("review: record publish result for %s: %w", r.DraftID, err)
		}
	}
	return w.Sync()
}
