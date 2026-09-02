package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// GitHubLink is what the board remembers about an issue that has been pushed to
// GitHub. Without it a second sync would file everything again, so it is
// written back as soon as an issue is created rather than at the end of a run.
type GitHubLink struct {
	Number int `yaml:"number" json:"number"`
	// Comments posted so far. taskhound comments are append-only, so a count is
	// enough to know which ones are new without fingerprinting each body.
	Comments int `yaml:"comments,omitempty" json:"comments,omitempty"`
}

// ghRunner runs the gh CLI. It is a variable so the tests can drive sync
// without a network, a token, or a repo to scribble on.
type ghRunner func(args ...string) (string, error)

func runGH(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func cmdSync(args []string) error {
	fs, file := newFS("sync")
	repo := fs.String("repo", "", "target repository as owner/name (default: the one gh infers here)")
	dryRun := fs.Bool("dry-run", false, "report what would happen, call nothing")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("gh"); err != nil && !*dryRun {
		return fmt.Errorf("sync needs the gh CLI on PATH (https://cli.github.com)")
	}
	return syncBoard(s, *repo, *dryRun, runGH, os.Stdout)
}

// syncBoard pushes the board to GitHub Issues: one way, and safe to run again.
//
// Issues go up in dependency order so that by the time a blocked issue is
// filed, its blockers already have numbers to reference. GitHub has no native
// blocking relation in the API, so the edge is text in the body — the graph
// stays authoritative here, and GitHub gets a readable shadow of it.
func syncBoard(s *Store, repo string, dryRun bool, run ghRunner, out io.Writer) error {
	board, err := s.Read()
	if err != nil {
		return err
	}
	if len(board.Issues) == 0 {
		fmt.Fprintln(out, "nothing to sync")
		return nil
	}

	repoArgs := []string{}
	if repo != "" {
		repoArgs = append(repoArgs, "--repo", repo)
	}

	created, updated, commented := 0, 0, 0
	for _, is := range syncOrder(board) {
		body := issueBody(board, is)
		labels := githubLabels(is)

		if is.GitHub == nil {
			if dryRun {
				fmt.Fprintf(out, "create   %s  %s\n", is.ID, is.Title)
				created++
				continue
			}
			for _, l := range labels {
				// --force turns "already exists" into a no-op, which is what we
				// want: the label only has to be there, not to be ours.
				if _, err := run(append([]string{"label", "create", l, "--force"}, repoArgs...)...); err != nil {
					return err
				}
			}
			args := append([]string{"issue", "create", "--title", is.Title, "--body", body}, repoArgs...)
			for _, l := range labels {
				args = append(args, "--label", l)
			}
			url, err := run(args...)
			if err != nil {
				return err
			}
			n, err := issueNumberFromURL(url)
			if err != nil {
				return err
			}
			// Record the number before anything else can fail, so a crash costs
			// a half-populated issue rather than a duplicate on the next run.
			if err := s.Update(func(b *Board) error {
				cur, err := b.Get(is.ID)
				if err != nil {
					return err
				}
				cur.GitHub = &GitHubLink{Number: n}
				return nil
			}); err != nil {
				return err
			}
			is.GitHub = &GitHubLink{Number: n}
			fmt.Fprintf(out, "created  %s -> #%d  %s\n", is.ID, n, is.Title)
			created++
		} else {
			if dryRun {
				fmt.Fprintf(out, "update   %s -> #%d  %s\n", is.ID, is.GitHub.Number, is.Title)
				updated++
			} else {
				num := strconv.Itoa(is.GitHub.Number)
				args := append([]string{"issue", "edit", num, "--title", is.Title, "--body", body}, repoArgs...)
				if _, err := run(args...); err != nil {
					return err
				}
				fmt.Fprintf(out, "updated  %s -> #%d\n", is.ID, is.GitHub.Number)
				updated++
			}
		}

		if is.GitHub == nil {
			continue // dry run, nothing to hang comments or state off
		}
		num := strconv.Itoa(is.GitHub.Number)

		// Only comments the board has that GitHub has not seen.
		pending := is.Comments[min(is.GitHub.Comments, len(is.Comments)):]
		for _, c := range pending {
			if dryRun {
				commented++
				continue
			}
			text := c.Body
			if c.Author != "" {
				text = fmt.Sprintf("**%s** wrote on the taskhound board:\n\n%s", c.Author, c.Body)
			}
			if _, err := run(append([]string{"issue", "comment", num, "--body", text}, repoArgs...)...); err != nil {
				return err
			}
			commented++
		}
		if !dryRun && len(pending) > 0 {
			posted := len(is.Comments)
			if err := s.Update(func(b *Board) error {
				cur, err := b.Get(is.ID)
				if err != nil {
					return err
				}
				if cur.GitHub != nil {
					cur.GitHub.Comments = posted
				}
				return nil
			}); err != nil {
				return err
			}
		}

		// done closes the issue, anything else keeps it open.
		if !dryRun {
			verb := "reopen"
			if is.Status == StatusDone {
				verb = "close"
			}
			state, err := run(append([]string{"issue", "view", num, "--json", "state", "--jq", ".state"}, repoArgs...)...)
			if err != nil {
				return err
			}
			wantClosed := is.Status == StatusDone
			isClosed := strings.EqualFold(strings.TrimSpace(state), "CLOSED")
			if wantClosed != isClosed {
				if _, err := run(append([]string{"issue", verb, num}, repoArgs...)...); err != nil {
					return err
				}
				fmt.Fprintf(out, "%sd  %s -> #%s\n", verb, is.ID, num)
			}
		}
	}

	what := "synced"
	if dryRun {
		what = "would sync"
	}
	fmt.Fprintf(out, "%s: %d created, %d updated, %d comment(s)\n", what, created, updated, commented)
	return nil
}

// syncOrder returns the issues blockers-first, so every "Blocked by #N" in a
// body refers to an issue that already has a number. The graph is a DAG by
// construction, so this always terminates having emitted everything.
func syncOrder(b *Board) []*Issue {
	emitted := map[string]bool{}
	var out []*Issue
	remaining := append([]*Issue{}, b.Issues...)
	for len(remaining) > 0 {
		var deferred []*Issue
		progress := false
		for _, is := range remaining {
			ready := true
			for _, dep := range is.BlockedBy {
				if !emitted[dep] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, is)
				emitted[is.ID] = true
				progress = true
			} else {
				deferred = append(deferred, is)
			}
		}
		if !progress {
			return append(out, deferred...)
		}
		remaining = deferred
	}
	return out
}

