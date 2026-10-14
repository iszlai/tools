// golink.go reconciles golang's exact-tier symbol ids with the
// structural-tier ids Gherkin step definitions resolve their handlers to,
// and wires golang.EntryPoints to Go's own call graph -- the two pieces of
// glue FR-17's board comments ask this slice to add.
package order

import (
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// symbolIndex is every symbol golang.Symbols(res) reports, by id.
func symbolIndex(res *golang.Result) map[string]model.CodeSymbol {
	out := map[string]model.CodeSymbol{}
	for _, s := range golang.Symbols(res) {
		out[s.ID] = s
	}
	return out
}

// entryPointIndex runs golang.EntryPoints with Config.HasCallers wired to
// res's own call graph (FR-17's board comment: "wire golang.EntryPoints
// with Config.HasCallers from the FR-15 graph"), and dedupes a symbol that
// matched more than one heuristic down to the single Kind/Reason pair Flow
// shows -- the same comment's "a symbol may appear under several
// entry-point kinds; dedupe for ordering".
func entryPointIndex(res *golang.Result, cfg golang.Config) map[string]golang.EntryPoint {
	cfg.HasCallers = func(id string) bool {
		edges, err := golang.Callers(res, id)
		return err == nil && len(edges) > 0
	}
	out := map[string]golang.EntryPoint{}
	for _, ep := range golang.EntryPoints(res, cfg) {
		existing, ok := out[ep.Symbol.ID]
		if !ok || entryPriority(ep.Kind) < entryPriority(existing.Kind) {
			out[ep.Symbol.ID] = ep
		}
	}
	return out
}

// entryPriority orders entry-point kinds for the dedupe above: a
// deliberate, specific registration (main, a framework handler, a gRPC
// method, a cobra command, a test) is always a better explanation than "a
// user pattern of unknown specificity" or "nothing in the module happens to
// call this", so those two sort last.
func entryPriority(kind string) int {
	switch kind {
	case golang.KindMain:
		return 0
	case golang.KindInit:
		return 1
	case golang.KindHTTPHandler:
		return 2
	case golang.KindGRPCMethod:
		return 3
	case golang.KindCobraCommand:
		return 4
	case golang.KindTest:
		return 5
	case golang.KindCustom:
		return 6
	case golang.KindUnreferenced:
		return 7
	default:
		return 8
	}
}

// structuralSymbolID is a type-checked CodeSymbol's id in the structural
// tier's convention (golang/symbols.go's structuralID, treesitter's
// buildSymbol): "path#Name" or "path#Recv.Name", receiver bare. Gherkin
// step definitions resolve their Go handler through the structural tier
// only (gherkin/stepdef.go uses treesitter, never go/packages) and never
// learn the exact-tier id the Go call graph is actually keyed by; this is
// how spliceScenarios reconciles the two.
func structuralSymbolID(sym model.CodeSymbol) string {
	recv := strings.TrimPrefix(sym.Receiver, "*")
	if recv == "" {
		return sym.Path + "#" + sym.Name
	}
	return sym.Path + "#" + recv + "." + sym.Name
}

// spliceScenarios maps an exact-tier Go symbol id to the Gherkin scenarios
// that reach it as a step definition's handler, when the ids map -- not
// every step definition resolves a handler, and a resolved handler is not
// always inside the loaded Go scope at all; both are silently skipped, the
// same "never invent a match" rule gherkin.Link itself follows.
func spliceScenarios(res *golang.Result, ix *gherkin.Index) map[string][]model.CodeSymbol {
	if res == nil || ix == nil {
		return nil
	}
	exactByStructural := map[string]string{}
	for _, s := range golang.Symbols(res) {
		exactByStructural[structuralSymbolID(s)] = s.ID
	}

	out := map[string][]model.CodeSymbol{}
	for _, def := range ix.StepDefs() {
		if def.HandlerID == "" || def.Language != "go" {
			continue // a Java/JS handler id can never map into the Go call graph
		}
		exactID, ok := exactByStructural[def.HandlerID]
		if !ok {
			continue
		}
		scenarios := ix.ScenariosUsingStepDef(def.HandlerID)
		if len(scenarios) == 0 {
			continue
		}
		out[exactID] = dedupeSymbols(append(out[exactID], scenarios...))
	}
	return out
}

// dedupeSymbols removes id duplicates and sorts by id, for deterministic
// output regardless of map iteration order upstream.
func dedupeSymbols(syms []model.CodeSymbol) []model.CodeSymbol {
	seen := map[string]bool{}
	out := make([]model.CodeSymbol, 0, len(syms))
	for _, s := range syms {
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
