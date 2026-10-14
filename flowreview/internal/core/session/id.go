package session

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// NewID derives a session id from its source, so opening the same PR or
// range always proposes the same id: "pr-owner-repo-123", "range-<hash>",
// "commit-<hash>", "patch-<hash>". A worktree session is always "worktree" --
// there is only ever one uncommitted diff per repo. A Source without enough
// of a natural key gets a short random id instead; FindBySource is how that
// kind of session gets resumed.
func NewID(src model.Source) string {
	switch src.Kind {
	case model.SourcePR:
		if src.PR != nil && src.PR.Owner != "" && src.PR.Repo != "" {
			return fmt.Sprintf("pr-%s-%s-%d", slug(src.PR.Owner), slug(src.PR.Repo), src.PR.Number)
		}
	case model.SourceRange:
		if src.Ref != "" {
			return "range-" + shortHash(src.Ref)
		}
	case model.SourceCommit:
		if src.Ref != "" {
			return "commit-" + shortHash(src.Ref)
		}
	case model.SourcePatch:
		if src.PatchPath != "" {
			return "patch-" + shortHash(src.PatchPath)
		}
	case model.SourceWorktree:
		return "worktree"
	}
	return "s-" + randomHex(8)
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read on a real OS does not fail
	return hex.EncodeToString(b)
}

// FindBySource looks for an existing session reviewing the same logical diff
// as src, so reopening a PR or range resumes its review instead of starting a
// new one. Matching ignores fields that drift as a diff's host advances
// (BaseSHA, HeadSHA) and compares the identity of what is being reviewed
// instead: which PR, which patch file, which range or commit by its Ref.
func (s *Store) FindBySource(repoKey string, src model.Source) (*model.ReviewSession, error) {
	sessions, err := s.List(repoKey)
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sameSource(sess.Source, src) {
			return sess, nil
		}
	}
	return nil, ErrNotFound
}

func sameSource(a, b model.Source) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case model.SourcePR:
		if a.PR == nil || b.PR == nil {
			return false
		}
		return strings.EqualFold(a.PR.Host, b.PR.Host) &&
			strings.EqualFold(a.PR.Owner, b.PR.Owner) &&
			strings.EqualFold(a.PR.Repo, b.PR.Repo) &&
			a.PR.Number == b.PR.Number
	case model.SourcePatch:
		return a.PatchPath == b.PatchPath
	case model.SourceWorktree:
		return a.RepoPath == b.RepoPath
	case model.SourceRange, model.SourceCommit:
		return a.RepoPath == b.RepoPath && a.Ref == b.Ref
	default:
		return false
	}
}