// issueBody renders the GitHub body: the description, the blocking edges as
// issue references, and a backlink saying which board the issue came from.
func issueBody(b *Board, is *Issue) string {
	var sb strings.Builder
	if is.Description != "" {
		sb.WriteString(is.Description)
		sb.WriteString("\n\n")
	}
	if refs := blockerRefs(b, is); refs != "" {
		fmt.Fprintf(&sb, "**Blocked by:** %s\n\n", refs)
	}
	fmt.Fprintf(&sb, "---\ntaskhound: `%s`\n", is.ID)
	return sb.String()
}

// blockerRefs names each blocker by its GitHub number where it has one, and by
// its taskhound id where it does not — which happens only if the board was
// edited between the blocker being filed and this issue being pushed.
func blockerRefs(b *Board, is *Issue) string {
	var refs []string
	for _, dep := range is.BlockedBy {
		if d, err := b.Get(dep); err == nil && d.GitHub != nil {
			refs = append(refs, fmt.Sprintf("#%d", d.GitHub.Number))
			continue
		}
		refs = append(refs, dep)
	}
	return strings.Join(refs, ", ")
}

// githubLabels carries the board's own labels, plus "doing" — without it the
// open/closed mapping would lose the distinction between work not started and
// work in flight.
func githubLabels(is *Issue) []string {
	labels := append([]string{}, is.Labels...)
	if is.Status == StatusDoing && !hasString(labels, StatusDoing) {
		labels = append(labels, StatusDoing)
	}
	return labels
}

func issueNumberFromURL(url string) (int, error) {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0, fmt.Errorf("cannot read an issue number out of %q", url)
	}
	n, err := strconv.Atoi(strings.TrimSpace(url[i+1:]))
	if err != nil {
		return 0, fmt.Errorf("cannot read an issue number out of %q", url)
	}
	return n, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
