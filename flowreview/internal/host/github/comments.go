package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// reviewCommentResponse is one element of GET /pulls/{n}/comments: a comment
// anchored to a line of the diff. Line is null when the comment's position no
// longer maps onto the current diff ("outdated"); OriginalLine is always the
// line it was created against, so there is still somewhere to show it.
// See https://docs.github.com/en/rest/pulls/comments.
type reviewCommentResponse struct {
	ID           int64     `json:"id"`
	Body         string    `json:"body"`
	Path         string    `json:"path"`
	Line         *int      `json:"line"`
	OriginalLine int       `json:"original_line"`
	Side         string    `json:"side"` // "LEFT" | "RIGHT"
	CommitID     string    `json:"commit_id"`
	InReplyToID  int64     `json:"in_reply_to_id"`
	CreatedAt    time.Time `json:"created_at"`
	HTMLURL      string    `json:"html_url"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
}

// issueCommentResponse is one element of GET /issues/{n}/comments: a general
// comment on the PR conversation, not anchored to any line.
// See https://docs.github.com/en/rest/issues/comments.
type issueCommentResponse struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	HTMLURL   string    `json:"html_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// ListComments returns both kinds of comment GitHub has on a PR: review
// comments anchored to a diff line, and general conversation comments.
func (c *Client) ListComments(ctx context.Context, ref model.PRRef) ([]host.RemoteComment, error) {
	reviewComments, err := c.reviewComments(ctx, ref)
	if err != nil {
		return nil, err
	}
	issueComments, err := c.generalComments(ctx, ref)
	if err != nil {
		return nil, err
	}
	return append(reviewComments, issueComments...), nil
}

func (c *Client) reviewComments(ctx context.Context, ref model.PRRef) ([]host.RemoteComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=100", ref.Owner, ref.Repo, ref.Number)
	raw, err := fetchAllPages[reviewCommentResponse](ctx, c, path)
	if err != nil {
		return nil, err
	}
	out := make([]host.RemoteComment, 0, len(raw))
	for _, rc := range raw {
		line := rc.OriginalLine
		outdated := rc.Line == nil
		if !outdated {
			line = *rc.Line
		}
		side := model.SideNew
		if strings.EqualFold(rc.Side, "LEFT") {
			side = model.SideOld
		}
		out = append(out, host.RemoteComment{
			ID:        rc.ID,
			Author:    rc.User.Login,
			Body:      rc.Body,
			Path:      rc.Path,
			Line:      line,
			Side:      side,
			CommitID:  rc.CommitID,
			InReplyTo: rc.InReplyToID,
			CreatedAt: rc.CreatedAt,
			HTMLURL:   rc.HTMLURL,
			Outdated:  outdated,
		})
	}
	return out, nil
}

func (c *Client) generalComments(ctx context.Context, ref model.PRRef) ([]host.RemoteComment, error) {
	// PRs are issues too, in GitHub's data model: general conversation
	// comments live under /issues/{n}/comments, not /pulls/{n}/comments.
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", ref.Owner, ref.Repo, ref.Number)
	raw, err := fetchAllPages[issueCommentResponse](ctx, c, path)
	if err != nil {
		return nil, err
	}
	out := make([]host.RemoteComment, 0, len(raw))
	for _, ic := range raw {
		out = append(out, host.RemoteComment{
			ID:        ic.ID,
			Author:    ic.User.Login,
			Body:      ic.Body,
			CreatedAt: ic.CreatedAt,
			HTMLURL:   ic.HTMLURL,
		})
	}
	return out, nil
}

// Review is one submitted review of the PR, as GitHub recorded it (not to be
// confused with host.Review, which is a batch this client would submit).
type Review struct {
	ID          int64     `json:"id"`
	Author      string    `json:"author"`
	Body        string    `json:"body"`
	State       string    `json:"state"` // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED | PENDING
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
}

// reviewResponse is one element of GET /pulls/{n}/reviews.
// See https://docs.github.com/en/rest/pulls/reviews.
type reviewResponse struct {
	ID          int64     `json:"id"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

// ListReviews returns the reviews submitted on the PR (approvals, change
// requests, and plain review comments). Not part of host.Host — reviews are
// GitHub-specific enough that FR-11's batch submission is the only shape the
// interface needs; this is extra read surface for the UI's side panel.
func (c *Client) ListReviews(ctx context.Context, ref model.PRRef) ([]Review, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", ref.Owner, ref.Repo, ref.Number)
	raw, err := fetchAllPages[reviewResponse](ctx, c, path)
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(raw))
	for _, r := range raw {
		out = append(out, Review{
			ID:          r.ID,
			Author:      r.User.Login,
			Body:        r.Body,
			State:       r.State,
			CommitID:    r.CommitID,
			SubmittedAt: r.SubmittedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return out, nil
}
