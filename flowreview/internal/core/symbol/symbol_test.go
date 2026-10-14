package symbol

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// pkgGoSrc is the fixture's one package: Caller calls Helper (line 18 is the
// call site), English implements Greeter. Line numbers below are load-
// bearing for the tests -- keep them in sync with any edit.
const pkgGoSrc = `package pkg

type Greeter interface {
	Greet() string
}

type English struct{}

func (English) Greet() string {
	return "hello"
}

func Helper() int {
	return 1
}

func Caller() int {
	return Helper() + 1
}
`

const mainGoSrc = `package main

import "symtest.example/app/pkg"

func main() {
	pkg.Caller()
}
`

// writeGoFixture builds a tiny two-package module on disk (no git needed --
// golang.Loader only needs a go.mod, see loader_test.go's own fixtureDir)
// with pkg (Caller, Helper, Greeter/English) and cmd/app (a reverse importer
// whose main() calls pkg.Caller, left untouched by the "changed" file list so
// it is always a target outside the diff).
func writeGoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module symtest.example/app\n\ngo 1.25\n")
	write("pkg/pkg.go", pkgGoSrc)
	write("cmd/app/main.go", mainGoSrc)
	return dir
}

func loadFixture(t *testing.T, dir string) *golang.Result {
	t.Helper()
	l := &golang.Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"pkg/pkg.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res
}

// callSiteFiles marks pkg/pkg.go's line 18 (Caller's call to Helper) as
// inside a changed hunk, and leaves cmd/app/main.go out of the diff entirely
// -- exactly what the "callers" and "a target outside the diff" assertions
// below need.
func callSiteFiles() []model.ChangedFile {
	return []model.ChangedFile{{
		NewPath: "pkg/pkg.go",
		Status:  model.FileModified,
		Hunks:   []model.DiffHunk{{NewStart: 18, NewLines: 1}},
	}}
}

func TestResolveGo_Callees(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	rep, err := ResolveGo(context.Background(), res, callSiteFiles(), "pkg/pkg.go", nil, 17, true)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep == nil {
		t.Fatal("ResolveGo returned nil, want a report for Caller")
	}
	if rep.Symbol.Name != "Caller" {
		t.Errorf("Symbol.Name = %q, want Caller", rep.Symbol.Name)
	}
	if rep.Tier != lang.TierExact {
		t.Errorf("Tier = %q, want %q", rep.Tier, lang.TierExact)
	}
	if len(rep.Callees) != 1 {
		t.Fatalf("Callees = %+v, want exactly one (Helper)", rep.Callees)
	}
	c := rep.Callees[0]
	if c.Name != "Helper" || c.Path != "pkg/pkg.go" || c.Line != 13 {
		t.Errorf("Callees[0] = %+v, want Helper at pkg/pkg.go:13", c)
	}
	if c.Confidence != model.ConfStatic {
		t.Errorf("Callees[0].Confidence = %q, want static", c.Confidence)
	}
}

func TestResolveGo_Callers(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	// Helper (line 13) is called once, from inside Caller's body at line 18
	// -- which callSiteFiles() marks as changed, so this caller should come
	// back InDiff: true.
	rep, err := ResolveGo(context.Background(), res, callSiteFiles(), "pkg/pkg.go", nil, 13, true)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep == nil || rep.Symbol.Name != "Helper" {
		t.Fatalf("ResolveGo = %+v, want a report for Helper", rep)
	}
	if len(rep.Callers) != 1 {
		t.Fatalf("Callers = %+v, want exactly one (Caller)", rep.Callers)
	}
	caller := rep.Callers[0]
	if caller.Name != "Caller" || caller.Path != "pkg/pkg.go" || caller.Line != 18 {
		t.Errorf("Callers[0] = %+v, want Caller's call site at pkg/pkg.go:18", caller)
	}
	if !caller.InDiff {
		t.Error("Callers[0].InDiff = false, want true (its call site is the changed line)")
	}
}

// TestResolveGo_Callers_TargetOutsideDiff resolves Caller's own callers:
// main(), in cmd/app/main.go, a file callSiteFiles() never mentions -- the
// "a target outside the diff" case the verification plan calls for.
func TestResolveGo_Callers_TargetOutsideDiff(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	rep, err := ResolveGo(context.Background(), res, callSiteFiles(), "pkg/pkg.go", nil, 17, true)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep == nil || rep.Symbol.Name != "Caller" {
		t.Fatalf("ResolveGo = %+v, want a report for Caller", rep)
	}
	if len(rep.Callers) != 1 {
		t.Fatalf("Callers = %+v, want exactly one (main)", rep.Callers)
	}
	main := rep.Callers[0]
	if main.Name != "main" || main.Path != "cmd/app/main.go" || main.Line != 6 {
		t.Errorf("Callers[0] = %+v, want main's call site at cmd/app/main.go:6", main)
	}
	if main.InDiff {
		t.Error("Callers[0].InDiff = true, want false: cmd/app/main.go is not part of the diff")
	}
}

