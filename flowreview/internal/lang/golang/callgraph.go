// FR-15: the call graph on top of a Load result — callers, callees and
// interface implementations — built with go/ssa and go/callgraph, and
// go/types for implementations. Everything here reads a *Result FR-13
// already produced and the symbol-ID conventions FR-14 (symbols.go) defined;
// nothing here triggers a Load itself.
//
// Building the SSA program and its call graph is not cheap (see BENCHMARK.md
// for real numbers), so it happens once per *Result and is cached by the
// Result's pointer identity — the same scoping FR-13's own in-memory Load
// cache uses, and for the same reason: a *Result is only ever handed out by
// one Loader, reused for the life of a review session.
package golang

import (
	"fmt"
	"go/token"
	"go/types"
	"io"
	"sort"
	"sync"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// callGraph is FR-15's view of one Result: the SSA-level call graph plus the
// lookups Callers/Callees/Implementations need, built once by buildCallGraph
// and cached by graphFor.
type callGraph struct {
	source string // "vta" or "cha" — whichever algorithm actually produced graph; see buildWithFallback
	graph  *callgraph.Graph

	// funcsByID maps a symbol ID (funcSymbolID's output) to every
	// *ssa.Function that resolves to it. More than one is normal: Tests:true
	// in loader.go makes go/packages hand back a "[pkg.test]" type-checked
	// variant of a package alongside the plain one, so a function declared in
	// a tested package gets one *ssa.Function per variant, both mapping to
	// the same ID — dropping either would silently miss real call sites that
	// only the test variant's SSA sees (e.g. a call from a _test.go file).
	funcsByID map[string][]*ssa.Function

	// implEdges is every EdgeImplements edge in res's loaded scope (see
	// buildImplementsEdges); it does not depend on SSA/VTA/CHA at all, so it
	// is still populated even on the degraded "SSA build panicked" path.
	implEdges []model.NavigationEdge
}

var (
	graphCacheMu sync.Mutex
	graphCache   = map[*Result]*callGraph{}
)

// graphFor returns the cached call graph for res, building it on first use.
// The cache key is res's pointer identity, as FR-15 specifies: a Loader hands
// out the same *Result to every caller asking about one Load, so building
// once per Result is exactly "once per review session's view of this scope",
// never rebuilt for repeat queries.
func graphFor(res *Result) (*callGraph, error) {
	if res == nil {
		return nil, fmt.Errorf("golang: nil Result")
	}

	graphCacheMu.Lock()
	g, ok := graphCache[res]
	graphCacheMu.Unlock()
	if ok {
		return g, nil
	}

	g = buildCallGraph(res)

	graphCacheMu.Lock()
	graphCache[res] = g
	graphCacheMu.Unlock()
	return g, nil
}

// buildCallGraph builds everything graphFor caches. It never errors: a
// failure building the SSA-based call graph degrades to an empty one (see the
// recover below) rather than making every caller/callee/implementation query
// against this Result fail — implEdges, which needs only go/types, is
// unaffected either way and is always computed.
func buildCallGraph(res *Result) *callGraph {
	implEdges := buildImplementsEdges(res)

	var (
		graph  *callgraph.Graph
		source string
		prog   *ssa.Program
	)
	func() {
		// SSA construction (ssautil.Packages, Program.Build) is mature but
		// not infallible on pathological or partially-ill-typed input; FR-15
		// asks for this to degrade to CHA, never to crash flowreview. CHA
		// itself (built from the same prog) would panic for the same reasons
		// SSA construction would, so one recover covers both — the narrower,
		// VTA-specific recover is in buildWithFallback, since VTA (unlike
		// go/ssa and CHA) is documented by its own package as experimental
		// and is the panic this slice actually expects to see in practice.
		defer func() {
			if recover() != nil {
				graph, source, prog = nil, "", nil
			}
		}()
		p, _ := ssautil.Packages(res.Packages, ssa.InstantiateGenerics)
		p.Build()
		g, s := buildWithFallback(p)
		prog, graph, source = p, g, s
	}()

	if graph == nil {
		// Believed unreachable on real code (go/ssa and CHA are both
		// mature), but documented and handled rather than assumed away:
		// there is no ssa.Program to index, so every Callers/Callees query
		// against this Result sees an empty graph instead of panicking.
		// "cha" is the honest label even though no algorithm actually ran —
		// it is what the degraded path would have fallen back to.
		return &callGraph{source: "cha", funcsByID: map[string][]*ssa.Function{}, implEdges: implEdges}
	}
	return &callGraph{
		source:    source,
		graph:     graph,
		funcsByID: indexFunctions(prog),
		implEdges: implEdges,
	}
}

// buildWithFallback returns prog's call graph: VTA seeded with an initial CHA
// graph — the vta package's own doc says "If initial is nil, VTA uses a more
// efficient [but less precise] approach to construct a CHA call graph"
// internally anyway, so building that seed ourselves costs nothing extra and
// gives a real, already-computed graph to fall back to — or CHA alone when
// VTA panics. VTA's package doc calls it "experimental" and warns its
// interface and behavior are "subject to change"; CHA carries no such
// warning. source names whichever algorithm actually produced the returned
// graph, for NavigationEdge.Source and BENCHMARK.md.
func buildWithFallback(prog *ssa.Program) (graph *callgraph.Graph, source string) {
	chaGraph := cha.CallGraph(prog)
	functions := ssautil.AllFunctions(prog)

	var vtaGraph *callgraph.Graph
	func() {
		defer func() {
			if recover() != nil {
				vtaGraph = nil
			}
		}()
		vtaGraph = vta.CallGraph(functions, chaGraph)
	}()
	if vtaGraph != nil {
		return vtaGraph, "vta"
	}
	return chaGraph, "cha"
}

// indexFunctions maps every function flowreview can assign a symbol ID to,
// by that ID (funcSymbolID), so Callers/Callees can go straight from a
// model.CodeSymbol.ID to the ssa.Function(s) that realize it.
func indexFunctions(prog *ssa.Program) map[string][]*ssa.Function {
	out := map[string][]*ssa.Function{}
	if prog == nil {
		return out
	}
	for fn := range ssautil.AllFunctions(prog) {
		id, ok := funcSymbolID(fn)
		if !ok {
			continue
		}
		out[id] = append(out[id], fn)
	}
	return out
}

// funcSymbolID is fn's model.CodeSymbol.ID, using exactly FR-14's convention
// (goID: "pkgpath.Name" / "pkgpath.(Recv).Name", receiver bare — see
// symbols.go). Three SSA shapes need resolving down to "the symbol a
// reviewer would recognize" first:
//
//   - a closure (fn.Parent() != nil) is attributed to its nearest enclosing
//     named function: walk Parent() until nil. A call inside a closure, a
//     goroutine's or a defer's function literal is reported as made by the
//     function that lexically contains it — nothing names the closure
//     itself in FR-14's symbol model, and a reviewer reads "RunClosure calls
//     Helper", not "an anonymous function calls Helper".
//   - a method value, bound method, or any of go/ssa's other method
//     wrappers/thunks (see wrappers.go) has no declaration of its own, but
//     fn.Object() already returns the *types.Func of the real method it
//     delegates to — using Object() instead of fn.Name() maps all of them to
//     that method with no special-casing.
//   - a generic instantiation (fn.Origin() returns non-nil) is attributed to
//     its origin, the one declaration a reviewer actually wrote — not one ID
//     per call site's type arguments.
//
// These compose in that order: closures resolve first (a closure can live
// inside a generic instantiation), then the resulting top-level function's
// Origin, if any, is resolved once more.
//
// A function with no types.Func at all — a package initializer, "init", or a
// handful of other fully synthetic functions, see ssa.Function.Object's own
// doc — has no CodeSymbol and no ID; (_, false) says so, and callers drop the
// edge rather than invent one.
func funcSymbolID(fn *ssa.Function) (string, bool) {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	if origin := fn.Origin(); origin != nil {
		fn = origin
	}

	tf, ok := fn.Object().(*types.Func)
	if !ok || tf == nil {
		return "", false
	}
	pkg := tf.Pkg()
	if pkg == nil { // a universe-scope builtin; never a reviewable symbol
		return "", false
	}

	recv := ""
	if sig, ok := tf.Type().(*types.Signature); ok && sig.Recv() != nil {
		recv = recvBareName(sig.Recv().Type())
	}
	return goID(pkg.Path(), recv, tf.Name()), true
}

// recvBareName is receiverTypeName's semantic-tier counterpart: t is a
// method's resolved receiver type (types.Signature.Recv().Type()). The bare
// declared name is what goID wants regardless of pointer-ness or generic
// instantiation — a generic type's Named still reports its declared name via
// Obj().Name() no matter what it was instantiated with ("Stack" for
// Stack[int]), matching symbols.go's receiverTypeName exactly.
func recvBareName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// location turns a token.Pos from res's Fset into a model.Location with a
// repo-relative path, using res.FileToPkg the same way symbols.go's relPath
// callers do. A position with no known owning package (outside the files
// go/packages told res about) still gets reported, with an absolute path —
// honestly approximate rather than silently dropped.
func location(res *Result, pos token.Pos) *model.Location {
	if !pos.IsValid() {
		return nil
	}
	p := res.Fset.Position(pos)
	if p.Filename == "" {
		return nil
	}
	return &model.Location{Path: relPath(res, res.FileToPkg[p.Filename], p.Filename), Line: p.Line, Col: p.Column}
}

// Callers returns every call edge into the function(s) symbolID names — "who
// calls this". See Callees for the edge fields; the only difference is which
// end of each callgraph.Edge symbolID had to match.
func Callers(res *Result, symbolID string) ([]model.NavigationEdge, error) {
	return queryCalls(res, symbolID, func(n *callgraph.Node) []*callgraph.Edge { return n.In })
}

// Callees returns every call edge out of the function(s) symbolID names —
// "what this calls". Kind is always EdgeCalls; Confidence is "static" for a
// site whose callee was resolved at compile time (CallCommon.StaticCallee()
// != nil — true even for a direct call written inside a closure, goroutine or
// defer) and "interface-dispatch" for everything else: invoke-mode interface
// calls and any call through a func value VTA/CHA had to resolve by type
// flow. Source names whichever algorithm built the whole graph (see
// buildWithFallback). Every candidate for a dynamic call is returned — never
// one picked — and (from, to, site) duplicates (e.g. from a function and its
// "[pkg.test]" variant both reaching the same call) are collapsed.
func Callees(res *Result, symbolID string) ([]model.NavigationEdge, error) {
	return queryCalls(res, symbolID, func(n *callgraph.Node) []*callgraph.Edge { return n.Out })
}

func queryCalls(res *Result, symbolID string, edgesOf func(*callgraph.Node) []*callgraph.Edge) ([]model.NavigationEdge, error) {
	g, err := graphFor(res)
	if err != nil {
		return nil, err
	}
	var out []model.NavigationEdge
	for _, fn := range g.funcsByID[symbolID] {
		node := g.graph.Nodes[fn]
		if node == nil {
			continue
		}
		for _, e := range edgesOf(node) {
			if edge, ok := navigationEdge(res, g, e); ok {
				out = append(out, edge)
			}
		}
	}
	return dedupEdges(out), nil
}

// navigationEdge turns one callgraph.Edge into a NavigationEdge, or (_,
// false) when either end has no symbol ID (funcSymbolID) to anchor it to —
// e.g. an edge from the call graph's synthetic root, which has no Func.
func navigationEdge(res *Result, g *callGraph, e *callgraph.Edge) (model.NavigationEdge, bool) {
	if e.Caller == nil || e.Caller.Func == nil || e.Callee == nil || e.Callee.Func == nil {
		return model.NavigationEdge{}, false
	}
	fromID, ok := funcSymbolID(e.Caller.Func)
	if !ok {
		return model.NavigationEdge{}, false
	}
	toID, ok := funcSymbolID(e.Callee.Func)
	if !ok {
		return model.NavigationEdge{}, false
	}

	conf := model.ConfInterfaceDispatch
	if e.Site != nil && !e.Site.Common().IsInvoke() && e.Site.Common().StaticCallee() != nil {
		conf = model.ConfStatic
	}
	var site *model.Location
	if e.Site != nil {
		site = location(res, e.Site.Pos())
	}
	return model.NavigationEdge{
		Kind:       model.EdgeCalls,
		From:       fromID,
		To:         toID,
		Confidence: conf,
		Source:     g.source,
		Site:       site,
	}, true
}

// Implementations answers FR-15's third question for symbolID: for an
// interface type, every concrete type implementing it; for a concrete type,
// every interface it implements (the reverse direction — useful for FR-19's
// blast radius); and the same at method granularity (see
// buildImplementsEdges). symbolID can be either end of an EdgeImplements
// edge; which edges come back depends only on which side matched.
func Implementations(res *Result, symbolID string) ([]model.NavigationEdge, error) {
	g, err := graphFor(res)
	if err != nil {
		return nil, err
	}
	var out []model.NavigationEdge
	for _, e := range g.implEdges {
		if e.From == symbolID || e.To == symbolID {
			out = append(out, e)
		}
	}
	return dedupEdges(out), nil
}

// scopeType is one named type declared at package scope in res's loaded
// scope, indexed for buildImplementsEdges.
type scopeType struct {
	pkgPath string
	name    string
	named   *types.Named
}

// scopeNamedTypes lists every named, non-generic type declared at package
// scope across res.Packages — FR-13's "changed packages plus their in-module
// reverse importers", never res.ByPath's full dependency closure. That is a
// deliberate bound, not an oversight: matching every concrete type against
// every interface transitively reachable (the whole standard library
// included) would be unbounded and mostly noise for a review tool, and
// loader.go's own scoping already treats "in-module" as the unit of
// relevance everywhere else (FR-19's "blast radius" is explicitly
// "in-module callers"). An interface or type symbolID from outside the scope
// can still be queried — Implementations just will not find scope members
// satisfying it beyond what Implements can decide from its own definition.
//
// A generic type's uninstantiated Named (TypeParams().Len() > 0) is skipped:
// types.Implements's own doc says its result is unspecified for one.
func scopeNamedTypes(res *Result) []scopeType {
	seenPkg := map[*types.Package]bool{}
	var out []scopeType
	for _, p := range res.Packages {
		if p.Types == nil || seenPkg[p.Types] {
			continue
		}
		seenPkg[p.Types] = true
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			out = append(out, scopeType{pkgPath: p.PkgPath, name: name, named: named})
		}
	}
	return out
}

