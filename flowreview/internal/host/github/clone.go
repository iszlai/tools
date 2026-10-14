package github

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// runGitFetch and cloneRepo are package vars so tests can exercise
// ResolveClone's branching (use-cwd vs. use-cache, clone-once) without any
// network access or a real git/gh binary.
var runGitFetch = func(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("github: git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return nil
}

var cloneRepo = func(ctx context.Context, owner, repo, dir string) error {
	cmd := exec.CommandContext(ctx, "gh", "repo", "clone", owner+"/"+repo, dir, "--", "--filter=blob:none")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("github: gh repo clone %s/%s: %s", owner, repo, strings.TrimSpace(errb.String()))
	}
	return nil
}

// fetchPR brings a PR's commits into dir without touching its worktree.
// Fetching refs/pull/N/head gets the PR's own commits; a second, plain fetch
// of the default refspec brings in the current base-branch tip, so the PR's
// actual base commit — an ancestor of it in the normal case — is present too,
// without this function having to know the base branch's name.
func fetchPR(ctx context.Context, dir string, n int) error {
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/remotes/origin/pr/%d/head", n, n)
	if err := runGitFetch(ctx, dir, "fetch", "--no-tags", "origin", refspec); err != nil {
		return err
	}
	return runGitFetch(ctx, dir, "fetch", "--no-tags", "origin")
}

// ResolveClone returns a local git repository that has ref's PR fetched into
// it, never checking out or otherwise modifying any worktree. When cwd is
// inside a repo whose origin matches ref's owner/repo (comparing ssh/https
// forms normalized), that repo is reused. Otherwise ref's repo is cloned
// once into cacheRoot/repos/<owner>/<repo> (via `gh repo clone ... --
// --filter=blob:none`) and reused on later calls.
func ResolveClone(ctx context.Context, ref model.PRRef, cwd, cacheRoot string) (string, error) {
	if cwd != "" {
		if repoPath, ok := tryLocalOrigin(ctx, ref, cwd); ok {
			if err := fetchPR(ctx, repoPath, ref.Number); err != nil {
				return "", err
			}
			return repoPath, nil
		}
	}

	dir := filepath.Join(cacheRoot, "repos", ref.Owner, ref.Repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", fmt.Errorf("github: create cache dir %q: %w", filepath.Dir(dir), err)
		}
		if err := cloneRepo(ctx, ref.Owner, ref.Repo, dir); err != nil {
			return "", err
		}
	}
	if err := fetchPR(ctx, dir, ref.Number); err != nil {
		return "", err
	}
	return dir, nil
}

// tryLocalOrigin reports whether cwd is inside a git repo whose "origin"
// remote names the same owner/repo as ref, returning that repo's root.
func tryLocalOrigin(ctx context.Context, ref model.PRRef, cwd string) (string, bool) {
	repo, err := git.Open(ctx, cwd)
	if err != nil {
		return "", false
	}
	originURL, err := repo.RemoteURL(ctx, "origin")
	if err != nil {
		return "", false
	}
	owner, name, err := ownerRepoFromRemote(originURL)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(owner, ref.Owner) || !strings.EqualFold(name, ref.Repo) {
		return "", false
	}
	return repo.Root(), true
}

// OpenPR fetches a PR's metadata and diff from h, and resolves a local clone
// with its commits fetched, returning everything a session needs to open it.
// internal/core/review (another slice, not yet on main) is expected to call
// this rather than this package wiring itself into review opening.
func OpenPR(ctx context.Context, h *Client, ref model.PRRef, cwd, cacheRoot string) (model.Source, []byte, *host.PullRequest, error) {
	pr, err := h.FetchPR(ctx, ref)
	if err != nil {
		return model.Source{}, nil, nil, err
	}
	diff, err := h.FetchDiff(ctx, ref)
	if err != nil {
		return model.Source{}, nil, nil, err
	}
	repoPath, err := ResolveClone(ctx, ref, cwd, cacheRoot)
	if err != nil {
		return model.Source{}, nil, nil, err
	}

	refCopy := ref
	src := model.Source{
		Kind:     model.SourcePR,
		RepoPath: repoPath,
		Ref:      fmt.Sprintf("#%d", ref.Number),
		BaseSHA:  pr.BaseSHA,
		HeadSHA:  pr.HeadSHA,
		PR:       &refCopy,
	}
	return src, diff, pr, nil
}
