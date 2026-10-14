package summary

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Fallback builds a deterministic summary for in when no model is
// configured, the provider failed, or it returned output that didn't match
// Schema: PLAN.md §5 FR-28's "+A/-B lines" text, located by whatever name
// EnclosingFunction's first line gives up, or the file's base name
// otherwise. Kind is guessed from the path and language alone (test/config),
// never "behavior" vs. "refactor" vs. "rename" -- telling those apart is
// exactly the judgment call a fallback, by definition, cannot make.
func Fallback(key, hunkID string, in Input) model.SnippetSummary {
	added, removed := countHunkLines(in.Hunk)
	loc := enclosingName(in.EnclosingFunction)
	if loc == "" {
		loc = filepath.Base(in.Path)
	}
	return model.SnippetSummary{
		Key:           key,
		HunkID:        hunkID,
		ModelID:       "fallback",
		PromptVersion: PromptVersion,
		Kind:          guessKind(in.Path, in.Language),
		What:          fmt.Sprintf("%s: +%d/-%d lines", loc, added, removed),
		Confidence:    0,
		Fallback:      true,
		CreatedAt:     time.Now().UTC(),
	}
}

// countHunkLines counts added/removed lines in a model.HunkBody string: one
// line per diff line, prefixed '+', '-' or ' '.
func countHunkLines(hunkBody string) (added, removed int) {
	for _, line := range strings.Split(hunkBody, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// configLanguages are internal/lang.Detect's names for build/deploy/config
// files -- no application logic, so Fallback (and a real model, per
// prompt.go's systemPrompt) calls these "config" rather than "behavior".
var configLanguages = map[string]bool{
	"yaml": true, "json": true, "toml": true, "hcl": true,
	"dockerfile": true, "make": true, "gomod": true, "shell": true,
}

// guessKind is Fallback's path/language heuristic: a test file (by name
// convention) is "test", a build/deploy/config file is "config", everything
// else defaults to "behavior" -- the safest default since nothing here can
// tell a true behavior change from a refactor or a rename.
func guessKind(path, language string) string {
	base := strings.ToLower(filepath.Base(path))
	if isTestPath(base, language) {
		return "test"
	}
	if configLanguages[language] {
		return "config"
	}
	return "behavior"
}

// isTestPath recognizes the common test-file naming conventions across the
// languages FlowReview reviews: Go's "_test.go", Java/Kotlin/JS/TS's
// "*Test"/"*.test."/"*.spec.", Python's "test_*.py", and Gherkin features
// (exercised through step definitions, not asserted directly, but close
// enough in spirit to label "test").
func isTestPath(base, language string) bool {
	switch {
	case strings.Contains(base, "_test."),
		strings.Contains(base, ".test."),
		strings.Contains(base, "_spec."),
		strings.Contains(base, ".spec."),
		strings.HasPrefix(base, "test_"):
		return true
	case language == "gherkin":
		return true
	default:
		return false
	}
}
