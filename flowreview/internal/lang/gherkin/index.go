// Package gherkin links Gherkin .feature files to the step definitions that
// implement them. It builds on the structural Gherkin outline from
// internal/lang/treesitter (FR-39: Feature/Scenario/Step symbols from a
// hand-written parser, since no usable tree-sitter Gherkin binding exists)
// and on treesitter.Provider.Outline/FunctionAt to resolve a step
// definition's handler to a symbol id when the handler is a plain identifier
// or an inline closure.
//
// Step definitions are discovered across the repo by regex over source
// text, not a resolved AST: godog's ctx.Step/Given/When/Then (Go),
// cucumber-jvm's @Given/@When/@Then/@And annotations (Java), and
// cucumber-js's Given/When/Then(pattern, fn) calls (JS/TS). Cucumber
// expressions ({int}, {float}, {string}, {word}) are translated to Go
// regexps; a pattern that already starts with "^" is compiled as a regex
// as-is, which covers godog (always regex) and the classic
// cucumber-jvm/cucumber-js regex style.
//
// Link matches each step's text, keyword stripped, against every known
// pattern. A step matching exactly one pattern is Confidence: static — a
// cucumber pattern match is a positive, structural identification, not a
// guess; two or more matches are heuristic and every one is reported, since
// this package never silently picks one over the others.
package gherkin

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

var skipDirNames = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	".git":         true,
}

// walk calls fn for every file under root whose detected language is not
// empty, skipping vendor/node_modules/.git. Per-file read errors are
// skipped rather than aborting the walk.
func walk(root string, fn func(relPath, language string, src []byte) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		language := lang.Detect(p)
		if language == "" {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		return fn(filepath.ToSlash(rel), language, src)
	})
}

// Index is the result of Build: every .feature file's symbols, every step
// definition found in the repo, and the edges Link produced between them.
type Index struct {
	features  map[string][]model.CodeSymbol // .feature path -> outline, source order
	rawLines  map[string][]string           // .feature path -> raw lines, for Examples tables
	stepDefs  []StepDef
	edges     []model.NavigationEdge
	unmatched map[string][]model.CodeSymbol // .feature path -> steps with no matching pattern
}

// Build walks root once for .feature files and once for Go/Java/JS/TS step
// definitions, then links them. It is meant to run once per repo (or per
// session refresh); callers needing live updates re-run it.
func Build(ctx context.Context, root string) (*Index, error) {
	tsProv := treesitter.New(root)

	features, rawLines, err := loadFeatures(ctx, root, tsProv)
	if err != nil {
		return nil, err
	}

	ri, err := buildRepoIndex(ctx, root, tsProv)
	if err != nil {
		return nil, err
	}

	defs, err := discoverStepDefs(ctx, tsProv, ri)
	if err != nil {
		return nil, err
	}

	ix := &Index{
		features: features,
		rawLines: rawLines,
		stepDefs: defs,
	}
	ix.link()
	return ix, nil
}

func loadFeatures(ctx context.Context, root string, tsProv *treesitter.Provider) (map[string][]model.CodeSymbol, map[string][]string, error) {
	features := map[string][]model.CodeSymbol{}
	rawLines := map[string][]string{}
	err := walk(root, func(rel, language string, src []byte) error {
		if language != "gherkin" {
			return nil
		}
		syms, err := tsProv.Outline(ctx, rel, src)
		if err != nil {
			return nil // unparsable .feature file: skip, don't abort the walk
		}
		features[rel] = syms
		rawLines[rel] = strings.Split(string(src), "\n")
		return nil
	})
	return features, rawLines, err
}

// StepDefs returns every step definition Build discovered, for callers that
// want to inspect discovery results directly (debugging, tests).
func (ix *Index) StepDefs() []StepDef {
	out := make([]StepDef, len(ix.stepDefs))
	copy(out, ix.stepDefs)
	return out
}

// StepsForScenario returns the Step symbols nested under the scenario (or
// background) with the given symbol id, in source order.
func (ix *Index) StepsForScenario(scenarioID string) []model.CodeSymbol {
	for _, syms := range ix.features {
		for _, sc := range syms {
			if sc.ID != scenarioID {
				continue
			}
			var steps []model.CodeSymbol
			for _, s := range syms {
				if s.Kind == model.SymStep && s.Range.StartLine >= sc.Range.StartLine && s.Range.EndLine <= sc.Range.EndLine {
					steps = append(steps, s)
				}
			}
			sort.Slice(steps, func(i, j int) bool { return steps[i].Range.StartLine < steps[j].Range.StartLine })
			return steps
		}
	}
	return nil
}

// ScenariosUsingStepDef returns every scenario (or background) that
// exercises the step definition identified by stepdefID — the value used as
// a NavigationEdge.To by Link: a handler symbol id when resolvable, else the
// StepDef's own synthetic id.
func (ix *Index) ScenariosUsingStepDef(stepdefID string) []model.CodeSymbol {
	seen := map[string]bool{}
	var out []model.CodeSymbol
	for _, e := range ix.edges {
		if e.To != stepdefID {
			continue
		}
		path, step := ix.findStep(e.From)
		if step == nil {
			continue
		}
		sc := enclosing(ix.features[path], model.SymScenario, step.Range.StartLine)
		if sc == nil {
			sc = enclosing(ix.features[path], model.SymBackground, step.Range.StartLine)
		}
		if sc == nil || seen[sc.ID] {
			continue
		}
		seen[sc.ID] = true
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Range.StartLine < out[j].Range.StartLine
	})
	return out
}

// EdgesForStep returns the step-def edges Link produced for the step with
// the given symbol id: zero when unmatched, one when unambiguous, more than
// one when ambiguous (every candidate is kept, never one picked over the
// others).
func (ix *Index) EdgesForStep(stepID string) []model.NavigationEdge {
	var out []model.NavigationEdge
	for _, e := range ix.edges {
		if e.From == stepID {
			out = append(out, e)
		}
	}
	return out
}