func TestResolveGo_Implementations(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	// English.Greet is at line 9.
	rep, err := ResolveGo(context.Background(), res, nil, "pkg/pkg.go", nil, 9, true)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep == nil || rep.Symbol.Name != "Greet" || rep.Symbol.Receiver != "English" {
		t.Fatalf("ResolveGo = %+v, want a report for English.Greet", rep)
	}
	if len(rep.Implementations) != 1 {
		t.Fatalf("Implementations = %+v, want exactly one (Greeter.Greet)", rep.Implementations)
	}
	impl := rep.Implementations[0]
	// An interface's own method has no CodeSymbol of its own (see
	// internal/core/radius's identical gap), so this never resolves to a
	// path -- the name still comes through via fallbackName.
	if impl.Name != "Greeter.Greet" {
		t.Errorf("Implementations[0].Name = %q, want Greeter.Greet", impl.Name)
	}
	if impl.Path != "" {
		t.Errorf("Implementations[0].Path = %q, want unresolved (empty)", impl.Path)
	}
	if impl.Confidence != model.ConfStatic {
		t.Errorf("Implementations[0].Confidence = %q, want static", impl.Confidence)
	}
}

func TestResolveGo_OldSide_SyntaxOnlyNoEdges(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	// exact=false (the old-side case): even with a real *Result in hand, no
	// edges should come back -- only the syntax-only declaration.
	rep, err := ResolveGo(context.Background(), res, nil, "pkg/pkg.go", []byte(pkgGoSrc), 17, false)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep == nil || rep.Symbol.Name != "Caller" {
		t.Fatalf("ResolveGo = %+v, want a syntax-only report for Caller", rep)
	}
	if rep.Tier != lang.TierExact {
		t.Errorf("Tier = %q, want %q even for the syntax-only fallback", rep.Tier, lang.TierExact)
	}
	if len(rep.Callees) != 0 {
		t.Errorf("Callees = %+v, want none on the old side", rep.Callees)
	}
}

func TestResolveGo_NotAFunctionLine(t *testing.T) {
	dir := writeGoFixture(t)
	res := loadFixture(t, dir)

	// Line 3 is the Greeter interface declaration, not a func/method.
	rep, err := ResolveGo(context.Background(), res, nil, "pkg/pkg.go", nil, 3, true)
	if err != nil {
		t.Fatalf("ResolveGo: %v", err)
	}
	if rep != nil {
		t.Errorf("ResolveGo = %+v, want nil for a non-func/method line", rep)
	}
}

// --- structural tier (tree-sitter) -----------------------------------

const utilJS = `function helper() {
  return 1;
}

module.exports = { helper };
`

const mainJS = `const { helper } = require('./util');

function run() {
  return helper() + 1;
}
`

func writeJSFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("scripts/util.js", utilJS)
	write("scripts/main.js", mainJS)
	return dir
}

// TestResolveStructural_Callees exercises FR-39's heuristic tier end to end:
// run() (scripts/main.js) calls helper() (scripts/util.js), a different file
// entirely -- the "a non-Go file via tree-sitter" case the verification plan
// calls for.
func TestResolveStructural_Callees(t *testing.T) {
	dir := writeJSFixture(t)
	p := treesitter.New(dir)

	rep, err := ResolveStructural(context.Background(), p, nil, "scripts/main.js", []byte(mainJS), 3)
	if err != nil {
		t.Fatalf("ResolveStructural: %v", err)
	}
	if rep == nil || rep.Symbol.Name != "run" {
		t.Fatalf("ResolveStructural = %+v, want a report for run", rep)
	}
	if rep.Tier != lang.TierStructural {
		t.Errorf("Tier = %q, want %q", rep.Tier, lang.TierStructural)
	}
	if len(rep.Callees) != 1 {
		t.Fatalf("Callees = %+v, want exactly one (helper)", rep.Callees)
	}
	c := rep.Callees[0]
	if c.Name != "helper" || c.Path != "scripts/util.js" || c.Line != 1 {
		t.Errorf("Callees[0] = %+v, want helper at scripts/util.js:1", c)
	}
	if c.Confidence != model.ConfHeuristic {
		t.Errorf("Callees[0].Confidence = %q, want heuristic", c.Confidence)
	}
	if len(rep.Implementations) != 0 {
		t.Errorf("Implementations = %+v, want none: the structural tier has no type information", rep.Implementations)
	}
}
