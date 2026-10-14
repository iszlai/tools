package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/model"
)

const sessionsUsage = `usage: flowreview sessions [--json] [--all]

  (no flags)  list sessions for the repository in the current directory
  --all       list sessions across every repository
  --json      machine-readable output
`

// sessionListView is one row of `flowreview sessions`. TotalHunks is nil
// when recomputing it would need something other than a local, offline git
// diff (see progressFor) -- "if cheap" from FR-12's brief.
type sessionListView struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Source        string    `json:"source"`
	Updated       time.Time `json:"updated"`
	ReviewedHunks int       `json:"reviewed_hunks"`
	TotalHunks    *int      `json:"total_hunks,omitempty"`
}

// runSessionsCLI implements `flowreview sessions [--json] [--all]`.
func runSessionsCLI(args []string, stdout, stderr io.Writer) int {
	var jsonOut, all bool
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--all":
			all = true
		case "-h", "--help":
			fmt.Fprint(stdout, sessionsUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "flowreview sessions: unknown argument %q\n\n%s", a, sessionsUsage)
			return 2
		}
	}

	store := session.New(session.DefaultRoot())
	var sessions []*model.ReviewSession
	var err error
	if all {
		sessions, err = store.ListAll()
	} else {
		repoKey, rerr := currentRepoKey()
		if rerr != nil {
			fmt.Fprintln(stderr, "flowreview sessions:", rerr)
			return exitCode(rerr)
		}
		sessions, err = store.List(repoKey)
	}
	if err != nil {
		fmt.Fprintln(stderr, "flowreview sessions:", err)
		return 1
	}

	views := make([]sessionListView, len(sessions))
	for i, s := range sessions {
		views[i] = buildSessionListView(s)
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(views); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if len(views) == 0 {
		fmt.Fprintln(stdout, "no sessions")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tSOURCE\tUPDATED\tPROGRESS")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.ID, v.Title, v.Source, v.Updated.Format(time.RFC3339), progressText(v))
	}
	tw.Flush()
	return 0
}

func progressText(v sessionListView) string {
	if v.TotalHunks == nil {
		return fmt.Sprintf("%d/?", v.ReviewedHunks)
	}
	return fmt.Sprintf("%d/%d", v.ReviewedHunks, *v.TotalHunks)
}

// currentRepoKey is session.RepoKey for the repository containing cwd, the
// same identity review.Open would file a session under.
func currentRepoKey() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repo, err := git.Open(context.Background(), cwd)
	if err != nil {
		return "", notFoundErr(fmt.Errorf("%q is not a git repository", cwd))
	}
	if remote, err := repo.RemoteURL(context.Background(), "origin"); err == nil && remote != "" {
		return session.RepoKey(remote), nil
	}
	return session.RepoKey(repo.Root()), nil
}

func buildSessionListView(s *model.ReviewSession) sessionListView {
	var reviewed int
	for _, st := range s.States {
		if st.Status == model.HunkReviewed {
			reviewed++
		}
	}

	view := sessionListView{
		ID:            s.ID,
		Title:         s.Title,
		Source:        sourceSummary(s.Source),
		Updated:       s.UpdatedAt,
		ReviewedHunks: reviewed,
	}
	if files, ok := localDiff(s.Source); ok {
		total := 0
		for _, f := range files {
			total += len(f.Hunks)
		}
		view.TotalHunks = &total
	}
	return view
}

func sourceSummary(src model.Source) string {
	switch src.Kind {
	case model.SourceWorktree:
		return "worktree"
	case model.SourceRange, model.SourceCommit:
		return string(src.Kind) + " " + src.Ref
	case model.SourcePatch:
		return "patch " + filepath.Base(src.PatchPath)
	case model.SourcePR:
		if src.PR != nil {
			return fmt.Sprintf("pr %s/%s#%d", src.PR.Owner, src.PR.Repo, src.PR.Number)
		}
		return "pr"
	default:
		return string(src.Kind)
	}
}

// localDiff recomputes a session's current file/hunk list without ever
// touching the network: a local git diff is cheap enough to run for every row
// of `flowreview sessions`. For a PR session it diffs the base/head SHAs
// already fetched into its clone (set by fetchPRHook) -- still entirely
// local; it never calls GitHub. ok is false whenever that isn't possible
// (repo gone, patch file moved, PR session missing its clone or SHAs), and
// the caller falls back to showing just the reviewed count.
func localDiff(src model.Source) ([]model.ChangedFile, bool) {
	ctx := context.Background()
	switch src.Kind {
	case model.SourceWorktree, model.SourceCommit, model.SourceRange:
		repo, err := git.Open(ctx, src.RepoPath)
		if err != nil {
			return nil, false
		}
		var raw []byte
		switch src.Kind {
		case model.SourceWorktree:
			raw, err = repo.WorktreeDiff(ctx)
		case model.SourceCommit:
			raw, err = repo.CommitDiff(ctx, src.Ref)
		case model.SourceRange:
			raw, err = repo.RangeDiff(ctx, src.Ref)
		}
		if err != nil {
			return nil, false
		}
		files, err := diff.Parse(raw)
		return files, err == nil

	case model.SourcePatch:
		raw, err := git.PatchFile(src.PatchPath)
		if err != nil {
			return nil, false
		}
		files, err := diff.Parse(raw)
		return files, err == nil

	case model.SourcePR:
		if src.RepoPath == "" || src.BaseSHA == "" || src.HeadSHA == "" {
			return nil, false
		}
		repo, err := git.Open(ctx, src.RepoPath)
		if err != nil {
			return nil, false
		}
		raw, err := repo.RangeDiff(ctx, src.BaseSHA+".."+src.HeadSHA)
		if err != nil {
			return nil, false
		}
		files, err := diff.Parse(raw)
		return files, err == nil

	default:
		return nil, false
	}
}
