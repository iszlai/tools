# internal/host/github

GitHub implementation of `internal/host.Host` (FR-10 read; FR-11 publish is
stubbed — `SubmitReview` and `PublishComment` return an error until that
slice lands).

## Token resolution

`New(Options{})` resolves a token in this order:

1. `Options.Token`
2. `GITHUB_TOKEN`
3. `GH_TOKEN`
4. `gh auth token` (requires the [gh CLI](https://cli.github.com), logged in)

If none of these work, `New` returns an error with an install/login hint
instead of a cryptic HTTP 401 later.

## PR references

- `ParsePRRef` accepts a full URL (`https://github.com/o/r/pull/123`) or the
  `o/r#123` shorthand.
- A bare number (`123`) needs a repo to belong to — use
  `ParsePRRefWithRepo(s, originURL)`, passing the `origin` remote URL of the
  local repo (any form: `https://`, `git@host:`, `ssh://`).

## Local clone resolution

`ResolveClone` never checks out or otherwise modifies a worktree — it only
runs `git fetch`. If `cwd` is inside a repo whose `origin` matches the PR's
owner/repo, that repo is fetched into directly. Otherwise the repo is cloned
once (`gh repo clone owner/repo <cacheRoot>/repos/owner/repo --
--filter=blob:none`) and fetched into on later calls.

`OpenPR(ctx, h, ref, cwd, cacheRoot)` ties `FetchPR`, `FetchDiff` and
`ResolveClone` together into a `model.Source` + diff + `host.PullRequest`
ready for a session to open. `internal/core/review` (a later slice, not on
`main` yet) is expected to call it rather than this package wiring itself
into review opening.

## Manual check against a real PR

The automated tests use recorded HTTP fixtures under `testdata/` — no
network, no `gh` binary required. To check this package against the real
GitHub API by hand (read-only GETs only), with `gh` installed and logged in:

```sh
cd flowreview
go test -tags manual ./internal/host/github/... -run TestManualCheck_realPR -v
```

This fetches the latest PR on `cli/cli` (or edit the URL in
`manualcheck_test.go` to point at a different public PR) and prints its
metadata, diff size, comment count and review count. The `manual` build tag
keeps it out of `go test ./...` and `make check`.

### Last run

Checked against `https://github.com/cli/cli/pull/14679` on 2026-10-14:
PR metadata, a 2687-byte diff, 1 comment and 1 review were fetched
successfully.
