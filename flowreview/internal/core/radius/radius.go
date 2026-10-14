// Package radius computes FR-19's blast radius: for every function or method
// FR-14 says changed, the in-module callers that are not themselves part of
// the diff; and, for a changed signature or a changed interface method,
// every call site and implementer, each flagged against the diff — so a
// signature change that forgot to update a caller or an implementer shows up
// without an LLM.
//
// Compute reads a go/packages load result FR-13 already produced (a
// *golang.Result) and a diff FR-2 already parsed ([]model.ChangedFile); it
// never triggers a Load or a git operation itself.
package radius

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"

	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Caller is one in-module caller of a changed function or method that is not
// itself changed by this diff.
type Caller struct {
	Symbol     string           `json:"symbol"`
	Confidence model.Confidence `json:"confidence"`
	Site       *model.Location  `json:"site,omitempty"`
	InDiff     bool             `json:"in_diff"` // the caller's own file is part of the diff, even though this particular call was not
}

// Site is one call site that must be checked against a signature or
// interface-method change: who makes the call and where, flagged whether
// that exact line sits inside a changed hunk of the diff (not just whether
// its file does — see Caller.InDiff for the looser, file-level flag used
// where the caller itself is not under suspicion).
type Site struct {
	Caller     string           `json:"caller"`
	Confidence model.Confidence `json:"confidence"`
	Location   *model.Location  `json:"location,omitempty"`
	InDiff     bool             `json:"in_diff"`
}

// Impl is one concrete implementer relevant to a changed interface method, or
// — the reverse direction callgraph.go's Implementations documents — an
// interface a changed concrete method implements. Confidence is static for a
// type-checked match (types.Implements still holds after the change) and
// heuristic for a method found only by name: a type that implemented the
// interface before the diff but was not updated to match no longer satisfies
// it, so types.Implements can no longer see it — exactly the "forgot an
// implementer" case FR-19 exists to catch.
type Impl struct {
	Symbol     string           `json:"symbol"`
	Confidence model.Confidence `json:"confidence"`
	InDiff     bool             `json:"in_diff"`
}

// FuncRadius is one changed function, method or interface method's blast
// radius. For an interface method, Symbol is built with golang.SymbolID
// (interface methods have no CodeSymbol of their own).
type FuncRadius struct {
	Symbol           string           `json:"symbol"`
	Kind             model.SymbolKind `json:"kind"`
	SignatureChanged bool             `json:"signature_changed"`
	UnchangedCallers []Caller         `json:"unchanged_callers,omitempty"`
	CallSites        []Site           `json:"call_sites,omitempty"`
	Implementers     []Impl           `json:"implementers,omitempty"`
	Warnings         []string         `json:"warnings,omitempty"`
}

// Report is Compute's result, sorted deterministically by Symbol.
type Report struct {
	Functions []FuncRadius `json:"functions"`
}

