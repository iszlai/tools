package golang

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Provider is golang's lang.LanguageProvider. FunctionAt and Outline work
// from syntax alone (go/parser, no type information — matching FR-8's "no
// type checking" requirement, and meaning they need no Load to have
// completed, let alone succeeded). Callers, Callees and Implementations need
// the call graph FR-15 builds on top of a Loader and return lang.ErrUnsupported
// until then; Definition, which does need a Load result, is not part of the
// LanguageProvider interface at all (it takes a *Result, a go/packages-specific
// shape no other language has) — call golang.Definition directly.
//
// Because FunctionAt/Outline are syntax-only, their CodeSymbol.ID uses the
// structural-tier convention ("path#Name", see structuralID) even though
// Capabilities reports TierExact: Symbols/ChangedSymbols (which do have a
// type-checked *Result) use the semantic "pkgpath.Name" form instead. A
// symbol looked up through Provider and the same symbol looked up through
// Symbols(res) therefore carry different ID strings until FR-15 unifies the
// two paths; this is a deliberate, documented seam, not an oversight.
type Provider struct {
	loader *Loader
}

var _ lang.LanguageProvider = (*Provider)(nil)

// NewProvider wraps loader as a lang.LanguageProvider. loader is unused by
// FunctionAt/Outline (they parse src directly) and is accepted now so FR-15
// can wire Callers/Callees/Implementations through it without changing this
// constructor's signature.
func NewProvider(loader *Loader) *Provider {
	return &Provider{loader: loader}
}

func (p *Provider) Capabilities() lang.Capabilities {
	return lang.Capabilities{
		Name:        "golang",
		Tier:        lang.TierExact,
		Languages:   []string{"go"},
		Definitions: true,
	}
}

func (p *Provider) Supports(path string) bool {
	return strings.HasSuffix(path, ".go")
}

// FunctionAt returns the innermost of src's top-level declarations enclosing
// line, or nil when none does.
func (p *Provider) FunctionAt(ctx context.Context, path string, src []byte, line int) (*model.CodeSymbol, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}
	var best *model.CodeSymbol
	for _, d := range declsFromFile(file) {
		start := fset.Position(d.start).Line
		end := fset.Position(d.end).Line
		if line < start || line > end {
			continue
		}
		if best != nil && (end-start) >= (best.Range.EndLine-best.Range.StartLine) {
			continue
		}
		sym := declSymbol(fset, path, d.id(path, true), d)
		best = &sym
	}
	return best, nil
}

// Outline lists src's top-level declarations in source order.
func (p *Provider) Outline(ctx context.Context, path string, src []byte) ([]model.CodeSymbol, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}
	decls := declsFromFile(file)
	out := make([]model.CodeSymbol, 0, len(decls))
	for _, d := range decls {
		out = append(out, declSymbol(fset, path, d.id(path, true), d))
	}
	return out, nil
}

func (p *Provider) Callers(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	return nil, lang.ErrUnsupported
}

func (p *Provider) Callees(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	return nil, lang.ErrUnsupported
}

func (p *Provider) Implementations(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	return nil, lang.ErrUnsupported
}

// declSymbol builds a syntax-only CodeSymbol for d, reported at path under
// the given, already-computed id (goID or structuralID — see callers in
// provider.go and symbols.go).
func declSymbol(fset *token.FileSet, path, id string, d declInfo) model.CodeSymbol {
	start := fset.Position(d.start)
	end := fset.Position(d.end)
	return model.CodeSymbol{
		ID:        id,
		Name:      d.name,
		Kind:      d.kind,
		Language:  "go",
		Receiver:  d.recv,
		Path:      path,
		Range:     model.Range{StartLine: start.Line, EndLine: end.Line},
		Signature: d.sigText(fset),
		Exported:  ast.IsExported(d.name),
	}
}
