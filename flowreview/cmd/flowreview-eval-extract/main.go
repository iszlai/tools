// Command flowreview-eval-extract gathers candidate hunks for the FR-27 eval
// set from this monorepo's own history (taskhound, diagme, mdlite,
// flowreview — personal code, safe to commit). It is a labeling aid, run by
// hand when the set needs refreshing, not part of `make check`: it writes
// draft case files with everything mechanical filled in (hunk body,
// best-effort enclosing function, line counts, candidate symbol names) and
// "expected" left for a human (or the agent that reads each hunk) to write.
//
// Usage (from flowreview/, so relative paths match the Makefile's):
//
//	go run ./cmd/flowreview-eval-extract --out eval/cases
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func main() {
	subdirs := flag.String("subdirs", "taskhound,diagme,mdlite,flowreview", "comma-separated repo-relative directories to mine")
	out := flag.String("out", "eval/cases", "directory to write draft case JSON into")
	minLines := flag.Int("min-lines", 3, "skip hunks with fewer lines than this")
	maxLines := flag.Int("max-lines", 50, "skip hunks with more lines than this")
	maxPerCommit := flag.Int("max-per-commit", 2, "cap hunks taken from one commit")
	maxTotal := flag.Int("max-total", 80, "stop once this many draft cases have been written")
	flag.Parse()

	ctx := context.Background()
	repo, err := git.Open(ctx, ".")
	if err != nil {
		log.Fatalf("open repo: %v", err)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *out, err)
	}

	ts := treesitter.New(repo.Root())
	nextID := 1

	for _, sub := range strings.Split(*subdirs, ",") {
		shas, err := commitSHAs(repo.Root(), sub)
		if err != nil {
			log.Fatalf("list commits under %s: %v", sub, err)
		}
		for _, sha := range shas {
			if nextID > *maxTotal {
				break
			}
			taken, err := extractFromCommit(ctx, repo, ts, sub, sha, *minLines, *maxLines, *maxPerCommit, &nextID, *out)
			if err != nil {
				log.Printf("skip %s %s: %v", sub, sha[:12], err)
				continue
			}
			if taken > 0 {
				fmt.Printf("%s %s: %d case(s)\n", sub, sha[:12], taken)
			}
		}
	}
	fmt.Printf("wrote %d draft case(s) to %s\n", nextID-1, *out)
}

// commitSHAs lists, oldest first, the commits that touch sub under repoRoot.
// internal/core/git.Log takes one rangeExpr with no room for a pathspec, so
// this shells out directly rather than stretching that frozen signature.
func commitSHAs(repoRoot, sub string) ([]string, error) {
	cmd := exec.Command("git", "log", "--reverse", "--pretty=%H", "--", sub)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			shas = append(shas, l)
		}
	}
	return shas, nil
}

// extractFromCommit writes up to maxPerCommit draft cases from sha's diff,
// restricted to modified (not added/deleted/binary) source files under sub.
func extractFromCommit(ctx context.Context, repo *git.Repo, ts *treesitter.Provider, sub, sha string, minLines, maxLines, maxPerCommit int, nextID *int, out string) (int, error) {
	raw, err := repo.CommitDiff(ctx, sha)
	if err != nil {
		return 0, err
	}
	files, err := diff.Parse(raw)
	if err != nil {
		return 0, err
	}

	taken := 0
	for _, f := range files {
		if taken >= maxPerCommit {
			break
		}
		if f.Status != model.FileModified || f.Binary {
			continue
		}
		if !strings.HasPrefix(f.Path(), sub+"/") {
			continue
		}
		if !isSourceFile(f.Path()) {
			continue // skip docs/prose: no real symbols to ground against
		}
		src, err := repo.Show(ctx, sha, f.Path())
		if err != nil {
			continue // e.g. the file was removed later; best effort only
		}

		for _, h := range f.Hunks {
			if taken >= maxPerCommit {
				break
			}
			if len(h.Lines) < minLines || len(h.Lines) > maxLines {
				continue
			}

			enclosing := enclosingFunction(ctx, ts, f.Path(), src, h)
			added, removed := countChanges(h)
			c := draftCase{
				ID:                fmt.Sprintf("%03d", *nextID),
				RepoPath:          sub,
				Commit:            sha,
				Path:              f.Path(),
				Hunk:              model.HunkBody(h.Lines),
				EnclosingFunction: enclosing,
				CandidateSymbols:  candidateSymbols(enclosing, h),
				Expected: draftExpected{
					AddedLines:   added,
					RemovedLines: removed,
				},
			}
			if err := writeDraft(out, c); err != nil {
				return taken, err
			}
			*nextID++
			taken++
		}
	}
	return taken, nil
}

