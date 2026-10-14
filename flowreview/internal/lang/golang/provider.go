package golang

import (
	"context"
	"fmt"
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
// the call graph FR-15 builds on top of a Loader: when loader is nil (the
// zero-ish Provider FunctionAt/Outline-only callers construct) they return
// lang.ErrUnsupported, since there is no Loader to ask for a Result at all;
// otherwise they fetch the loader's current Result with Get (see resolve) —
// Warm/Load must already have been called by whoever drives this Provider, or
// Get's own "called before Warm" error surfaces. Definition, which does need
// a Load result, is not part of the LanguageProvider interface at all (it
// takes a *Result, a go/packages-specific shape no other language has) — call
// golang.Definition directly.
//
// Because FunctionAt/Outline are syntax-only, their CodeSymbol.ID uses the
// structural-tier convention ("path#Name", see structuralID) even though
// Capabilities reports TierExact: Symbols/ChangedSymbols (which do have a
// type-checked *Result) use the semantic "pkgpath.Name" form instead. A
// symbol looked up through Provider and the same symbol looked up through
// Symbols(res) therefore carry different ID strings; resolve reconciles the
// two by re-finding the symbol at its own Path/Range.StartLine in the
// type-checked Result (SymbolAt) before querying the call graph, so a caller
// holding either ID form gets the same answer.
type Provider struct {
	loader *Loader
}

var _ lang.LanguageProvider = (*Provider)(nil)

// NewProvider wraps loader as a lang.LanguageProvider. loader is unused by
// FunctionAt/Outline (they parse src directly); Callers/Callees/Implementations
// use it (see resolve).
func NewProvider(loader *Loader) *Provider {
	return &Provider{loader: loader}
}

func (p *Provider) Capabilities() lang.Capabilities {
	return lang.Capabilities{
		Name:            "golang",
		Tier:            lang.TierExact,
		Languages:       []string{"go"},
		Definitions:     true,
		Callers:         true,
		Callees:         true,
		Implementations: true,
		EntryPoints:     true,
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
	res, id, err := p.resolve(ctx, sym)
	if err != nil {
		return nil, err
	}
	return Callers(res, id)
}

func (p *Provider) Callees(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	res, id, err := p.resolve(ctx, sym)
	if err != nil {
		return nil, err
	}
	return Callees(res, id)
}

func (p *Provider) Implementations(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	res, id, err := p.resolve(ctx, sym)
	if err != nil {
		return nil, err
	}
	return Implementations(res, id)
}

// resolve fetches p.loader's current Result and reconciles sym to the
// exact-tier symbol ID the call graph is keyed by (see Provider's doc
// comment): FunctionAt/Outline only ever had syntax, so they stamped the
// structural-tier "path#Name" form on sym.ID even though Capabilities reports
// TierExact. Looking sym back up by its own Path and Range.StartLine in the
// type-checked Result — SymbolAt, the same lookup symbols.go itself uses —
// finds the matching exact-tier symbol when the Result has that file loaded
// at all; otherwise sym.ID is used as given, which is correct when it already
// is an exact-tier ID (e.g. one a caller got from Symbols(res) itself).
//
// A nil loader (the FunctionAt/Outline-only construction) has no Result to
// ask for at all, so it is lang.ErrUnsupported rather than a panic or a
// confusing "Get called before Warm".
func (p *Provider) resolve(ctx context.Context, sym model.CodeSymbol) (*Result, string, error) {
	if p.loader == nil {
		return nil, "", lang.ErrUnsupported
	}
	res, err := p.loader.Get(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("golang: %w", err)
	}
	if exact := SymbolAt(res, sym.Path, sym.Range.StartLine); exact != nil {
		return res, exact.ID, nil
	}
	return res, sym.ID, nil
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
