// Package github implements the FlowReview host.Host interface against the
// GitHub REST API. The read half (fetching PRs, diffs, comments and reviews)
// is FR-10; SubmitReview and PublishComment are FR-11 and return an error
// here so the interface is satisfied in the meantime.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// defaultBaseURL is GitHub's public REST API. A different BaseURL lets this
// client talk to a GitHub Enterprise Server instance instead.
const defaultBaseURL = "https://api.github.com"

// Options configures a Client. Token, if set, wins over everything else;
// otherwise GITHUB_TOKEN, then GH_TOKEN, then `gh auth token` are tried in
// that order.
type Options struct {
	BaseURL string
	HTTP    *http.Client
	Token   string
}

// Client is a GitHub REST client authenticated with a single token.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

// New resolves a token (Options.Token, then GITHUB_TOKEN, then GH_TOKEN,
// then `gh auth token`) and returns a ready Client. It fails fast with an
// install/login hint when no token can be found.
func New(opts Options) (*Client, error) {
	token := opts.Token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		t, err := tokenFromGH(context.Background())
		if err != nil {
			return nil, err
		}
		token = t
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    httpClient,
		token:   token,
	}, nil
}

// lookPath and runGHAuthToken are package vars so tests can simulate "gh
// missing" and "gh not logged in" without touching the real gh binary.
var lookPath = exec.LookPath

var runGHAuthToken = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh auth token: %s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// tokenFromGH shells out to `gh auth token`. It is a var (not a plain call
// in New) so tests can stub it out.
var tokenFromGH = func(ctx context.Context) (string, error) {
	if _, err := lookPath("gh"); err != nil {
		return "", errors.New("github: no token found and the gh CLI is not installed; " +
			"install it from https://cli.github.com and run `gh auth login`, " +
			"or set GITHUB_TOKEN")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := runGHAuthToken(ctx)
	if err != nil {
		return "", fmt.Errorf("github: no token found and %s; run `gh auth login`, "+
			"or set GITHUB_TOKEN", err)
	}
	if token == "" {
		return "", errors.New("github: gh auth token returned an empty token; " +
			"run `gh auth login`, or set GITHUB_TOKEN")
	}
	return token, nil
}
