package session

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// scpLike matches git's scp-style SSH syntax, which has no scheme:
// "user@host:path", e.g. "git@github.com:owner/repo.git".
var scpLike = regexp.MustCompile(`^[\w.+-]+@([\w.-]+):(.+)$`)

// RepoKey turns a git remote URL or an absolute repository path into a short,
// stable identifier used as a directory name under sessions/. Remote URLs are
// normalized first, so equivalent spellings of the same remote collide on
// purpose: "git@github.com:o/r.git" and "https://github.com/o/r" both produce
// the same key.
func RepoKey(remoteURLOrAbsPath string) string {
	sum := sha1.Sum([]byte(normalizeRepoRef(remoteURLOrAbsPath)))
	return hex.EncodeToString(sum[:])[:16]
}

// normalizeRepoRef reduces a remote URL to "host/owner/repo" (lowercased, no
// scheme, no credentials, no trailing ".git"), or cleans a local path to its
// absolute form. Anything it cannot parse as either is returned trimmed, so
// RepoKey stays deterministic even for input it does not recognise.
func normalizeRepoRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimSuffix(ref, "/")
	ref = strings.TrimSuffix(ref, ".git")

	if m := scpLike.FindStringSubmatch(ref); m != nil {
		return strings.ToLower(m[1] + "/" + strings.TrimPrefix(m[2], "/"))
	}
	if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
		return strings.ToLower(u.Host + "/" + strings.TrimPrefix(u.Path, "/"))
	}
	if abs, err := filepath.Abs(ref); err == nil {
		return abs
	}
	return ref
}
