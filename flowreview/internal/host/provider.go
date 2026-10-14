// Package host talks to code hosts: fetching pull requests and their
// comments, and publishing a review.
package host

import (
	"context"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Capabilities says what a host implementation can do.
type Capabilities struct {
	Name    string `json:"name"` // "github"
	Read    bool   `json:"read"`
	Publish bool   `json:"publish"`
}

// PullRequest is the metadata of a PR.
type PullRequest struct {
	Ref      model.PRRef `json:"ref"`
	Title    string      `json:"title"`
	Body     string      `json:"body,omitempty"`
	Author   string      `json:"author"`
	State    string      `json:"state"` // open | closed | merged
	Draft    bool        `json:"draft,omitempty"`
	BaseRef  string      `json:"base_ref"`
	HeadRef  string      `json:"head_ref"`
	BaseSHA  string      `json:"base_sha"`
	HeadSHA  string      `json:"head_sha"`
	CloneURL string      `json:"clone_url,omitempty"`
	HTMLURL  string      `json:"html_url,omitempty"`
}

// RemoteComment is a comment already on the host.
type RemoteComment struct {
	ID        int64      `json:"id"`
	Author    string     `json:"author"`
	Body      string     `json:"body"`
	Path      string     `json:"path,omitempty"` // empty for review-level comments
	Line      int        `json:"line,omitempty"`
	Side      model.Side `json:"side,omitempty"`
	CommitID  string     `json:"commit_id,omitempty"`
	InReplyTo int64      `json:"in_reply_to,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	HTMLURL   string     `json:"html_url,omitempty"`
	Outdated  bool       `json:"outdated,omitempty"`
}

// ReviewEvent is the verdict submitted with a review.
type ReviewEvent string

const (
	EventComment        ReviewEvent = "COMMENT"
	EventApprove        ReviewEvent = "APPROVE"
	EventRequestChanges ReviewEvent = "REQUEST_CHANGES"
)

// Review is a batch to submit.
type Review struct {
	Event    ReviewEvent          `json:"event"`
	Body     string               `json:"body,omitempty"`
	CommitID string               `json:"commit_id"`
	Comments []model.CommentDraft `json:"comments"`
}

// CommentResult is the outcome for one draft.
type CommentResult struct {
	DraftID  string `json:"draft_id"`
	OK       bool   `json:"ok"`
	RemoteID int64  `json:"remote_id,omitempty"`
	HTMLURL  string `json:"html_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ReviewResult is the outcome of a submitted review.
type ReviewResult struct {
	ID       int64           `json:"id,omitempty"`
	HTMLURL  string          `json:"html_url,omitempty"`
	Comments []CommentResult `json:"comments"`
}

// Host is a code host.
type Host interface {
	Capabilities() Capabilities
	FetchPR(ctx context.Context, ref model.PRRef) (*PullRequest, error)
	// FetchDiff returns the unified diff of the PR as the host renders it,
	// which is what comment positions are relative to.
	FetchDiff(ctx context.Context, ref model.PRRef) ([]byte, error)
	ListComments(ctx context.Context, ref model.PRRef) ([]RemoteComment, error)
	SubmitReview(ctx context.Context, ref model.PRRef, r Review) (*ReviewResult, error)
	PublishComment(ctx context.Context, ref model.PRRef, commitID string, c model.CommentDraft) (*CommentResult, error)
}
