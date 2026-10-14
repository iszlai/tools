package order

import (
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// maxFlowDepth and maxFlowNodes bound the backward search from a changed
// symbol to an entry point: the plan asks for "cap depth ~6 and node count"
// so a pathological fan-in (a logging helper called from half the repo)
// can never make flow ordering slow or its output unreadable.
const (
	maxFlowDepth = 6
	maxFlowNodes = 500
)

// flowNode is one symbol in the subgraph Flow builds: a changed symbol, one
// of its callers (found by walking backward from it), or an entry point
// reached that way -- a Go one, or a Gherkin scenario spliced in via a step
// definition (see spliceScenarios).
type flowNode struct {
	sym       model.CodeSymbol
	changed   bool // its own declaration is touched by a hunk
	isEntry   bool
	entryKind string // a golang.Kind* constant, "scenario", or ""
	depth     int    // hops from the nearest seed (0 = a seed itself)
}

// Flow is FR-17's primary ordering: group changed files by which entry
// point's call chain reaches them, order files within a group callers-first,
// and order the groups themselves by where they first appear in the diff.
// A file whose changed declarations never connect to Go or Gherkin analysis
// -- including every file when Go and Gherkin inputs are both nil -- lands
// in Order.Unclassified instead of a Group.
func Flow(in Inputs) Order {
	b := newFlowBuilder(in)
	b.run()
	return b.order()
}

// flowBuilder holds the state one Flow call builds and reads back: the
// discovered subgraph (nodes, and the caller edges found while walking it),
// derived once run has finished into topological order, connected
// components and finally Groups.
type flowBuilder struct {
	in Inputs

	fileByPath map[string]model.ChangedFile

	nodes    map[string]*flowNode
	incoming map[string][]string // node id -> ids of its discovered callers (full fan-in)
	outgoing map[string][]string // inverse of incoming: caller id -> callee ids, for topo sort

	scenariosFor map[string][]model.CodeSymbol // exact Go handler id -> scenarios reaching it via a step
}

func newFlowBuilder(in Inputs) *flowBuilder {
	fileByPath := make(map[string]model.ChangedFile, len(in.Files))
	for _, f := range in.Files {
		fileByPath[f.Path()] = f
	}
	return &flowBuilder{
		in:         in,
		fileByPath: fileByPath,
		nodes:      map[string]*flowNode{},
		incoming:   map[string][]string{},
	}
}

// run performs the backward BFS: seed every changed Go symbol and every
// changed Gherkin scenario, then repeatedly look for each frontier node's
// callers (real Go callers, plus any scenario reaching it as a step
// definition's handler) until an entry point is found on every branch, or
// maxFlowDepth/maxFlowNodes cuts it off.
func (b *flowBuilder) run() {
	var entryByID map[string]golang.EntryPoint
	var symByID map[string]model.CodeSymbol
	if b.in.Go != nil {
		symByID = symbolIndex(b.in.Go)
		entryByID = entryPointIndex(b.in.Go, b.in.GoConfig)
		b.scenariosFor = spliceScenarios(b.in.Go, b.in.Gherkin)
	}

	var queue []string
	seed := func(sym model.CodeSymbol) {
		if _, ok := b.nodes[sym.ID]; ok {
			return
		}
		n := &flowNode{sym: sym, changed: true}
		classifyEntry(n, entryByID)
		b.nodes[sym.ID] = n
		if !n.isEntry {
			queue = append(queue, sym.ID)
		}
	}

	if b.in.Go != nil {
		for _, sym := range golang.ChangedSymbols(b.in.Go, b.in.Files) {
			seed(sym)
		}
	}
	if b.in.Gherkin != nil {
		for _, sym := range b.changedScenarios() {
			seed(sym)
		}
	}

	for len(queue) > 0 && len(b.nodes) < maxFlowNodes {
		id := queue[0]
		queue = queue[1:]
		cur := b.nodes[id]
		if cur.depth >= maxFlowDepth {
			continue
		}

		var callers []model.CodeSymbol
		if symByID != nil {
			if _, ok := symByID[id]; ok {
				if edges, err := golang.Callers(b.in.Go, id); err == nil {
					for _, e := range edges {
						if sym, ok := symByID[e.From]; ok {
							callers = append(callers, sym)
						}
					}
				}
			}
			callers = append(callers, b.scenariosFor[id]...)
		}
		sort.Slice(callers, func(i, j int) bool { return callers[i].ID < callers[j].ID })

		for _, csym := range callers {
			if len(b.nodes) >= maxFlowNodes {
				break
			}
			if _, exists := b.nodes[csym.ID]; !exists {
				n := &flowNode{sym: csym, depth: cur.depth + 1}
				classifyEntry(n, entryByID)
				b.nodes[csym.ID] = n
				if !n.isEntry {
					queue = append(queue, csym.ID)
				}
			}
			b.incoming[id] = appendUnique(b.incoming[id], csym.ID)
		}
	}

	b.buildOutgoing()
}

func (b *flowBuilder) buildOutgoing() {
	b.outgoing = map[string][]string{}
	for callee, callers := range b.incoming {
		for _, c := range callers {
			b.outgoing[c] = appendUnique(b.outgoing[c], callee)
		}
	}
}

// changedScenarios returns every Gherkin scenario entry point whose range
// overlaps a hunk in the diff -- the board comment's "a changed scenario...
// makes the scenario an entry point". A changed *step definition* reaches
// its scenario through the normal backward search instead (see
// spliceScenarios): this only covers a .feature edit itself.
func (b *flowBuilder) changedScenarios() []model.CodeSymbol {
	hunksByPath := map[string][][2]int{}
	for _, f := range b.in.Files {
		if f.Status == model.FileDeleted {
			continue
		}
		for _, h := range f.Hunks {
			s, e := newSideRange(h)
			hunksByPath[f.Path()] = append(hunksByPath[f.Path()], [2]int{s, e})
		}
	}

	var out []model.CodeSymbol
	for _, ep := range b.in.Gherkin.EntryPoints() {
		for _, r := range hunksByPath[ep.Symbol.Path] {
			if rangesOverlap(ep.Symbol.Range.StartLine, ep.Symbol.Range.EndLine, r[0], r[1]) {
				out = append(out, ep.Symbol)
				break
			}
		}
	}
	return out
}

func newSideRange(h model.DiffHunk) (start, end int) {
	if h.NewLines == 0 {
		return h.NewStart, h.NewStart
	}
	return h.NewStart, h.NewStart + h.NewLines - 1
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

func classifyEntry(n *flowNode, entryByID map[string]golang.EntryPoint) {
	if n.sym.Kind == model.SymScenario {
		n.isEntry = true
		n.entryKind = "scenario"
		return
	}
	if entryByID == nil {
		return
	}
	if ep, ok := entryByID[n.sym.ID]; ok {
		n.isEntry = true
		n.entryKind = ep.Kind
	}
}

func appendUnique(list []string, id string) []string {
	for _, x := range list {
		if x == id {
			return list
		}
	}
	return append(list, id)
}

// order turns the discovered subgraph into Groups (one per connected
// component with at least one changed file) plus Unclassified (every
// changed file no Group claimed).
func (b *flowBuilder) order() Order {
	comps := connectedComponents(b.nodeIDs(), b.incoming, b.outgoing)

	placed := map[string]bool{} // changed file paths already placed in a Group
	var groups []Group
	for _, comp := range comps {
		topo := topoOrder(comp, b.outgoing)
		position := make(map[string]int, len(topo))
		for i, id := range topo {
			position[id] = i
		}

		files := b.filesForComponent(comp, position, placed)
		if len(files) == 0 {
			continue
		}
		entry, title := b.titleFor(topo)
		groups = append(groups, Group{Title: title, EntryPoint: entry, Files: files})
	}

	sort.SliceStable(groups, func(i, j int) bool {
		return b.firstDiffIndex(groups[i]) < b.firstDiffIndex(groups[j])
	})

	return Order{Kind: KindFlow, Groups: groups, Unclassified: b.unclassified(placed)}
}

func (b *flowBuilder) nodeIDs() []string {
	out := make([]string, 0, len(b.nodes))
	for id := range b.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// filesForComponent builds comp's FileEntry list: one per changed file with
// at least one changed node in comp (and not already claimed by an earlier
// component -- see order's placed map), positioned by that file's
// earliest-appearing changed node in topo order ("files ordered by first
// appearance of their changed symbols"), Why built from the path that node
// took to comp's entry point.
func (b *flowBuilder) filesForComponent(comp []string, position map[string]int, placed map[string]bool) []FileEntry {
	type hit struct {
		fileID  string
		pos     int
		why     string
		symbols []string
	}
	byPath := map[string]*hit{}
	var order []string // first-seen path order, for a stable map iteration below

	for _, id := range comp {
		n := b.nodes[id]
		if !n.changed {
			continue
		}
		f, ok := b.fileByPath[n.sym.Path]
		if !ok {
			continue
		}
		h := byPath[n.sym.Path]
		if h == nil {
			h = &hit{fileID: f.ID, pos: position[id], why: b.why(id)}
			byPath[n.sym.Path] = h
			order = append(order, n.sym.Path)
		} else if position[id] < h.pos {
			h.pos, h.why = position[id], b.why(id)
		}
		h.symbols = append(h.symbols, symbolLabel(n.sym))
	}

	var out []FileEntry
	for _, path := range order {
		if placed[path] {
			continue
		}
		h := byPath[path]
		symbols := append([]string{}, h.symbols...)
		sort.Strings(symbols)
		out = append(out, FileEntry{
			FileID:  h.fileID,
			Path:    path,
			Why:     h.why,
			Symbols: symbols,
			Hotspot: hotspotOf(b.in, path),
		})
		placed[path] = true
	}

	sort.Slice(out, func(i, j int) bool {
		pi, pj := byPath[out[i].Path].pos, byPath[out[j].Path].pos
		if pi != pj {
			return pi < pj
		}
		if out[i].Hotspot != out[j].Hotspot {
			return out[i].Hotspot > out[j].Hotspot
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// why narrates the call path from a changed node up toward its component's
// entry point: "called from <immediate caller>" plus one "← <next hop>"
// segment per additional hop, each hop marked "(changed)" when it is itself
// a changed symbol. Ties among several callers of the same node are broken
// by id, the same deterministic rule topoOrder uses, so the narrated path
// is always one topoOrder and connectedComponents already agree this node
// reaches.
func (b *flowBuilder) why(id string) string {
	callers := append([]string{}, b.incoming[id]...)
	if len(callers) == 0 {
		return "no in-repo callers found"
	}
	sort.Strings(callers)
	cur := callers[0]

	var hops []string
	for i := 0; i <= maxFlowDepth; i++ {
		n := b.nodes[cur]
		if n == nil {
			break
		}
		hops = append(hops, hopLabel(n))
		if n.isEntry {
			break
		}
		next := append([]string{}, b.incoming[cur]...)
		if len(next) == 0 {
			break
		}
		sort.Strings(next)
		cur = next[0]
	}
	if len(hops) == 0 {
		return "no in-repo callers found"
	}
	return "called from " + strings.Join(hops, " ← ")
}

func hopLabel(n *flowNode) string {
	if n.isEntry {
		return entryLabel(n.entryKind) + " " + n.sym.Name
	}
	label := symbolLabel(n.sym)
	if n.changed {
		label += " (changed)"
	}
	return label
}

func entryLabel(kind string) string {
	switch kind {
	case golang.KindMain:
		return "main"
	case golang.KindInit:
		return "init"
	case golang.KindHTTPHandler:
		return "handler"
	case golang.KindGRPCMethod:
		return "gRPC method"
	case golang.KindCobraCommand:
		return "cobra command"
	case golang.KindTest:
		return "test"
	case golang.KindUnreferenced:
		return "exported function"
	case "scenario":
		return "scenario"
	default:
		return "entry point"
	}
}

func symbolLabel(sym model.CodeSymbol) string {
	if sym.Receiver != "" {
		return strings.TrimPrefix(sym.Receiver, "*") + "." + sym.Name
	}
	return sym.Name
}

// titleFor names a Group from comp's topological order: the first entry
// point topoOrder places (callers-first, so the most upstream one when more
// than one reaches this component), or -- when none does -- the first
// changed symbol, labeled to say no entry point was found.
func (b *flowBuilder) titleFor(topo []string) (*model.CodeSymbol, string) {
	for _, id := range topo {
		n := b.nodes[id]
		if n.isEntry {
			sym := n.sym
			return &sym, entryLabel(n.entryKind) + " " + symbolLabel(n.sym)
		}
	}
	for _, id := range topo {
		n := b.nodes[id]
		if n.changed {
			return nil, symbolLabel(n.sym) + " (no entry point found)"
		}
	}
	return nil, "unknown" // unreachable: every component was seeded from a changed node or an entry point
}

// firstDiffIndex is the earliest position in in.Files any of g's files
// holds, used to order Groups the way the diff itself presents them.
func (b *flowBuilder) firstDiffIndex(g Group) int {
	best := len(b.in.Files)
	for i, f := range b.in.Files {
		for _, fe := range g.Files {
			if fe.FileID == f.ID && i < best {
				best = i
			}
		}
	}
	return best
}

// unclassified lists every changed file no Group claimed: files with no Go
// or Gherkin symbol at all (docs, config, other languages) and Go files
// whose diff touched no top-level declaration (import/comment/whitespace-
// only changes). Ordered by Go import depth -- approximated as the
// shallowest BFS depth any of the file's own symbols reached -- then by
// hotspot (higher first), then by path.
func (b *flowBuilder) unclassified(placed map[string]bool) []FileEntry {
	depth := map[string]int{}
	for _, n := range b.nodes {
		if d, ok := depth[n.sym.Path]; !ok || n.depth < d {
			depth[n.sym.Path] = n.depth
		}
	}
	depthOf := func(path string) int {
		if d, ok := depth[path]; ok {
			return d
		}
		return 1 << 30
	}

	var out []FileEntry
	for _, f := range b.in.Files {
		path := f.Path()
		if placed[path] {
			continue
		}
		out = append(out, FileEntry{FileID: f.ID, Path: path, Hotspot: hotspotOf(b.in, path)})
	}

	sort.Slice(out, func(i, j int) bool {
		di, dj := depthOf(out[i].Path), depthOf(out[j].Path)
		if di != dj {
			return di < dj
		}
		if out[i].Hotspot != out[j].Hotspot {
			return out[i].Hotspot > out[j].Hotspot
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// connectedComponents groups ids by undirected reachability over incoming
// and outgoing, each returned component sorted by id for determinism; ids
// itself must already be sorted, so which node starts each BFS -- and
// therefore the components' relative order before order's own firstDiffIndex
// sort -- never depends on map iteration order.
func connectedComponents(ids []string, incoming, outgoing map[string][]string) [][]string {
	visited := map[string]bool{}
	var comps [][]string
	for _, id := range ids {
		if visited[id] {
			continue
		}
		var comp []string
		queue := []string{id}
		visited[id] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			comp = append(comp, cur)
			neighbors := append(append([]string{}, incoming[cur]...), outgoing[cur]...)
			sort.Strings(neighbors)
			for _, nb := range neighbors {
				if !visited[nb] {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
		sort.Strings(comp)
		comps = append(comps, comp)
	}
	return comps
}

// topoOrder sorts ids callers-first (Kahn's algorithm over outgoing, the
// caller -> callee adjacency built after the BFS) so a reviewer reads a
// chain top to bottom. A cycle -- real recursion is common in a call graph
// -- is broken deterministically: when no remaining node has indegree zero,
// the lexicographically smallest remaining id is released next, the same
// tie-break rule used everywhere else ids need one.
func topoOrder(ids []string, outgoing map[string][]string) []string {
	idset := make(map[string]bool, len(ids))
	for _, id := range ids {
		idset[id] = true
	}
	indegree := make(map[string]int, len(ids))
	for _, id := range ids {
		indegree[id] = 0
	}
	for _, id := range ids {
		for _, to := range outgoing[id] {
			if idset[to] {
				indegree[to]++
			}
		}
	}

	emitted := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))

	for len(out) < len(ids) {
		var ready []string
		for _, id := range ids {
			if !emitted[id] && indegree[id] == 0 {
				ready = append(ready, id)
			}
		}
		if len(ready) == 0 {
			var rest []string
			for _, id := range ids {
				if !emitted[id] {
					rest = append(rest, id)
				}
			}
			sort.Strings(rest)
			ready = rest[:1]
		} else {
			sort.Strings(ready)
		}
		for _, id := range ready {
			emitted[id] = true
			out = append(out, id)
			for _, to := range outgoing[id] {
				if idset[to] && !emitted[to] {
					indegree[to]--
				}
			}
		}
	}
	return out
}
