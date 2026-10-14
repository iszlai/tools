// Package git reads diffs, revisions and content from a repository by
// shelling out to the git binary (no libgit). Every method is read-only: this
// tool never stages, commits, checks out or otherwise writes to the repo it
// is reviewing.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// emptyTreeSHA is git's well-known hash of an empty tree. Diffing a root
// commit (one with no parent) against it renders every file as an addition,
// the same shape as any other commit's diff.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// diffArgs are common to every unified-diff command: no color or external
// diff tool (so output is stable regardless of the user's git config), rename
// detection, and full (untruncated) blob hashes in index lines.
var diffArgs = []string{"--no-color", "--no-ext-diff", "-M", "--full-index"}

// Repo is a git repository rooted at an absolute path, as found by
// `git rev-parse --show-toplevel`.
type Repo struct {
	root string
}

// Open finds the toplevel of the repository containing path and returns a
// Repo rooted there. path may be the root itself or any subdirectory of it.
func Open(ctx context.Context, path string) (*Repo, error) {
	scratch := &Repo{root: path}
	out, err := scratch.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("git: open %q: %w", path, err)
	}
	return &Repo{root: strings.TrimSpace(string(out))}, nil
}

// Root is the absolute path to the repository's working tree.
func (r *Repo) Root() string { return r.root }

// Error wraps a failed git invocation with its stderr, which is almost always
// more useful to a caller than the bare exec.ExitError.
type Error struct {
	Args   []string
	Stderr string
	err    error
}

func (e *Error) Error() string {
	if msg := strings.TrimSpace(e.Stderr); msg != "" {
		return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), e.err)
}

func (e *Error) Unwrap() error { return e.err }

// run executes git with args, rooted at r.root, and returns stdout. A
// non-zero exit becomes an *Error carrying stderr.
func (r *Repo) run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runTolerant(ctx, nil, args...)
}

// runTolerant is run, except exit codes listed in ok are not treated as
// errors. `git diff --no-index` uses exit code 1 to mean "the files differ",
// not failure, which is the normal case when synthesizing an untracked file's
// diff.
func (r *Repo) runTolerant(ctx context.Context, ok []int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			for _, code := range ok {
				if exitErr.ExitCode() == code {
					return stdout.Bytes(), nil
				}
			}
		}
		return nil, &Error{Args: args, Stderr: stderr.String(), err: err}
	}
	return stdout.Bytes(), nil
}

// WorktreeDiff returns the unified diff of uncommitted changes against HEAD,
// including untracked files synthesized as additions against /dev/null. It
// never stages anything (no `git add -N`) and never touches the index.
func (r *Repo) WorktreeDiff(ctx context.Context) ([]byte, error) {
	tracked, err := r.run(ctx, append(append([]string{"diff"}, diffArgs...), "HEAD")...)
	if err != nil {
		return nil, err
	}

	paths, err := r.untrackedPaths(ctx)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(tracked)
	for _, p := range paths {
		d, err := r.untrackedDiff(ctx, p)
		if err != nil {
			return nil, err
		}
		if buf.Len() > 0 && !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.Write(d)
	}
	return buf.Bytes(), nil
}

// untrackedPaths lists files git sees but does not track, honoring
// .gitignore. It reads the working tree only; it does not touch the index.
func (r *Repo) untrackedPaths(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, string(p))
	}
	return paths, nil
}

// untrackedDiff synthesizes the diff of an untracked file as a pure addition,
// using `git diff --no-index` against /dev/null. This never adds the file to
// the index.
func (r *Repo) untrackedDiff(ctx context.Context, relPath string) ([]byte, error) {
	args := append(append([]string{"diff"}, diffArgs...), "--no-index", "--", os.DevNull, relPath)
	// Exit code 1 means "the files differ", which they always do here.
	return r.runTolerant(ctx, []int{1}, args...)
}

// RangeDiff returns the diff for a range expression exactly as git parses it:
// "A..B" diffs the two revisions directly; "A...B" diffs B against the
// merge-base of A and B. git computes the distinction itself.
func (r *Repo) RangeDiff(ctx context.Context, rangeExpr string) ([]byte, error) {
	return r.run(ctx, append(append([]string{"diff"}, diffArgs...), rangeExpr)...)
}

// CommitDiff returns the diff of rev against its first parent, or against the
// empty tree when rev is a root commit.
func (r *Repo) CommitDiff(ctx context.Context, rev string) ([]byte, error) {
	sha, err := r.ResolveSHA(ctx, rev)
	if err != nil {
		return nil, err
	}
	parent, err := r.firstParent(ctx, sha)
	if err != nil {
		return nil, err
	}
	return r.run(ctx, append(append([]string{"diff"}, diffArgs...), parent, sha)...)
}

// firstParent returns the first parent of sha, or emptyTreeSHA when sha is a
// root commit.
func (r *Repo) firstParent(ctx context.Context, sha string) (string, error) {
	out, err := r.run(ctx, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return emptyTreeSHA, nil
	}
	return fields[1], nil
}

// Show returns the content of path as it was at rev.
func (r *Repo) Show(ctx context.Context, rev, path string) ([]byte, error) {
	return r.run(ctx, "show", rev+":"+path)
}

// ResolveSHA resolves rev (a branch, tag, short sha, "HEAD~2", ...) to its
// full commit sha.
func (r *Repo) ResolveSHA(ctx context.Context, rev string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("git: resolve %q: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadSHA resolves HEAD.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	return r.ResolveSHA(ctx, "HEAD")
}

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.run(ctx, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteURL returns the fetch URL of the named remote (typically "origin").
func (r *Repo) RemoteURL(ctx context.Context, name string) (string, error) {
	out, err := r.run(ctx, "remote", "get-url", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentBranch returns the checked-out branch name, or "HEAD" when detached.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsBinaryDiffLine reports whether line is git's marker for a binary file,
// e.g. "Binary files a/old.png and b/new.png differ" (or "/dev/null" on
// either side for an add or delete). The diff parser (FR-3) uses this to flag
// a file as binary without reimplementing git's own content sniffing.
func IsBinaryDiffLine(line string) bool {
	return strings.HasPrefix(line, "Binary files ") &&
		strings.HasSuffix(line, " differ") &&
		strings.Contains(line, " and ")
}