// Compute builds the blast radius of every changed function, method and
// interface method golang.ChangedSymbolsDetailed finds in files, using res's
// call graph (Callers, Implementations) to find callers, call sites and
// implementers. old reads a file's content as of the diff's old revision —
// see golang.ChangedSymbolsDetailed's doc comment; Compute calls it a second
// time of its own, to read a changed interface's old method names (see
// oldInterfaceMethodNames).
func Compute(res *golang.Result, files []model.ChangedFile, old golang.OldContent) (Report, error) {
	changes, err := golang.ChangedSymbolsDetailed(res, files, old)
	if err != nil {
		return Report{}, fmt.Errorf("radius: %w", err)
	}

	// changedIDs is every symbol this diff touches that still exists on the
	// new side (added or modified) — a deleted symbol has nothing left in res
	// to be "an unchanged caller" of anything, so it is excluded.
	changedIDs := make(map[string]bool, len(changes))
	for _, c := range changes {
		if c.Kind != golang.ChangeDeleted {
			changedIDs[c.Symbol.ID] = true
		}
	}

	symsByID := make(map[string]model.CodeSymbol, len(res.FileToPkg))
	for _, s := range golang.Symbols(res) {
		symsByID[s.ID] = s
	}

	var out []FuncRadius
	for _, c := range changes {
		if c.Kind == golang.ChangeDeleted {
			continue // nothing left in res to ask the call graph about
		}
		switch c.Symbol.Kind {
		case model.SymFunc, model.SymMethod:
			out = append(out, funcRadius(res, files, symsByID, changedIDs, c))
		case model.SymInterface:
			out = append(out, interfaceRadius(res, files, old, symsByID, c)...)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return Report{Functions: out}, nil
}

// funcRadius is one changed func or method's FuncRadius: every in-module
// caller, split into "not itself changed" (always) and, when the signature
// changed, every call site regardless of whether its caller changed too.
func funcRadius(res *golang.Result, files []model.ChangedFile, symsByID map[string]model.CodeSymbol, changedIDs map[string]bool, c golang.Change) FuncRadius {
	fr := FuncRadius{Symbol: c.Symbol.ID, Kind: c.Symbol.Kind, SignatureChanged: c.SignatureChanged}

	callers, _ := golang.Callers(res, c.Symbol.ID)
	for _, e := range callers {
		if !changedIDs[e.From] {
			fr.UnchangedCallers = append(fr.UnchangedCallers, Caller{
				Symbol: e.From, Confidence: e.Confidence, Site: e.Site,
				InDiff: callerFileInDiff(files, e.Site),
			})
		}
		if c.SignatureChanged {
			fr.CallSites = append(fr.CallSites, Site{
				Caller: e.From, Confidence: e.Confidence, Location: e.Site,
				InDiff: locInHunk(files, e.Site),
			})
		}
	}

	if c.SignatureChanged {
		// The reverse direction (callgraph.go's own doc comment calls this
		// out as useful for FR-19): interfaces this changed method still
		// implements, for context. Unlike interfaceRadius, nothing here is
		// flagged as "forgot to update" — a concrete method breaking its own
		// interface conformance would need the same old-side type knowledge
		// interfaceRadius's heuristic needs, which this direction does not
		// have (it only runs from the new, post-change side).
		impls, _ := golang.Implementations(res, c.Symbol.ID)
		for _, e := range impls {
			other := e.To
			if other == c.Symbol.ID {
				other = e.From
			}
			fr.Implementers = append(fr.Implementers, Impl{
				Symbol: other, Confidence: e.Confidence, InDiff: symbolInDiff(files, symsByID[other]),
			})
		}
	}

	sortCallers(fr.UnchangedCallers)
	sortSites(fr.CallSites)
	sortImpls(fr.Implementers)
	return fr
}

// interfaceRadius is a changed interface's FuncRadius entries, one per
// method on the new side. Only interfaces whose declaration syntactically
// changed (SignatureChanged — added, removed or re-signatured method) get
// entries; an interface merely re-sorted or re-commented never triggers
// ChangedSymbolsDetailed's SignatureChanged in the first place.
func interfaceRadius(res *golang.Result, files []model.ChangedFile, old golang.OldContent, symsByID map[string]model.CodeSymbol, c golang.Change) []FuncRadius {
	if !c.SignatureChanged {
		return nil
	}
	f, ok := findChangedFile(files, c.Symbol.Path)
	if !ok {
		return nil
	}
	oldNames, ok := oldInterfaceMethodNames(old, f.OldPath, c.Symbol.Name)
	if !ok {
		return nil
	}
	newMethods, ok := newInterfaceMethods(res, c.Symbol.Package, c.Symbol.Name)
	if !ok {
		return nil
	}

	names := make([]string, 0, len(newMethods))
	for name := range newMethods {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []FuncRadius
	for _, name := range names {
		out = append(out, interfaceMethodRadius(res, files, symsByID, c, name, oldNames[name]))
	}
	return out
}

// interfaceMethodRadius is one method of a changed interface: every concrete
// implementer the type checker still finds (valid, Confidence static) plus,
// for a method that already existed before the diff, every other method in
// res's scope sharing its name that does not (Confidence heuristic) — the
// candidates for "implemented this before, was not updated to match". A
// brand-new method (wasOld false) skips the heuristic pass: nothing could
// have implemented a method that did not exist yet, so a same-named method
// elsewhere is very likely unrelated, not a forgotten implementer.
func interfaceMethodRadius(res *golang.Result, files []model.ChangedFile, symsByID map[string]model.CodeSymbol, c golang.Change, name string, wasOld bool) FuncRadius {
	methodID := golang.SymbolID(c.Symbol.Package, c.Symbol.Name, name)
	fr := FuncRadius{Symbol: methodID, Kind: model.SymMethod, SignatureChanged: true}

	addImplementer := func(implID string, conf model.Confidence) {
		inDiff := symbolInDiff(files, symsByID[implID])
		fr.Implementers = append(fr.Implementers, Impl{Symbol: implID, Confidence: conf, InDiff: inDiff})
		if !inDiff {
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("%s implements %s but is not changed in this diff", implID, methodID))
		}
		callers, _ := golang.Callers(res, implID)
		for _, ce := range callers {
			fr.CallSites = append(fr.CallSites, Site{
				Caller: ce.From, Confidence: ce.Confidence, Location: ce.Site, InDiff: locInHunk(files, ce.Site),
			})
		}
	}

	valid := map[string]bool{}
	implEdges, _ := golang.Implementations(res, methodID)
	for _, e := range implEdges {
		valid[e.From] = true
		addImplementer(e.From, e.Confidence)
	}

	if wasOld {
		for _, sym := range symsByID {
			if sym.Kind != model.SymMethod || sym.Name != name || valid[sym.ID] {
				continue
			}
			addImplementer(sym.ID, model.ConfHeuristic)
		}
	}

	fr.CallSites = dedupSites(fr.CallSites)
	sortSites(fr.CallSites)
	sortImpls(fr.Implementers)
	sort.Strings(fr.Warnings)
	return fr
}

func findChangedFile(files []model.ChangedFile, path string) (model.ChangedFile, bool) {
	for _, f := range files {
		if f.Path() == path {
			return f, true
		}
	}
	return model.ChangedFile{}, false
}

// oldInterfaceMethodNames parses oldPath's content as of the diff's old
// revision (via old) and returns the method names explicitly declared on the
// interface named ifaceName — syntax only, matching
// ChangedSymbolsDetailed's own old-side parsing. Only names are needed (see
// interfaceMethodRadius's wasOld), not a signature to compare: comparing this
// syntax-only text against the new side's type-checked signature would have
// exactly the false-positive risk golang.funcTypeText's doc comment already
// warns ChangedSymbolsDetailed itself away from. Embedded interfaces
// contribute no name (go/ast gives an embedded interface field no Names) and
// are not expanded.
func oldInterfaceMethodNames(old golang.OldContent, oldPath, ifaceName string) (map[string]bool, bool) {
	src, err := old(oldPath)
	if err != nil {
		return nil, false
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, oldPath, src, 0)
	if err != nil {
		return nil, false
	}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != ifaceName {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				return nil, false
			}
			out := map[string]bool{}
			for _, field := range it.Methods.List {
				for _, n := range field.Names {
					out[n.Name] = true
				}
			}
			return out, true
		}
	}
	return nil, false
}

