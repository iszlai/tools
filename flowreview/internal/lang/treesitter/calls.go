package treesitter

import (
	"context"
	"os"
	"path/filepath"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Callees finds call expressions lexically inside sym's own range whose
// name matches a function or method defined somewhere in the repo (same
// language family as sym). This is a heuristic name search, not a resolved
// call graph: it never invents an edge for a name with no matching
// definition, and it reports every matching definition when a name is
// ambiguous.
func (p *Provider) Callees(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	if sym.Language == gherkinLanguage {
		return nil, nil
	}
	src, err := os.ReadFile(filepath.Join(p.repoRoot, sym.Path))
	if err != nil {
		return nil, err
	}
	fa, err := p.analyze(sym.Path, sym.Language, src)
	if err != nil {
		return nil, err
	}

	starts := lineStarts(src)
	startByte := posToByte(starts, sym.Range.StartLine, orDefault(sym.Range.StartCol, 1))
	endByte := posToByte(starts, sym.Range.EndLine, orDefault(sym.Range.EndCol, lineByteLen(src, starts, sym.Range.EndLine)+1))

	family := familyOf(sym.Language)
	var edges []model.NavigationEdge
	for _, c := range fa.calls {
		b := posToByte(starts, c.line, c.col)
		if b < startByte || b >= endByte {
			continue
		}
		for _, target := range p.findDefsByName(family, c.name) {
			edges = append(edges, model.NavigationEdge{
				Kind:       model.EdgeCalls,
				From:       sym.ID,
				To:         target.ID,
				Confidence: model.ConfHeuristic,
				Source:     "name-search",
				Site:       &model.Location{Path: sym.Path, Line: c.line, Col: c.col},
			})
		}
	}
	return edges, nil
}

// Callers walks every file in the repo with the same language family as
// sym, looking for call sites named sym.Name, and reports the enclosing
// function or method at each one as the caller. Heuristic: a same-named
// function elsewhere in the repo is indistinguishable from the real target.
func (p *Provider) Callers(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	if sym.Language == gherkinLanguage {
		return nil, nil
	}
	family := familyOf(sym.Language)
	var edges []model.NavigationEdge
	err := walkRepo(p.repoRoot, family, func(relPath, language string, src []byte) {
		fa, err := p.analyze(relPath, language, src)
		if err != nil {
			return
		}
		var funcsAndMethods []model.CodeSymbol
		for _, d := range fa.defs {
			if d.Kind == model.SymFunc || d.Kind == model.SymMethod {
				funcsAndMethods = append(funcsAndMethods, d)
			}
		}
		for _, c := range fa.calls {
			if c.name != sym.Name {
				continue
			}
			caller := innermost(funcsAndMethods, c.line)
			if caller == nil {
				continue
			}
			edges = append(edges, model.NavigationEdge{
				Kind:       model.EdgeCalls,
				From:       caller.ID,
				To:         sym.ID,
				Confidence: model.ConfHeuristic,
				Source:     "name-search",
				Site:       &model.Location{Path: relPath, Line: c.line, Col: c.col},
			})
		}
	})
	return edges, err
}

// findDefsByName walks every file in family looking for a func or method
// definition named name, returning every match.
func (p *Provider) findDefsByName(family, name string) []model.CodeSymbol {
	var out []model.CodeSymbol
	_ = walkRepo(p.repoRoot, family, func(relPath, language string, src []byte) {
		fa, err := p.analyze(relPath, language, src)
		if err != nil {
			return
		}
		for _, d := range fa.defs {
			if d.Name == name && (d.Kind == model.SymFunc || d.Kind == model.SymMethod) {
				out = append(out, d)
			}
		}
	})
	return out
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// lineStarts returns the byte offset of the first byte of each line (1-based
// line n starts at lineStarts[n-1]).
func lineStarts(src []byte) []int {
	starts := make([]int, 1, 64)
	starts[0] = 0
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// posToByte converts a 1-based (line, col) into a byte offset. col is a byte
// offset within the line, matching tree-sitter's Point.Column.
func posToByte(starts []int, line, col int) int {
	idx := line - 1
	switch {
	case idx < 0:
		idx = 0
	case idx >= len(starts):
		idx = len(starts) - 1
	}
	return starts[idx] + (col - 1)
}

func lineByteLen(src []byte, starts []int, line int) int {
	idx := line - 1
	if idx < 0 || idx >= len(starts) {
		return 0
	}
	start := starts[idx]
	end := len(src)
	if idx+1 < len(starts) {
		end = starts[idx+1] - 1 // exclude the newline
	}
	if end < start {
		end = start
	}
	return end - start
}
