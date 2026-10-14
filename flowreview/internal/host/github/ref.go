package github

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

var (
	rePRURL     = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)/pull/(\d+)(?:/.*)?$`)
	reShorthand = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#(\d+)$`)
	reBareNum   = regexp.MustCompile(`^#?(\d+)$`)
)

// ParsePRRef parses a PR reference given as a full URL
// ("https://github.com/o/r/pull/123") or an "o/r#123" shorthand. A bare
// number ("123") cannot be resolved here, since it needs a repo to belong
// to; use ParsePRRefWithRepo for that case.
func ParsePRRef(s string) (model.PRRef, error) {
	s = strings.TrimSpace(s)

	if m := rePRURL.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[4])
		if err != nil {
			return model.PRRef{}, fmt.Errorf("github: parse PR reference %q: %w", s, err)
		}
		return model.PRRef{
			Host:   m[1],
			Owner:  m[2],
			Repo:   strings.TrimSuffix(m[3], ".git"),
			Number: n,
			URL:    s,
		}, nil
	}

	if m := reShorthand.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return model.PRRef{}, fmt.Errorf("github: parse PR reference %q: %w", s, err)
		}
		return model.PRRef{Host: "github.com", Owner: m[1], Repo: m[2], Number: n}, nil
	}

	if reBareNum.MatchString(s) {
		return model.PRRef{}, fmt.Errorf(
			"github: %q is a bare PR number; it needs a repo — use ParsePRRefWithRepo "+
				"with the cwd's origin URL", s)
	}

	return model.PRRef{}, fmt.Errorf(
		"github: cannot parse %q as a PR reference "+
			"(want a URL, \"owner/repo#123\" or a bare number)", s)
}

// ParsePRRefWithRepo is ParsePRRef, plus support for a bare PR number by
// taking the owner/repo from originURL — the repository's `origin` remote,
// in any form git accepts (https://, git@host:, or ssh://).
func ParsePRRefWithRepo(s, originURL string) (model.PRRef, error) {
	ref, err := ParsePRRef(s)
	if err == nil {
		return ref, nil
	}
	trimmed := strings.TrimSpace(s)
	if !reBareNum.MatchString(trimmed) {
		return model.PRRef{}, err
	}

	owner, repo, oerr := ownerRepoFromRemote(originURL)
	if oerr != nil {
		return model.PRRef{}, fmt.Errorf("github: parse PR number %q: %w", s, oerr)
	}
	n, nerr := strconv.Atoi(strings.TrimPrefix(trimmed, "#"))
	if nerr != nil {
		return model.PRRef{}, fmt.Errorf("github: parse PR number %q: %w", s, nerr)
	}
	return model.PRRef{Host: "github.com", Owner: owner, Repo: repo, Number: n}, nil
}

// ownerRepoFromRemote extracts "owner", "repo" from a git remote URL in any
// form git accepts: https://host/owner/repo(.git), git@host:owner/repo(.git),
// or ssh://git@host/owner/repo(.git).
func ownerRepoFromRemote(remote string) (owner, repo string, err error) {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	var path string
	switch {
	case strings.Contains(s, "://"):
		u, perr := url.Parse(s)
		if perr != nil {
			return "", "", fmt.Errorf("parse remote URL %q: %w", remote, perr)
		}
		path = strings.TrimPrefix(u.Path, "/")
	case strings.HasPrefix(s, "git@") || strings.Contains(s, "@"):
		// scp-like syntax: user@host:owner/repo
		_, after, found := strings.Cut(s, ":")
		if !found {
			return "", "", fmt.Errorf("parse remote URL %q: no %q separator", remote, ":")
		}
		path = after
	default:
		return "", "", fmt.Errorf("parse remote URL %q: unrecognized form", remote)
	}

	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", fmt.Errorf("parse remote URL %q: no owner/repo in path %q", remote, path)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}