// buildImplementsEdges computes every EdgeImplements edge in res's loaded
// scope: for every (interface, concrete type) pair where types.Implements
// holds for the concrete type or its pointer, one type-level edge (From the
// concrete type, To the interface — "T implements I"), plus one method-level
// edge per interface method (From the concrete method satisfying it, To the
// interface method — "for an interface method, the corresponding concrete
// methods"). Implementations(symbolID) then just filters this list by
// whichever side matches, which is what makes "for a concrete method, also
// list the interfaces it satisfies" fall out for free: it is the same edges,
// queried from their From side instead of their To side.
//
// A pointer method set (types.NewMethodSet(types.NewPointer(T))) is used to
// find the concrete method regardless of which receiver kind actually
// satisfied the interface — it is the superset of a value method set, so one
// lookup covers both.
func buildImplementsEdges(res *Result) []model.NavigationEdge {
	if res == nil {
		return nil
	}
	all := scopeNamedTypes(res)

	var ifaces, concretes []scopeType
	for _, t := range all {
		if _, ok := t.named.Underlying().(*types.Interface); ok {
			ifaces = append(ifaces, t)
		} else {
			concretes = append(concretes, t)
		}
	}

	var out []model.NavigationEdge
	for _, iface := range ifaces {
		ifaceType := iface.named.Underlying().(*types.Interface)
		ifaceID := goID(iface.pkgPath, "", iface.name)

		for _, concrete := range concretes {
			if !types.Implements(concrete.named, ifaceType) && !types.Implements(types.NewPointer(concrete.named), ifaceType) {
				continue
			}
			concreteID := goID(concrete.pkgPath, "", concrete.name)
			out = append(out, model.NavigationEdge{
				Kind: model.EdgeImplements, From: concreteID, To: ifaceID,
				Confidence: model.ConfStatic, Source: "types",
			})

			ms := types.NewMethodSet(types.NewPointer(concrete.named))
			for i := 0; i < ifaceType.NumMethods(); i++ {
				m := ifaceType.Method(i)
				sel := ms.Lookup(m.Pkg(), m.Name())
				if sel == nil {
					continue
				}
				cf, ok := sel.Obj().(*types.Func)
				if !ok {
					continue
				}
				out = append(out, model.NavigationEdge{
					Kind:       model.EdgeImplements,
					From:       goID(concrete.pkgPath, concrete.name, cf.Name()),
					To:         goID(iface.pkgPath, iface.name, m.Name()),
					Confidence: model.ConfStatic,
					Source:     "types",
				})
			}
		}
	}
	return out
}

