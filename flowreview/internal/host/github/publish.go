package github

import (
	"context"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// SubmitReview is FR-11. It is stubbed here only so Client satisfies
// host.Host before publishing is implemented.
func (c *Client) SubmitReview(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
	return nil, errNotImplemented
}

// PublishComment is FR-11. It is stubbed here only so Client satisfies
// host.Host before publishing is implemented.
func (c *Client) PublishComment(ctx context.Context, ref model.PRRef, commitID string, cm model.CommentDraft) (*host.CommentResult, error) {
	return nil, errNotImplemented
}