// Unmatched returns the steps in featurePath that matched no step
// definition pattern, in source order.
func (ix *Index) Unmatched(featurePath string) []model.CodeSymbol {
	out := ix.unmatched[featurePath]
	sorted := make([]model.CodeSymbol, len(out))
	copy(sorted, out)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Range.StartLine < sorted[j].Range.StartLine })
	return sorted
}

// EntryPoint is a candidate flow-ordering entry point with the reason FR-17
// (flow ordering, not yet built) will show when it orders files starting
// from it.
type EntryPoint struct {
	Symbol model.CodeSymbol `json:"symbol"`
	Reason string           `json:"reason"`
}

// EntryPoints returns every Gherkin scenario in the repo as a flow-ordering
// entry point. FR-17 doesn't exist yet; this is the extension point it will
// consume ("a scenario is an entry point").
func (ix *Index) EntryPoints() []EntryPoint {
	var out []EntryPoint
	for _, syms := range ix.features {
		for _, s := range syms {
			if s.Kind != model.SymScenario {
				continue
			}
			out = append(out, EntryPoint{
				Symbol: s,
				Reason: "Gherkin scenario: a flow starts here when it runs",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol.Path != out[j].Symbol.Path {
			return out[i].Symbol.Path < out[j].Symbol.Path
		}
		return out[i].Symbol.Range.StartLine < out[j].Symbol.Range.StartLine
	})
	return out
}

func (ix *Index) findStep(stepID string) (string, *model.CodeSymbol) {
	for path, syms := range ix.features {
		for i := range syms {
			if syms[i].Kind == model.SymStep && syms[i].ID == stepID {
				return path, &syms[i]
			}
		}
	}
	return "", nil
}

// enclosing returns the smallest symbol of kind containing line, or nil.
// Steps and scenarios never overlap another symbol of the same kind at the
// same nesting level, so "smallest span" is enough to pick the direct
// parent rather than an outer Rule or Feature.
func enclosing(syms []model.CodeSymbol, kind model.SymbolKind, line int) *model.CodeSymbol {
	best := -1
	bestSpan := 0
	for i, s := range syms {
		if s.Kind != kind {
			continue
		}
		if s.Range.StartLine <= line && line <= s.Range.EndLine {
			span := s.Range.EndLine - s.Range.StartLine
			if best == -1 || span < bestSpan {
				best, bestSpan = i, span
			}
		}
	}
	if best == -1 {
		return nil
	}
	return &syms[best]
}

var gherkinStepKeywords = []string{"Given ", "When ", "Then ", "And ", "But ", "* "}

// stripKeyword removes the leading Given/When/Then/And/But/* keyword from a
// step's text. Cucumber ignores the keyword when matching a step against a
// pattern — "And" and "But" inherit meaning from the previous step but carry
// no information for pattern matching — so Link never uses it as a filter.
func stripKeyword(text string) string {
	for _, kw := range gherkinStepKeywords {
		if strings.HasPrefix(text, kw) {
			return strings.TrimPrefix(text, kw)
		}
	}
	return text
}

// link matches every step across every feature file against every known
// step definition pattern, recording a NavigationEdge per match and the
// step in unmatched when nothing matches.
func (ix *Index) link() {
	ix.unmatched = map[string][]model.CodeSymbol{}
	for path, syms := range ix.features {
		lines := ix.rawLines[path]
		for _, s := range syms {
			if s.Kind != model.SymStep {
				continue
			}
			text := stripKeyword(s.Name)
			if strings.Contains(text, "<") {
				text = ix.resolveOutlinePlaceholders(syms, lines, s, text)
			}

			matched := ix.matchAny(text)
			if len(matched) == 0 {
				ix.unmatched[path] = append(ix.unmatched[path], s)
				continue
			}
			conf := model.ConfStatic
			if len(matched) > 1 {
				conf = model.ConfHeuristic
			}
			for _, def := range matched {
				to := def.HandlerID
				if to == "" {
					to = def.ID
				}
				ix.edges = append(ix.edges, model.NavigationEdge{
					Kind:       model.EdgeStepDef,
					From:       s.ID,
					To:         to,
					Confidence: conf,
					Source:     "gherkin",
					Site:       &model.Location{Path: def.Location.Path, Line: def.Location.Line},
				})
			}
		}
	}
}

// resolveOutlinePlaceholders substitutes "<name>" tokens in a Scenario
// Outline step with the enclosing scenario's first Examples row, when every
// placeholder has a matching column; otherwise every "<name>" becomes a
// generic wildcard value. The wildcard works because a placeholder is
// normally already inside the expression's own literal delimiters (quotes),
// e.g. a user named "<name>" -> a user named "x", which still lines up with
// a stepdef pattern like a user named "([^"]*)".
func (ix *Index) resolveOutlinePlaceholders(syms []model.CodeSymbol, lines []string, step model.CodeSymbol, text string) string {
	outline := enclosing(syms, model.SymScenario, step.Range.StartLine)
	if outline != nil {
		if ex := parseExamplesTable(lines, outline.Range.StartLine, outline.Range.EndLine); ex != nil {
			if sub, ok := substituteExample(text, ex); ok {
				return sub
			}
		}
	}
	return wildcardSubstitute(text)
}

// matchAny returns every StepDef whose pattern matches text, deduplicated
// by StepDef.ID.
func (ix *Index) matchAny(text string) []StepDef {
	var out []StepDef
	for _, d := range ix.stepDefs {
		if d.Pattern.MatchString(text) {
			out = append(out, d)
		}
	}
	return out
}
