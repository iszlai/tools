// Package symbol builds FR-18's call-chain explorer report: the function or
// method enclosing a requested line, plus every caller, callee and
// implementation that function's provider can find, each resolved to a
// path+line to navigate to where possible. Go gets ResolveGo, built on
// FR-15's exact call graph (a *golang.Result FR-13 already produced -- this
// package never triggers a Load itself); every other language with a
// tree-sitter grammar gets ResolveStructural, FR-39's heuristic name search.
// Neither invents an edge or a location: an edge whose far end does not
// resolve to a known declaration is still reported (name, confidence,
// source) with no path to jump to, exactly as internal/core/radius already
// does for the same gap.
package symbol

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Target is one resolved caller, callee or implementation of a Report's
// Symbol. Confidence and Source are the underlying NavigationEdge's,
// unchanged. Path/Line is the place a click should jump to: for a caller,
// the exact call site (more useful than the caller's own declaration line,
// and what FR-19's existing callers panel already navigates to); for a
// callee or an implementation, the target's own declaration. Empty Path
// means nothing resolved -- the UI shows the name as plain text, not a link.
type Target struct {
	Name       string           `json:"name"`
	Kind       model.SymbolKind `json:"kind,omitempty"`
	Path       string           `json:"path,omitempty"`
	Line       int              `json:"line,omitempty"`
	Confidence model.Confidence `json:"confidence"`
	Source     string           `json:"source"`
	InDiff     bool             `json:"in_diff"`
}

// Report is GET /api/symbol's payload for the symbol enclosing a requested
// line.
type Report struct {
	Symbol          model.CodeSymbol `json:"symbol"`
	Tier            lang.Tier        `json:"tier"`
	Callers         []Target         `json:"callers"`
	Callees         []Target         `json:"callees"`
	Implementations []Target         `json:"implementations"`
}

// isNavigable restricts FR-18 to what the user actually asked for: a button
// on a function or method line. A type or interface declaration (which
// Outline/FunctionAt can also return) gets no Report.
func isNavigable(k model.SymbolKind) bool {
	return k == model.SymFunc || k == model.SymMethod
}

// ResolveGo builds a Report for the function or method enclosing line in
// path, src. When exact is true and res has path loaded, callers/callees/
// implementations come from FR-15's call graph; otherwise (exact is false --
// the caller asked about the diff's old side, which the call graph never
// reflects -- or res has nothing for path, e.g. a navigation target outside
// the diff's changed+reverse-importer scope, or res is nil because the
// workspace's Go analysis never ran) the Symbol itself still comes back,
// found the same syntax-only way golang.Provider.FunctionAt already works
// (no type information needed), just with no edges: an honestly smaller
// answer, never an error. Returns (nil, nil) when line does not sit inside a
// func or method at all.
func ResolveGo(ctx context.Context, res *golang.Result, files []model.ChangedFile, path string, src []byte, line int, exact bool) (*Report, error) {
	if exact && res != nil {
		if sym := golang.SymbolAt(res, path, line); sym != nil && isNavigable(sym.Kind) {
			return resolveGoExact(res, files, *sym), nil
		}
	}

	sym, err := golang.NewProvider(nil).FunctionAt(ctx, path, src, line)
	if err != nil {
		return nil, err
	}
	if sym == nil || !isNavigable(sym.Kind) {
		return nil, nil
	}
	return &Report{Symbol: *sym, Tier: lang.TierExact, Callers: []Target{}, Callees: []Target{}, Implementations: []Target{}}, nil
}

func resolveGoExact(res *golang.Result, files []model.ChangedFile, sym model.CodeSymbol) *Report {
	symsByID := make(map[string]model.CodeSymbol, len(res.FileToPkg))
	for _, s := range golang.Symbols(res) {
		symsByID[s.ID] = s
	}
	resolve := func(id string) (model.CodeSymbol, bool) {
		s, ok := symsByID[id]
		return s, ok
	}

	callers, _ := golang.Callers(res, sym.ID)
	callees, _ := golang.Callees(res, sym.ID)
	impls, _ := golang.Implementations(res, sym.ID)

	return &Report{
		Symbol:          sym,
		Tier:            lang.TierExact,
		Callers:         buildTargets(callers, sym.ID, resolve, files, true),
		Callees:         buildTargets(callees, sym.ID, resolve, files, false),
		Implementations: buildTargets(impls, sym.ID, resolve, files, false),
	}
}

