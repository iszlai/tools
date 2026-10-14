package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// sideString maps model.Side to GitHub's line-anchoring side for the new
// line+side review-comment API (LEFT = old/base, RIGHT = new/head).
func sideString(s model.Side) string {
	if s == model.SideOld {
		return "LEFT"
	}
	return "RIGHT"
}

// reviewCommentRequest is one inline comment inside a submitReviewRequest or
// a standalone POST .../comments body -- the new line+side shape, not the
// older "position" one (position is kept on model.Anchor only for
// verification, per PLAN.md).
type reviewCommentRequest struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"` // LEFT | RIGHT
	Body string `json:"body"`
}

func toReviewComment(c model.CommentDraft) reviewCommentRequest {
	return reviewCommentRequest{
		Path: c.Anchor.Path,
		Line: c.Anchor.Line,
		Side: sideString(c.Anchor.Side),
		Body: c.Body,
	}
}

// submitReviewRequest is the body of POST /repos/{o}/{r}/pulls/{n}/reviews.
// See https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request.
type submitReviewRequest struct {
	CommitID string                 `json:"commit_id,omitempty"`
	Body     string                 `json:"body,omitempty"`
	Event    string                 `json:"event"`
	Comments []reviewCommentRequest `json:"comments,omitempty"`
}

// submitReviewResponse is the subset of GitHub's review object this client
// reads back -- notably not including the inline comments it created: that
// endpoint echoes only the review itself, so per-comment ids/urls are only
// ever known from PublishComment's own response.
type submitReviewResponse struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
}

// SubmitReview is FR-11: submits r as one GitHub review -- COMMENT, APPROVE
// or REQUEST_CHANGES -- with r.Comments placed inline via the line+side API.
// r.Comments must already be anchored (internal/core/review folds any
// general, unanchored drafts into r.Body before calling this); any entry
// with a nil Anchor is skipped defensively rather than sent malformed.
//
// GitHub rejects the whole review with 422 when any one comment's
// path/line/side does not resolve against commit_id's diff, with no way to
// tell from that response which comment was the problem. When that happens,
// this falls back to submitting the review's body and event alone (with no
// inline comments, which always succeeds once the review package's preflight
// has confirmed commit_id is the PR's real current head) and then publishes
// every originally-requested comment one at a time via PublishComment, so
// each one gets its own true/false result instead of the whole batch
// failing together.
func (c *Client) SubmitReview(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", ref.Owner, ref.Repo, ref.Number)

	anchored := make([]model.CommentDraft, 0, len(r.Comments))
	reqComments := make([]reviewCommentRequest, 0, len(r.Comments))
	for _, cm := range r.Comments {
		if cm.Anchor == nil {
			continue
		}
		anchored = append(anchored, cm)
		reqComments = append(reqComments, toReviewComment(cm))
	}

	var resp submitReviewResponse
	err := c.postJSON(ctx, path, submitReviewRequest{
		CommitID: r.CommitID, Body: r.Body, Event: string(r.Event), Comments: reqComments,
	}, &resp)
	if err == nil {
		results := make([]host.CommentResult, len(anchored))
		for i, cm := range anchored {
			results[i] = host.CommentResult{DraftID: cm.ID, OK: true}
		}
		return &host.ReviewResult{ID: resp.ID, HTMLURL: resp.HTMLURL, Comments: results}, nil
	}
	if !errors.Is(err, ErrUnprocessable) {
		return nil, err
	}

	var bodyOnly submitReviewResponse
	if fbErr := c.postJSON(ctx, path, submitReviewRequest{
		CommitID: r.CommitID, Body: r.Body, Event: string(r.Event),
	}, &bodyOnly); fbErr != nil {
		return nil, fmt.Errorf("github: batch review rejected (%w), and the body-only fallback also failed: %w", err, fbErr)
	}

	results := make([]host.CommentResult, len(anchored))
	for i, cm := range anchored {
		cr, pubErr := c.PublishComment(ctx, ref, r.CommitID, cm)
		if pubErr != nil {
			results[i] = host.CommentResult{DraftID: cm.ID, OK: false, Error: pubErr.Error()}
			continue
		}
		results[i] = *cr
	}
	return &host.ReviewResult{ID: bodyOnly.ID, HTMLURL: bodyOnly.HTMLURL, Comments: results}, nil
}

// reviewCommentCreateResponse is the subset of GitHub's review-comment object
// this client reads back from POST .../comments.
// See https://docs.github.com/en/rest/pulls/comments#create-a-review-comment-for-a-pull-request.
type reviewCommentCreateResponse struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
}

// PublishComment is FR-11: posts one inline review comment standalone (not
// tied to any review object), via the line+side API. Used both as
// SubmitReview's per-comment fallback after a 422, and directly to retry a
// single comment that previously failed without resubmitting a whole review.
func (c *Client) PublishComment(ctx context.Context, ref model.PRRef, commitID string, cm model.CommentDraft) (*host.CommentResult, error) {
	if cm.Anchor == nil {
		return nil, fmt.Errorf("github: PublishComment: draft %s has no anchor", cm.ID)
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", ref.Owner, ref.Repo, ref.Number)

	type createRequest struct {
		CommitID string `json:"commit_id"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Side     string `json:"side"`
		Body     string `json:"body"`
	}
	var resp reviewCommentCreateResponse
	err := c.postJSON(ctx, path, createRequest{
		CommitID: commitID,
		Path:     cm.Anchor.Path,
		Line:     cm.Anchor.Line,
		Side:     sideString(cm.Anchor.Side),
		Body:     cm.Body,
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &host.CommentResult{DraftID: cm.ID, OK: true, RemoteID: resp.ID, HTMLURL: resp.HTMLURL}, nil
}
