package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/order"
	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
)

const orderUsage = `flowreview order [--json] [<ref|range>]   print the diff in flow order

  flowreview order                 the worktree diff (cwd)
  flowreview order <ref>           one commit against its parent
  flowreview order <A..B|A...B>    a commit range

Go and Gherkin analysis run synchronously and may take a few seconds on a
large repository. --json is the machine-readable form FR-24 documents for
agents; the default is one screen of plain text (order.Text).
`

// runOrderCLI implements "flowreview order": FR-17's flow ordering over a
// local diff, as plain text or JSON. FR-24 (not this slice) is expected to
// grow this into the fuller order|context|radius|tests agent surface; this
// is deliberately just enough to exercise order.Flow end to end today.
func runOrderCLI(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	var ref string
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, orderUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "flowreview order: unknown flag %q\n", a)
			return 2
		case ref == "":
			ref = a
		default:
			fmt.Fprintf(stderr, "flowreview order: unexpected argument %q\n", a)
			return 2
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "flowreview order:", err)
		return 1
	}

	o, err := buildOrder(context.Background(), cwd, ref)
	if err != nil {
		fmt.Fprintln(stderr, "flowreview order:", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(o); err != nil {
			fmt.Fprintln(stderr, "flowreview order:", err)
			return 1
		}
		return 0
	}
	order.Text(o, stdout)
	return 0
}

// buildOrder loads the diff for ref (or the worktree, when ref is empty)
// rooted at repoPath, runs Go and Gherkin analysis when the repo has any,
// and returns FR-17's flow order. Analysis failures degrade silently to a
// flatter order rather than failing the command -- both inputs are
// optional by design (see order.Inputs).
func buildOrder(ctx context.Context, repoPath, ref string) (order.Order, error) {
	repo, err := git.Open(ctx, repoPath)
	if err != nil {
		return order.Order{}, fmt.Errorf("%q is not a git repository: %w", repoPath, err)
	}

	var raw []byte
	switch {
	case ref == "":
		raw, err = repo.WorktreeDiff(ctx)
	case strings.Contains(ref, ".."):
		raw, err = repo.RangeDiff(ctx, ref)
	default:
		raw, err = repo.CommitDiff(ctx, ref)
	}
	if err != nil {
		return order.Order{}, err
	}

	files, err := diff.Parse(raw)
	if err != nil {
		return order.Order{}, fmt.Errorf("parse diff: %w", err)
	}

	in := order.Inputs{Files: files}

	var goFiles []string
	for _, f := range files {
		if strings.HasSuffix(f.Path(), ".go") {
			goFiles = append(goFiles, f.Path())
		}
	}
	if len(goFiles) > 0 {
		loadCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		l := &golang.Loader{Dir: repo.Root()}
		if res, lerr := l.Load(loadCtx, goFiles); lerr == nil {
			in.Go = res
		}
		if cfg, cerr := golang.LoadConfig(repo.Root()); cerr == nil {
			in.GoConfig = cfg
		}
	}

	if ix, gerr := gherkin.Build(ctx, repo.Root()); gerr == nil {
		in.Gherkin = ix
	}

	return order.Flow(in), nil
}