// isSourceFile restricts extraction to files with real symbols to ground
// against: source and config, not prose documentation.
func isSourceFile(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "makefile", "dockerfile", "go.mod", "go.sum":
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".py", ".rs", ".yaml", ".yml":
		return true
	}
	return false
}

// enclosingFunction asks the tree-sitter structural provider for the
// function containing h's first line with content on the new side, falling
// back to "" for a hunk that only deletes lines. Best effort: an unsupported
// language or a miss also returns "".
func enclosingFunction(ctx context.Context, ts *treesitter.Provider, path string, newSrc []byte, h model.DiffHunk) string {
	if !ts.Supports(path) {
		return ""
	}
	line := 0
	for _, l := range h.Lines {
		if l.NewNo > 0 {
			line = l.NewNo
			break
		}
	}
	if line == 0 {
		return "" // pure deletion hunk; the old-side function text is not worth the extra git calls here
	}
	sym, err := ts.FunctionAt(ctx, path, newSrc, line)
	if err != nil || sym == nil {
		return ""
	}
	lines := strings.Split(string(newSrc), "\n")
	if sym.Range.StartLine < 1 || sym.Range.EndLine > len(lines) {
		return ""
	}
	return strings.Join(lines[sym.Range.StartLine-1:sym.Range.EndLine], "\n")
}

func countChanges(h model.DiffHunk) (added, removed int) {
	for _, l := range h.Lines {
		switch l.Kind {
		case model.LineAdd:
			added++
		case model.LineDel:
			removed++
		}
	}
	return added, removed
}

// identifierRe is deliberately broader than eval.LooksLikeSymbol: it is only
// used to propose candidates for the human labeler to keep or discard when
// writing Expected.Symbols, not to score anything itself.
var identifierRe = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]{2,}\b`)

// candidateSymbols proposes identifiers worth considering for
// Expected.Symbols: names mentioned in the enclosing function's signature
// line plus any added/removed line, deduplicated and capped so the draft
// stays readable.
func candidateSymbols(enclosing string, h model.DiffHunk) []string {
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		for _, tok := range identifierRe.FindAllString(text, -1) {
			if seen[tok] || isCommonWord(tok) {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	if sig, _, ok := strings.Cut(enclosing, "\n"); ok {
		add(sig)
	} else {
		add(enclosing)
	}
	for _, l := range h.Lines {
		if l.Kind != model.LineContext {
			add(l.Text)
		}
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func isCommonWord(tok string) bool {
	switch strings.ToLower(tok) {
	case "the", "and", "for", "return", "func", "var", "const", "import", "package",
		"true", "false", "nil", "err", "error", "string", "int", "bool", "struct",
		"interface", "type", "if", "else", "range", "ctx", "context":
		return true
	}
	return false
}

// draftCase/draftExpected mirror eval.Case/eval.Expected but leave Kind and
// Facts out of the JSON entirely (rather than present-but-empty) so a draft
// file is visibly incomplete until a human fills them in.
type draftCase struct {
	ID                string        `json:"id"`
	RepoPath          string        `json:"repo_path"`
	Commit            string        `json:"commit"`
	Path              string        `json:"path"`
	Hunk              string        `json:"hunk"`
	EnclosingFunction string        `json:"enclosing_function,omitempty"`
	CandidateSymbols  []string      `json:"candidate_symbols,omitempty"` // for the labeler; not part of eval.Case
	Expected          draftExpected `json:"expected"`
}

type draftExpected struct {
	AddedLines   int `json:"added_lines"`
	RemovedLines int `json:"removed_lines"`
}

func writeDraft(dir string, c draftCase) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), data, 0o644)
}
