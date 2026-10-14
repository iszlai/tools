package treesitter

import (
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Gherkin has no usable Go tree-sitter binding (see grammars.go), and its
// grammar is regular enough that a line scan is simpler and more robust
// than driving a real parser would be. gherkinHeading.level orders the
// nesting Feature > Rule > Scenario/Background > Step; a new heading or
// step closes anything open at its level or deeper.
type gherkinHeading struct {
	prefix string
	level  int
	kind   model.SymbolKind
}

var gherkinHeadings = []gherkinHeading{
	{"Feature:", 0, model.SymFeature},
	{"Rule:", 1, model.SymRule},
	{"Background:", 2, model.SymBackground},
	{"Scenario Outline:", 2, model.SymScenario},
	{"Scenario:", 2, model.SymScenario},
}

const gherkinStepLevel = 3

var gherkinStepKeywords = []string{"Given ", "When ", "Then ", "And ", "But ", "* "}

func parseGherkin(path string, src []byte) []model.CodeSymbol {
	lines := strings.Split(string(src), "\n")
	lastLine := len(lines)
	if lastLine > 0 && lines[lastLine-1] == "" {
		lastLine-- // trailing newline produces a phantom empty final element
	}

	type open struct {
		sym   model.CodeSymbol
		level int
	}
	var stack []open
	var out []model.CodeSymbol

	closeTo := func(level, endLine int) {
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.level == gherkinStepLevel {
				// Steps are single-line; a later heading's endLine may be
				// several (blank/table) lines past the step itself.
				top.sym.Range.EndLine = top.sym.Range.StartLine
			} else {
				top.sym.Range.EndLine = endLine
			}
			out = append(out, top.sym)
		}
	}
	enclosingName := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].sym.Name
	}
	push := func(level int, kind model.SymbolKind, name string, lineNo int) {
		receiver := enclosingName()
		id := path + "#" + name
		if receiver != "" {
			id = path + "#" + receiver + "." + name
		}
		stack = append(stack, open{
			level: level,
			sym: model.CodeSymbol{
				ID:       id,
				Name:     name,
				Kind:     kind,
				Language: gherkinLanguage,
				Path:     path,
				Receiver: receiver,
				Range:    model.Range{StartLine: lineNo, EndLine: lineNo},
			},
		})
	}

	for i, raw := range lines {
		lineNo := i + 1
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if kind, name, level, ok := matchGherkinHeading(text); ok {
			closeTo(level, lineNo-1)
			push(level, kind, name, lineNo)
			continue
		}
		if step, ok := matchGherkinStep(text); ok {
			closeTo(gherkinStepLevel, lineNo-1)
			push(gherkinStepLevel, model.SymStep, step, lineNo)
		}
	}
	closeTo(0, lastLine)

	sort.Slice(out, func(i, j int) bool { return out[i].Range.StartLine < out[j].Range.StartLine })
	return out
}

func matchGherkinHeading(text string) (model.SymbolKind, string, int, bool) {
	for _, h := range gherkinHeadings {
		if strings.HasPrefix(text, h.prefix) {
			return h.kind, strings.TrimSpace(strings.TrimPrefix(text, h.prefix)), h.level, true
		}
	}
	return "", "", 0, false
}

func matchGherkinStep(text string) (string, bool) {
	for _, kw := range gherkinStepKeywords {
		if strings.HasPrefix(text, kw) {
			return text, true
		}
	}
	return "", false
}