// ResolveStructural is ResolveGo's counterpart for every language with a
// tree-sitter grammar (FR-39): p.FunctionAt finds the enclosing symbol in
// src (whichever revision's content the caller read -- old or new, it makes
// no difference to a syntax-only parse), then p.Callers/Callees run their
// repo-wide heuristic name search. Implementations is always empty: the
// structural tier has no type information to decide "implements" with (see
// Provider.Implementations), so there is nothing to ask.
func ResolveStructural(ctx context.Context, p *treesitter.Provider, files []model.ChangedFile, path string, src []byte, line int) (*Report, error) {
	sym, err := p.FunctionAt(ctx, path, src, line)
	if err != nil {
		return nil, err
	}
	if sym == nil || !isNavigable(sym.Kind) {
		return nil, nil
	}

	resolve := func(id string) (model.CodeSymbol, bool) {
		s, err := p.ResolveID(ctx, id)
		if err != nil || s == nil {
			return model.CodeSymbol{}, false
		}
		return *s, true
	}

	callers, err := p.Callers(ctx, *sym)
	if err != nil {
		return nil, err
	}
	callees, err := p.Callees(ctx, *sym)
	if err != nil {
		return nil, err
	}

	return &Report{
		Symbol:          *sym,
		Tier:            lang.TierStructural,
		Callers:         buildTargets(callers, sym.ID, resolve, files, true),
		Callees:         buildTargets(callees, sym.ID, resolve, files, false),
		Implementations: []Target{},
	}, nil
}

// otherEnd is whichever end of edge is not selfID -- the far end to report,
// regardless of which direction (From or To) the edge happened to store it
// in. Callers edges always have To == selfID (the callee); Callees edges
// always have From == selfID (the caller); an Implementations edge can be
// either, depending on whether selfID is the interface or the concrete
// method -- see golang's Implementations doc comment. A self-recursive call
// has both ends equal to selfID, which this reports as selfID itself: an
// honest answer ("it calls itself"), not a bug to special-case.
func otherEnd(e model.NavigationEdge, selfID string) string {
	if e.From == selfID {
		return e.To
	}
	return e.From
}

// buildTargets turns edges into Targets. resolve looks an edge's far-end id
// up to its own declaration; it returns ok=false for an id outside the
// provider's known scope (a dependency, a closure, a since-edited
// declaration), in which case Target.Path stays empty rather than guessing.
// preferSite is true only for Callers: e.Site there is the call site inside
// the *caller's* own file, a more useful jump target than the caller's
// declaration line (and what FR-19's callers panel already does); for
// Callees and Implementations e.Site -- when a provider sets one at all --
// sits inside the *current* symbol's own body, so it is never preferred
// over the resolved far end's own declaration.
func buildTargets(edges []model.NavigationEdge, selfID string, resolve func(string) (model.CodeSymbol, bool), files []model.ChangedFile, preferSite bool) []Target {
	out := make([]Target, 0, len(edges))
	for _, e := range edges {
		otherID := otherEnd(e, selfID)
		t := Target{Confidence: e.Confidence, Source: e.Source, Name: fallbackName(otherID)}

		sym, ok := resolve(otherID)
		if ok {
			t.Name = displayName(sym)
			t.Kind = sym.Kind
		}

		switch {
		case preferSite && e.Site != nil:
			t.Path, t.Line = e.Site.Path, e.Site.Line
		case ok:
			t.Path, t.Line = sym.Path, sym.Range.StartLine
		case e.Site != nil:
			t.Path, t.Line = e.Site.Path, e.Site.Line
		}
		if t.Path != "" {
			t.InDiff = lineInDiff(files, t.Path, t.Line)
		}
		out = append(out, t)
	}
	sortTargets(out)
	return out
}

// displayName is sym's short, human-recognizable name: "Name" or
// "Recv.Name" -- the same shape app.js's own shortSymbolName derives
// client-side from a raw golang.SymbolID, computed here instead so a
// structural-tier id (a different string convention entirely) gets the same
// treatment without the client needing to know about either.
func displayName(sym model.CodeSymbol) string {
	if sym.Receiver != "" {
		return strings.TrimPrefix(sym.Receiver, "*") + "." + sym.Name
	}
	return sym.Name
}

// recvIDPattern matches golang's exact-tier method id convention,
// "pkgpath.(Recv).Name" (see golang.SymbolID).
var recvIDPattern = regexp.MustCompile(`\.\(([^)]+)\)\.([^.]+)$`)

// fallbackName derives a readable name straight from a raw symbol id, for
// when resolve found no declaration to read Name/Receiver off of: a
// structural-tier id ("path#Name" or "path#Recv.Name") after its "#", an
// exact-tier method id's "Recv.Name", or an exact-tier plain id's last
// "."-segment.
func fallbackName(id string) string {
	if _, rest, ok := strings.Cut(id, "#"); ok {
		return rest
	}
	if m := recvIDPattern.FindStringSubmatch(id); m != nil {
		return m[1] + "." + m[2]
	}
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// lineInDiff reports whether path's line sits inside one of its file's
// changed hunks (new-side range) -- the same "anchor at NewStart" rule for a
// deletion-only hunk that internal/core/radius's own rangeInHunks uses, kept
// here rather than exported from there: FR-18's Target is a single
// (path, line) point, not radius's (path, startLine, endLine) range.
func lineInDiff(files []model.ChangedFile, path string, line int) bool {
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			end := h.NewStart + h.NewLines - 1
			if h.NewLines == 0 {
				end = h.NewStart
			}
			if h.NewStart <= line && line <= end {
				return true
			}
		}
	}
	return false
}

func sortTargets(targets []Target) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Path != targets[j].Path {
			return targets[i].Path < targets[j].Path
		}
		if targets[i].Line != targets[j].Line {
			return targets[i].Line < targets[j].Line
		}
		return targets[i].Name < targets[j].Name
	})
}
