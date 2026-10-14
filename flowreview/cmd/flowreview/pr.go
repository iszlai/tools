package main

import (
	"context"
	"fmt"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/host/github"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// init wires review's PR hooks to the github host -- the only one flowreview
// speaks -- so internal/core/review never has to import a specific host
// package. FLOWREVIEW_GITHUB_API and GITHUB_TOKEN (read inside the github
// package) let a test point every network call this makes at a fake server.
func init() {
	review.FetchPR = fetchPRHook
	review.FetchRemoteComments = fetchRemoteCommentsHook
	review.SubmitReview = submitReviewHook
	review.PublishComment = publishCommentHook
}

// resolvePRRef turns the argument to "flowreview pr" into a full PRRef: a URL
// or "owner/repo#N" shorthand parses on its own, while a bare number needs
// cwd to be inside a clone whose "origin" remote names the owner/repo.
func resolvePRRef(ctx context.Context, raw, cwd string) (model.PRRef, error) {
	if ref, err := github.ParsePRRef(raw); err == nil {
		return ref, nil
	}

	repo, err := git.Open(ctx, cwd)
	if err != nil {
		return model.PRRef{}, notFoundErr(fmt.Errorf(
			"%q is not a git repository, needed to resolve the bare PR number %q to an owner/repo: %w", cwd, raw, err))
	}
	originURL, err := repo.RemoteURL(ctx, "origin")
	if err != nil {
		return model.PRRef{}, notFoundErr(fmt.Errorf(
			"could not read the %q remote to resolve PR number %q: %w", "origin", raw, err))
	}
	ref, err := github.ParsePRRefWithRepo(raw, originURL)
	if err != nil {
		return model.PRRef{}, usageErr(err)
	}
	return ref, nil
}

// fetchPRHook implements review.FetchPR: resolve the PR's metadata and diff
// from GitHub, and a local clone with its commits fetched (reusing cwd's repo
// when its origin matches, per github.OpenPR), returning the source filled in
// with everything review.Open needs to key and display the session.
func fetchPRHook(ctx context.Context, src model.Source) (model.Source, []byte, error) {
	if src.PR == nil {
		return model.Source{}, nil, fmt.Errorf("flowreview: PR source has no PR reference")
	}
	client, err := github.New(github.Options{})
	if err != nil {
		return model.Source{}, nil, err
	}
	resolved, diff, pr, err := github.OpenPR(ctx, client, *src.PR, src.RepoPath, session.DefaultRoot())
	if err != nil {
		return model.Source{}, nil, err
	}
	if resolved.PR != nil {
		resolved.PR.Title = pr.Title
	}
	return resolved, diff, nil
}

// fetchRemoteCommentsHook implements review.FetchRemoteComments.
func fetchRemoteCommentsHook(ctx context.Context, src model.Source) ([]host.RemoteComment, error) {
	if src.PR == nil {
		return nil, nil
	}
	client, err := github.New(github.Options{})
	if err != nil {
		return nil, err
	}
	return client.ListComments(ctx, *src.PR)
}

// submitReviewHook implements review.SubmitReview (FR-11).
func submitReviewHook(ctx context.Context, ref model.PRRef, r host.Review) (*host.ReviewResult, error) {
	client, err := github.New(github.Options{})
	if err != nil {
		return nil, err
	}
	return client.SubmitReview(ctx, ref, r)
}

// publishCommentHook implements review.PublishComment (FR-11).
func publishCommentHook(ctx context.Context, ref model.PRRef, commitID string, c model.CommentDraft) (*host.CommentResult, error) {
	client, err := github.New(github.Options{})
	if err != nil {
		return nil, err
	}
	return client.PublishComment(ctx, ref, commitID, c)
}
