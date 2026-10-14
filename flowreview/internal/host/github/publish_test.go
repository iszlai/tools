package github

import (
	"context"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestClient_satisfiesHost pins Client to the host.Host interface at compile
// time: if either changes shape incompatibly, this file fails to build.
var _ host.Host = (*Client)(nil)

func TestSubmitReview_notImplemented(t *testing.T) {
	c := &Client{token: "t", baseURL: defaultBaseURL, http: nil}
	_, err := c.SubmitReview(context.Background(), model.PRRef{}, host.Review{})
	if err == nil {
		t.Fatal("SubmitReview: want error before FR-11")
	}
}

func TestPublishComment_notImplemented(t *testing.T) {
	c := &Client{token: "t", baseURL: defaultBaseURL, http: nil}
	_, err := c.PublishComment(context.Background(), model.PRRef{}, "sha", model.CommentDraft{})
	if err == nil {
		t.Fatal("PublishComment: want error before FR-11")
	}
}
