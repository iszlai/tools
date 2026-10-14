// Package testreach is FR-21: which changed functions a diff touches are
// still reachable from a test, and which are not. It walks FR-15's call
// graph forward from every Test*/Benchmark*/Example*/Fuzz* function in the
// load set -- golang.EntryPoints' KindTest, which already covers both the
// internal "[pkg.test]" variant and the external "pkg_test" package variant
// loader.go's Tests:true produces (see symbols.go's FileToPkg ownership rule,
// which attributes each _test.go file's declarations to exactly one package
// variant, and callgraph.go's funcSymbolID, which resolves a function
// declared in more than one variant to the same FR-14-shaped ID either way)
// -- and reports, for each changed function, whether some test's forward
// reach includes it.
package testreach

import (
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// maxDepth caps how many call-graph hops a BFS from one test follows: deep
// enough for realistic indirection (a handler calling a service calling a
// repository), shallow enough that a pathological fixture cannot make one
// query unbounded.
const maxDepth = 10

// maxBy caps how many reaching tests FunctionReach.By lists.
const maxBy = 5

// FunctionReach is one changed function's test-reach result.
type FunctionReach struct {
	Symbol  model.CodeSymbol `json:"symbol"`
	Reached bool             `json:"reached"`
	// By is the symbol ID of every test that reaches Symbol, shortest call
	// chain first, capped at maxBy.
	By []string `json:"by,omitempty"`
	// Path is the call chain for By[0] -- the closest reaching test -- from
	// the test itself through Symbol, inclusive of both ends. Empty when
	// Reached is false.
	Path []string `json:"path,omitempty"`
}

// PackageCommand is a copyable `go test` invocation that runs exactly the
// tests that reach changed code and are declared in one package directory.
type PackageCommand struct {
	Dir     string   `json:"dir"`     // package directory, relative to the module root ("." for the root package)
	Tests   []string `json:"tests"`   // bare test function names, sorted
	Command string   `json:"command"` // e.g. "go test ./internal/foo -run '^(TestA|TestB)$'"
}

// Report is FR-21's full result for one review.
type Report struct {
	Functions []FunctionReach    `json:"functions"`
	Unreached []model.CodeSymbol `json:"unreached"` // Functions where Reached is false, for a reader who only wants the gap
	Commands  []PackageCommand   `json:"commands"`
}

// Compute finds which of files' changed functions are reachable from a test
// in res's load set. res must come from a Loader that loaded with Tests:true
// (loader.go's default) so _test.go files, internal and external "_test"
// package alike, are part of the scope EntryPoints and the call graph see.
func Compute(res *golang.Result, files []model.ChangedFile) (Report, error) {
	if res == nil {
		return Report{}, fmt.Errorf("testreach: nil Result")
	}

	changed := changedFunctions(res, files)
	if len(changed) == 0 {
		return Report{}, nil
	}

	tests := testEntryPoints(res)
	hits, err := reachFromTests(res, tests)
	if err != nil {
		return Report{}, err
	}

	return buildReport(changed, hits, tests), nil
}

// changedFunctions is files' changed symbols narrowed to funcs and methods:
// a changed type or interface has no call-graph presence to walk.
func changedFunctions(res *golang.Result, files []model.ChangedFile) []model.CodeSymbol {
	var out []model.CodeSymbol
	for _, s := range golang.ChangedSymbols(res, files) {
		if s.Kind == model.SymFunc || s.Kind == model.SymMethod {
			out = append(out, s)
		}
	}
	return out
}

// testEntryPoints is every golang.KindTest entry point in res: Test*,
// Benchmark*, Example* and Fuzz* functions. A zero golang.Config is enough --
// FR-16's test heuristic needs no .flowreview.yaml patterns and no call
// graph (cfg.HasCallers).
func testEntryPoints(res *golang.Result) []golang.EntryPoint {
	var out []golang.EntryPoint
	for _, ep := range golang.EntryPoints(res, golang.Config{}) {
		if ep.Kind == golang.KindTest {
			out = append(out, ep)
		}
	}
	return out
}

// hit is one test's shortest path to a symbol it reaches.
type hit struct {
	testID string
	path   []string // symbol IDs: the test itself through the reached symbol, inclusive
}

// reachFromTests runs a forward BFS over res's call graph from every test in
// tests, and returns every symbol reached, keyed by its ID, with one hit per
// reaching test.
func reachFromTests(res *golang.Result, tests []golang.EntryPoint) (map[string][]hit, error) {
	out := map[string][]hit{}
	for _, t := range tests {
		paths, err := bfs(res, t.Symbol.ID)
		if err != nil {
			return nil, err
		}
		for targetID, p := range paths {
			out[targetID] = append(out[targetID], hit{testID: t.Symbol.ID, path: p})
		}
	}
	return out, nil
}

// bfs walks golang.Callees forward from rootID, breadth-first so the first
// path recorded to any symbol is a shortest one, up to maxDepth hops.
// Confidence is never filtered on: a changed function reached only through
// an interface call is exactly what FR-21 wants to surface, not hide.
func bfs(res *golang.Result, rootID string) (map[string][]string, error) {
	visited := map[string]bool{rootID: true}
	paths := map[string][]string{}
	queue := [][]string{{rootID}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(cur)-1 >= maxDepth {
			continue
		}
		callees, err := golang.Callees(res, cur[len(cur)-1])
		if err != nil {
			return nil, err
		}
		for _, e := range callees {
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			next := append(append([]string{}, cur...), e.To)
			paths[e.To] = next
			queue = append(queue, next)
		}
	}
	return paths, nil
}

// buildReport assembles Report from changed's symbols and the reach hits
// every test's BFS produced. tests is needed only to resolve, for
// buildCommands, the package directory and bare name of each reaching test.
func buildReport(changed []model.CodeSymbol, hits map[string][]hit, tests []golang.EntryPoint) Report {
	r := Report{Functions: make([]FunctionReach, 0, len(changed))}
	reachingTests := map[string]bool{}

	for _, sym := range changed {
		hs := append([]hit(nil), hits[sym.ID]...)
		sort.Slice(hs, func(i, j int) bool {
			if len(hs[i].path) != len(hs[j].path) {
				return len(hs[i].path) < len(hs[j].path)
			}
			return hs[i].testID < hs[j].testID
		})

		fr := FunctionReach{Symbol: sym, Reached: len(hs) > 0}
		for i, h := range hs {
			if i >= maxBy {
				break
			}
			fr.By = append(fr.By, h.testID)
			reachingTests[h.testID] = true
		}
		if len(hs) > 0 {
			fr.Path = hs[0].path
		}
		r.Functions = append(r.Functions, fr)
		if !fr.Reached {
			r.Unreached = append(r.Unreached, sym)
		}
	}

	r.Commands = buildCommands(tests, reachingTests)
	return r
}

// buildCommands groups every test in reachingTests by the directory its own
// declaration lives in -- go test runs by package directory, not by symbol
// ID, and a test and the changed code it reaches need not share one -- and
// renders one copyable `go test` invocation per directory.
func buildCommands(tests []golang.EntryPoint, reachingTests map[string]bool) []PackageCommand {
	byDir := map[string]map[string]bool{}
	for _, t := range tests {
		if !reachingTests[t.Symbol.ID] {
			continue
		}
		dir := path.Dir(t.Symbol.Path)
		if byDir[dir] == nil {
			byDir[dir] = map[string]bool{}
		}
		byDir[dir][t.Symbol.Name] = true
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	out := make([]PackageCommand, 0, len(dirs))
	for _, dir := range dirs {
		names := make([]string, 0, len(byDir[dir]))
		for n := range byDir[dir] {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, PackageCommand{Dir: dir, Tests: names, Command: goTestCommand(dir, names)})
	}
	return out
}

// goTestCommand renders dir and names as a copyable `go test` invocation: a
// regex-escaped, anchored alternation so -run matches exactly these test
// names and nothing a shared prefix would also match (e.g. TestFoo and
// TestFooBar both existing).
func goTestCommand(dir string, names []string) string {
	escaped := make([]string, len(names))
	for i, n := range names {
		escaped[i] = regexp.QuoteMeta(n)
	}
	pattern := fmt.Sprintf("^(%s)$", strings.Join(escaped, "|"))
	pkg := "./" + dir
	if dir == "." {
		pkg = "."
	}
	return fmt.Sprintf("go test %s -run '%s'", pkg, pattern)
}

// Text writes a one-screen plain-text report of r. FR-24 mounts this as
// `flowreview tests`.
func Text(r Report, w io.Writer) error {
	reached := 0
	for _, f := range r.Functions {
		if f.Reached {
			reached++
		}
	}
	if _, err := fmt.Fprintf(w, "Test reach: %d/%d changed functions reached by a test\n", reached, len(r.Functions)); err != nil {
		return err
	}
	for _, f := range r.Functions {
		mark := " "
		if f.Reached {
			mark = "x"
		}
		if _, err := fmt.Fprintf(w, "  [%s] %s\n", mark, f.Symbol.ID); err != nil {
			return err
		}
		if f.Reached {
			if _, err := fmt.Fprintf(w, "        by %s\n", strings.Join(f.By, ", ")); err != nil {
				return err
			}
		}
	}

	if len(r.Commands) > 0 {
		if _, err := fmt.Fprintln(w, "\nSuggested:"); err != nil {
			return err
		}
		for _, c := range r.Commands {
			if _, err := fmt.Fprintf(w, "  %s\n", c.Command); err != nil {
				return err
			}
		}
	}
	return nil
}
