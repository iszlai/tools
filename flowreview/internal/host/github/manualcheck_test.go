//go:build manual

package github

import (
	"context"
	"fmt"
	"testing"
)

// TestManualCheck_realPR is not run by `go test ./...` (the manual build
// tag excludes it). It is the one real, read-only check against a public PR
// described in README.md, kept here only long enough to run once by hand.
func TestManualCheck_realPR(t *testing.T) {
	ctx := context.Background()
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, err := ParsePRRef("https://github.com/cli/cli/pull/14679")
	if err != nil {
		t.Fatalf("ParsePRRef: %v", err)
	}

	pr, err := c.FetchPR(ctx, ref)
	if err != nil {
		t.Fatalf("FetchPR: %v", err)
	}
	fmt.Printf("PR: %+v\n", *pr)

	diff, err := c.FetchDiff(ctx, ref)
	if err != nil {
		t.Fatalf("FetchDiff: %v", err)
	}
	fmt.Printf("diff: %d bytes\n", len(diff))

	comments, err := c.ListComments(ctx, ref)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	fmt.Printf("comments: %d\n", len(comments))

	reviews, err := c.ListReviews(ctx, ref)
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	fmt.Printf("reviews: %d\n", len(reviews))
}
