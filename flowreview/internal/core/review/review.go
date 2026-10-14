// Package review opens one diff for review: it fetches the raw diff for a
// model.Source via git (or a patch file), parses it, and finds or creates the
// session that tracks progress on it, reconciling stored state against the
// fresh diff on every open.
package review

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// ErrPRNotSupported is returned for model.SourcePR until FR-10 gives it a
// GitHub host to fetch from.
var ErrPRNotSupported = errors.New("review: PR sources need a host adapter (FR-10)")

// FetchPR is the hook FR-12 fills in (cmd/flowreview wires it to the github
// host): given a PR source -- Kind SourcePR, PR naming owner/repo/number, and
// RepoPath as a hint for resolving a bare PR number and for reusing a local
// clone -- it resolves the PR against a host and returns the source filled in
// with the resolved local clone path and head/base SHAs, plus the raw unified
// diff the host renders, which is what comment positions are relative to.
// Left nil until the CLI wires a host.
var FetchPR func(ctx context.Context, src model.Source) (resolved model.Source, diff []byte, err error)

// FetchRemoteComments is the companion hook: given a resolved PR source, it
// returns the comments already on the PR at the host, for display alongside
// the diff. Left nil until the CLI wires a host; a nil hook, or an error from
// it, just means no comments are shown -- they are supplementary to the diff,
// never required to open it.
var FetchRemoteComments func(ctx context.Context, src model.Source) ([]host.RemoteComment, error)

// Workspace is one open review: the parsed diff, the persisted session
// tracking progress on it, the repo it came from (nil for a patch file
// reviewed outside any git working tree), and -- for a PR -- the comments
// already on it at the host.
type Workspace struct {
	Session *model.ReviewSession
	Files   []model.ChangedFile
	Repo    *git.Repo
	Remote  []host.RemoteComment

	mu    sync.Mutex
	store *session.Store
	src   model.Source
}

// Open loads the diff for src, finds or creates the session that tracks its
// review (session.FindBySource), reconciles any stored state against the
// fresh diff (session.Refresh) and saves the result.
func Open(ctx context.Context, store *session.Store, src model.Source) (*Workspace, error) {
	repo, raw, err := loadDiff(ctx, &src)
	if err != nil {
		return nil, err
	}
	files, err := diff.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("review: parse diff: %w", err)
	}

	repoKey := repoKeyFor(ctx, repo, src)
	sess, err := store.FindBySource(repoKey, src)
	switch {
	case errors.Is(err, session.ErrNotFound):
		sess = newSession(repoKey, src)
		if err := store.Create(sess); err != nil {
			return nil, fmt.Errorf("review: create session: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("review: find session: %w", err)
	default:
		// An existing session may carry state from a diff that has since
		// moved on (more commits, a rebase, an edited worktree); reconcile
		// before handing it back.
		if err := store.Update(repoKey, sess.ID, func(s *model.ReviewSession) error {
			session.Refresh(s, files)
			sess = s
			return nil
		}); err != nil {
			return nil, fmt.Errorf("review: refresh session: %w", err)
		}
	}

	var remote []host.RemoteComment
	if src.Kind == model.SourcePR && FetchRemoteComments != nil {
		remote, _ = FetchRemoteComments(ctx, src) // best-effort: supplementary to the diff
	}

	return &Workspace{
		Session: sess,
		Files:   files,
		Repo:    repo,
		Remote:  remote,
		store:   store,
		src:     src,
	}, nil
}

// Reload re-fetches the diff (from git, or re-reads the patch file) and
// reconciles the session against it again, as if the review had just been
// opened. Use this when the thing being reviewed may have moved since the
// Workspace was created, e.g. more commits landed on a worktree branch.
func (w *Workspace) Reload(ctx context.Context) error {
	fresh, err := Open(ctx, w.store, w.src)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Session, w.Files, w.Repo, w.Remote = fresh.Session, fresh.Files, fresh.Repo, fresh.Remote
	return nil
}

// RemoteComments returns the comments already on the host for a PR session
// (nil for everything else), as of the last Open or Reload.
func (w *Workspace) RemoteComments() []host.RemoteComment {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Remote
}

// Sync reloads just the session from disk, for callers that changed it
// through the Store directly (MarkReviewed, UpsertComment, ...) and want the
// Workspace's copy to catch up without re-running the diff.
func (w *Workspace) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	sess, err := w.store.Get(w.Session.RepoKey, w.Session.ID)
	if err != nil {
		return err
	}
	w.Session = sess
	return nil
}