// dedupEdges removes (Kind, From, To, Site) duplicates and sorts the result
// deterministically (by To, then From, then Site), so repeated queries and
// golden tests both see a stable order regardless of map iteration or which
// of several ssa.Function variants (see callGraph.funcsByID) produced a given
// edge first.
func dedupEdges(edges []model.NavigationEdge) []model.NavigationEdge {
	type key struct {
		kind, from, to string
		path           string
		line, col      int
	}
	seen := make(map[key]bool, len(edges))
	out := make([]model.NavigationEdge, 0, len(edges))
	for _, e := range edges {
		k := key{kind: string(e.Kind), from: e.From, to: e.To}
		if e.Site != nil {
			k.path, k.line, k.col = e.Site.Path, e.Site.Line, e.Site.Col
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.To != b.To {
			return a.To < b.To
		}
		if a.From != b.From {
			return a.From < b.From
		}
		as, bs := a.Site, b.Site
		switch {
		case as == nil && bs == nil:
			return false
		case as == nil:
			return true
		case bs == nil:
			return false
		case as.Path != bs.Path:
			return as.Path < bs.Path
		case as.Line != bs.Line:
			return as.Line < bs.Line
		default:
			return as.Col < bs.Col
		}
	})
	return out
}

// ContextText writes a plain-text report of symbolID's callers, callees and
// implementations in res, grouped by section and then by Confidence. This is
// the `flowreview context <pkg.Func>` CLI's eventual output — FR-12 owns
// cmd/, so this is exposed for it to call later and is not wired up to any
// command here.
func ContextText(res *Result, symbolID string, w io.Writer) error {
	callers, err := Callers(res, symbolID)
	if err != nil {
		return fmt.Errorf("golang: ContextText: callers: %w", err)
	}
	callees, err := Callees(res, symbolID)
	if err != nil {
		return fmt.Errorf("golang: ContextText: callees: %w", err)
	}
	impls, err := Implementations(res, symbolID)
	if err != nil {
		return fmt.Errorf("golang: ContextText: implementations: %w", err)
	}

	fmt.Fprintf(w, "%s\n", symbolID)
	writeEdgeSection(w, "Callers", callers, func(e model.NavigationEdge) string { return e.From })
	writeEdgeSection(w, "Callees", callees, func(e model.NavigationEdge) string { return e.To })
	writeEdgeSection(w, "Implementations", impls, func(e model.NavigationEdge) string {
		if e.From == symbolID {
			return e.To
		}
		return e.From
	})
	return nil
}

// writeEdgeSection writes one ContextText section: a title with a count,
// "none" when empty, otherwise edges grouped under a "[confidence]" heading
// in a fixed order, each printed as other(edge) and its call site when known.
// other picks whichever end of the edge is not the symbol the section is
// about, so the printed name is always "the other side", whichever direction
// the edge happens to run.
func writeEdgeSection(w io.Writer, title string, edges []model.NavigationEdge, other func(model.NavigationEdge) string) {
	fmt.Fprintf(w, "\n%s (%d):\n", title, len(edges))
	if len(edges) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	byConf := map[model.Confidence][]model.NavigationEdge{}
	for _, e := range edges {
		byConf[e.Confidence] = append(byConf[e.Confidence], e)
	}
	for _, conf := range []model.Confidence{model.ConfStatic, model.ConfInterfaceDispatch, model.ConfHeuristic} {
		group := byConf[conf]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "  [%s]\n", conf)
		for _, e := range group {
			site := ""
			if e.Site != nil {
				site = fmt.Sprintf(" (%s:%d:%d)", e.Site.Path, e.Site.Line, e.Site.Col)
			}
			fmt.Fprintf(w, "    %s%s\n", other(e), site)
		}
	}
}
