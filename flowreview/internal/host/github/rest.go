package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Sentinel errors for the status codes callers most need to branch on.
// Wrapped with context via %w, so errors.Is still matches.
var (
	ErrUnauthorized = errors.New("github: unauthorized")
	ErrForbidden    = errors.New("github: forbidden")
	ErrNotFound     = errors.New("github: not found")
)

// errNotImplemented is returned by the FR-11 publish stubs.
var errNotImplemented = errors.New("github: not implemented (FR-11)")

// newRequest builds a request for path, which may be a path relative to the
// client's base URL ("/repos/o/r/pulls/1") or an absolute URL (as Link
// headers give for later pages). accept overrides the default JSON media
// type when non-empty, e.g. for the raw-diff representation of a PR.
func (c *Client) newRequest(ctx context.Context, method, path, accept string) (*http.Request, error) {
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		url = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if accept != "" {
		req.Header.Set("Accept", accept)
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	return req, nil
}

// getJSON GETs path and decodes a single JSON object into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	body, err := c.getRaw(ctx, path, "")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("github: GET %s: decode response: %w", path, err)
	}
	return nil
}

// getRaw GETs path and returns the raw body, e.g. a PR's unified diff
// requested with the application/vnd.github.v3.diff media type.
func (c *Client) getRaw(ctx context.Context, path, accept string) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, accept)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: GET %s: read response: %w", path, err)
	}
	return body, nil
}

// fetchAllPages GETs path and every page the Link header's rel="next" points
// to, decoding each page as a JSON array of T and concatenating the results.
func fetchAllPages[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	next := path
	for next != "" {
		req, err := c.newRequest(ctx, http.MethodGet, next, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github: GET %s: %w", next, err)
		}
		if err := checkStatus(resp); err != nil {
			resp.Body.Close()
			return nil, err
		}
		var page []T
		decodeErr := json.NewDecoder(resp.Body).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("github: GET %s: decode response: %w", next, decodeErr)
		}
		all = append(all, page...)
		next = nextPageURL(link)
	}
	return all, nil
}

// nextPageURL extracts the rel="next" URL from a GitHub Link header, or ""
// when there is no next page. Link headers look like:
//
//	<https://api.github.com/...&page=2>; rel="next", <...>; rel="last"
func nextPageURL(header string) string {
	for _, part := range strings.Split(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		url := strings.TrimSpace(segs[0])
		url = strings.TrimPrefix(url, "<")
		url = strings.TrimSuffix(url, ">")
		for _, attr := range segs[1:] {
			if strings.TrimSpace(attr) == `rel="next"` {
				return url
			}
		}
	}
	return ""
}

// apiError is GitHub's standard error body shape.
type apiError struct {
	Message string `json:"message"`
}

// checkStatus turns a non-2xx response into a clear error, reading GitHub's
// JSON error message when present. 401/403/404 get a sentinel so callers can
// branch with errors.Is, plus a hint toward the likely fix.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := strings.TrimSpace(string(body))
	var apiErr apiError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
		msg = apiErr.Message
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s (check your token: `gh auth login`, or GITHUB_TOKEN)", ErrUnauthorized, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s (token may lack scope, or you hit a rate limit)", ErrForbidden, msg)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s (check the owner/repo/PR number, and that your token can see it)", ErrNotFound, msg)
	default:
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("github: %s %s: %s", resp.Request.Method, resp.Request.URL.Path, msg)
	}
}
