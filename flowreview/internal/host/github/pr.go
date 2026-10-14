package github

import (
	"context"
	"fmt"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Capabilities reports this client as read-capable only until FR-11 lands.
func (c *Client) Capabilities() host.Capabilities {
	return host.Capabilities{Name: "github", Read: true, Publish: false}
}

// prResponse is the subset of GitHub's pull request object this client uses.
// See https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request.
type prResponse struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"` // "open" | "closed"
	Merged  bool   `json:"merged"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			CloneURL string `json:"clone_url"`
		} `json:"repo"` // nil when the source fork was deleted
	} `json:"head"`
}

// FetchPR fetches the PR's metadata, including head/base refs and SHAs.
func (c *Client) FetchPR(ctx context.Context, ref model.PRRef) (*host.PullRequest, error) {
	var pr prResponse
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)
	if err := c.getJSON(ctx, path, &pr); err != nil {
		return nil, err
	}

	state := pr.State
	if pr.Merged {
		state = "merged"
	}
	var cloneURL string
	if pr.Head.Repo != nil {
		cloneURL = pr.Head.Repo.CloneURL
	}

	return &host.PullRequest{
		Ref:      ref,
		Title:    pr.Title,
		Body:     pr.Body,
		Author:   pr.User.Login,
		State:    state,
		Draft:    pr.Draft,
		BaseRef:  pr.Base.Ref,
		HeadRef:  pr.Head.Ref,
		BaseSHA:  pr.Base.SHA,
		HeadSHA:  pr.Head.SHA,
		CloneURL: cloneURL,
		HTMLURL:  pr.HTMLURL,
	}, nil
}

// FetchDiff returns the PR's unified diff exactly as GitHub renders it, by
// requesting the pull request resource with the diff media type instead of
// JSON. Comment positions (FR-9/FR-11) are relative to this rendering, not
// to a locally recomputed diff.
func (c *Client) FetchDiff(ctx context.Context, ref model.PRRef) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)
	return c.getRaw(ctx, path, "application/vnd.github.v3.diff")
}