// Snapshot returns the current session, files and repo together, so a reader
// never sees a session from before a Reload paired with files from after it
// (or vice versa).
func (w *Workspace) Snapshot() (*model.ReviewSession, []model.ChangedFile, *git.Repo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Session, w.Files, w.Repo
}

// loadDiff fetches the raw diff for src and, for the git-backed kinds,
// normalizes src.RepoPath to the repo's toplevel so the identity it is opened
// with (and matched against on the next Open) is stable regardless of which
// subdirectory or empty string the caller passed in.
func loadDiff(ctx context.Context, src *model.Source) (*git.Repo, []byte, error) {
	switch src.Kind {
	case model.SourceWorktree, model.SourceRange, model.SourceCommit:
		repo, err := git.Open(ctx, src.RepoPath)
		if err != nil {
			return nil, nil, fmt.Errorf("review: open repo: %w", err)
		}
		src.RepoPath = repo.Root()

		var raw []byte
		switch src.Kind {
		case model.SourceWorktree:
			raw, err = repo.WorktreeDiff(ctx)
		case model.SourceRange:
			raw, err = repo.RangeDiff(ctx, src.Ref)
		case model.SourceCommit:
			raw, err = repo.CommitDiff(ctx, src.Ref)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("review: diff: %w", err)
		}
		return repo, raw, nil

	case model.SourcePatch:
		raw, err := git.PatchFile(src.PatchPath)
		if err != nil {
			return nil, nil, fmt.Errorf("review: read patch: %w", err)
		}
		if abs, err := filepath.Abs(src.PatchPath); err == nil {
			src.PatchPath = abs
		}
		// Best-effort: a patch reviewed from inside a repo's working tree
		// still gets a repo handle (later slices use it to read file content
		// at a revision); it is not required to parse the patch itself.
		return tryOpenRepo(ctx, src.RepoPath), raw, nil

	case model.SourcePR:
		if FetchPR == nil {
			return nil, nil, ErrPRNotSupported
		}
		resolved, raw, err := FetchPR(ctx, *src)
		if err != nil {
			return nil, nil, err
		}
		*src = resolved
		return tryOpenRepo(ctx, src.RepoPath), raw, nil

	default:
		return nil, nil, fmt.Errorf("review: unknown source kind %q", src.Kind)
	}
}

// tryOpenRepo opens path as a repo, or returns nil when it isn't one. Used
// where a repo handle is a nice-to-have, not a requirement.
func tryOpenRepo(ctx context.Context, path string) *git.Repo {
	repo, err := git.Open(ctx, path)
	if err != nil {
		return nil
	}
	return repo
}

// repoKeyFor picks the identity a session is filed under: the repo's remote
// (so clones of the same repo on different machines share sessions), falling
// back to its local path, then to the patch path for a patch reviewed outside
// any repo.
func repoKeyFor(ctx context.Context, repo *git.Repo, src model.Source) string {
	if repo != nil {
		if remote, err := repo.RemoteURL(ctx, "origin"); err == nil && remote != "" {
			return session.RepoKey(remote)
		}
		return session.RepoKey(repo.Root())
	}
	if src.RepoPath != "" {
		return session.RepoKey(src.RepoPath)
	}
	return session.RepoKey(src.PatchPath)
}

func newSession(repoKey string, src model.Source) *model.ReviewSession {
	now := time.Now().UTC()
	return &model.ReviewSession{
		ID:        session.NewID(src),
		RepoKey:   repoKey,
		Title:     titleFor(src),
		Source:    src,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func titleFor(src model.Source) string {
	switch src.Kind {
	case model.SourceWorktree:
		return "Worktree"
	case model.SourceRange:
		return "Diff " + src.Ref
	case model.SourceCommit:
		return "Commit " + src.Ref
	case model.SourcePatch:
		return "Patch " + filepath.Base(src.PatchPath)
	case model.SourcePR:
		if src.PR != nil {
			if src.PR.Title != "" {
				return src.PR.Title
			}
			return fmt.Sprintf("PR #%d %s/%s", src.PR.Number, src.PR.Owner, src.PR.Repo)
		}
		return "Pull request"
	default:
		return "Review"
	}
}