// newInterfaceMethods is the type-checked method set of the interface named
// name in pkgPath, as of res — the new side. res.ByPath covers every package
// reachable from the loaded scope (FR-13), not just the "changed" ones, so
// this resolves even when the interface's own package was not itself
// modified (an interface method elsewhere can still be the thing that
// changed — not this slice's main scenario, but not excluded either).
func newInterfaceMethods(res *golang.Result, pkgPath, name string) (map[string]*types.Func, bool) {
	pkg, ok := res.ByPath[pkgPath]
	if !ok || pkg.Types == nil {
		return nil, false
	}
	obj := pkg.Types.Scope().Lookup(name)
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, false
	}
	iface, ok := tn.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, false
	}
	out := make(map[string]*types.Func, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		out[m.Name()] = m
	}
	return out, true
}

// callerFileInDiff reports whether loc's file is one of files — the looser,
// file-level flag Caller.InDiff documents.
func callerFileInDiff(files []model.ChangedFile, loc *model.Location) bool {
	if loc == nil {
		return false
	}
	for _, f := range files {
		if f.Path() == loc.Path {
			return true
		}
	}
	return false
}

// locInHunk reports whether loc's exact line sits inside one of its file's
// changed hunks (new-side range).
func locInHunk(files []model.ChangedFile, loc *model.Location) bool {
	if loc == nil {
		return false
	}
	return rangeInHunks(files, loc.Path, loc.Line, loc.Line)
}

// symbolInDiff reports whether sym's whole declaration range sits inside one
// of its file's changed hunks. A zero-value sym (an ID golang.Symbols did not
// resolve — e.g. a dependency outside res's loaded scope) is never in the
// diff.
func symbolInDiff(files []model.ChangedFile, sym model.CodeSymbol) bool {
	if sym.ID == "" {
		return false
	}
	return rangeInHunks(files, sym.Path, sym.Range.StartLine, sym.Range.EndLine)
}

func rangeInHunks(files []model.ChangedFile, path string, startLine, endLine int) bool {
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			he := h.NewStart + h.NewLines - 1
			if h.NewLines == 0 {
				he = h.NewStart
			}
			if startLine <= he && h.NewStart <= endLine {
				return true
			}
		}
	}
	return false
}

func sortCallers(c []Caller) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Symbol != c[j].Symbol {
			return c[i].Symbol < c[j].Symbol
		}
		return locLess(c[i].Site, c[j].Site)
	})
}

func sortSites(s []Site) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Caller != s[j].Caller {
			return s[i].Caller < s[j].Caller
		}
		return locLess(s[i].Location, s[j].Location)
	})
}

func sortImpls(impls []Impl) {
	sort.Slice(impls, func(i, j int) bool { return impls[i].Symbol < impls[j].Symbol })
}

func locLess(a, b *model.Location) bool {
	switch {
	case a == nil && b == nil:
		return false
	case a == nil:
		return true
	case b == nil:
		return false
	case a.Path != b.Path:
		return a.Path < b.Path
	case a.Line != b.Line:
		return a.Line < b.Line
	default:
		return a.Col < b.Col
	}
}

// dedupSites removes (Caller, Location) duplicates — interfaceMethodRadius
// can see the same call site twice when more than one implementer's Callers
// happens to report it (never observed from a single concrete method, but
// not ruled out either).
func dedupSites(sites []Site) []Site {
	type key struct {
		caller    string
		path      string
		line, col int
	}
	seen := make(map[key]bool, len(sites))
	out := make([]Site, 0, len(sites))
	for _, s := range sites {
		k := key{caller: s.Caller}
		if s.Location != nil {
			k.path, k.line, k.col = s.Location.Path, s.Location.Line, s.Location.Col
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}
