// Package eval is the FR-27 harness: a fixed set of real hunks from this
// monorepo's own history, hand-labeled with the objective facts a correct
// structured summary (internal/llm/summary) must get right, and a scorer that
// checks a model's output against them. cmd/flowreview-eval runs it against
// the catalog and writes the markdown report under eval/reports/.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Case is one hand-labeled hunk. Hunk is the normalized body
// (model.HunkBody: one line per diff line, "+"/"-"/" " prefix, no "@@"
// header) and EnclosingFunction is best effort — empty when no tree-sitter
// grammar covers the file or none was found.
type Case struct {
	ID                string   `json:"id"`
	RepoPath          string   `json:"repo_path"` // subdirectory of the monorepo this hunk came from, e.g. "taskhound"
	Commit            string   `json:"commit"`
	Path              string   `json:"path"`
	Hunk              string   `json:"hunk"`
	EnclosingFunction string   `json:"enclosing_function,omitempty"`
	Expected          Expected `json:"expected"`
}

// Expected is what a correct structured summary of a Case must get right.
// Facts are short, objective, independently checkable statements written by
// the labeler reading the hunk — not graded automatically (there is no
// generic fact-checker here), but printed in the report for a human to
// spot-check a model's answer against.
type Expected struct {
	Kind         string   `json:"kind"` // behavior | refactor | rename | test | config
	Symbols      []string `json:"symbols"`
	AddedLines   int      `json:"added_lines"`
	RemovedLines int      `json:"removed_lines"`
	Facts        []string `json:"facts"`
}

// LoadCases reads every "*.json" file directly under dir as a Case, sorted by
// filename so a run is reproducible. A Case with no "id" field gets its
// filename (without ".json") as its id.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: load cases from %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	cases := make([]Case, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("eval: %w", err)
		}
		var c Case
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("eval: %s: %w", name, err)
		}
		if c.ID == "" {
			c.ID = strings.TrimSuffix(name, ".json")
		}
		cases = append(cases, c)
	}
	return cases, nil
}
