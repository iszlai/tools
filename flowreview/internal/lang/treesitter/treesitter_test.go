package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return b
}

func TestSupportsAndCapabilities(t *testing.T) {
	p := New(".")
	caps := p.Capabilities()
	if caps.Tier != "structural" {
		t.Fatalf("Tier = %q, want structural", caps.Tier)
	}
	for _, want := range []string{"go", "java", "javascript", "typescript", "tsx", "python", "rust", "yaml", "gherkin"} {
		found := false
		for _, l := range caps.Languages {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Capabilities().Languages missing %q (got %v)", want, caps.Languages)
		}
		if !p.Supports("x." + extFor(want)) {
			t.Errorf("Supports() false for language %q", want)
		}
	}
	if p.Supports("x.unknownlang") {
		t.Error("Supports() true for an unknown extension")
	}
}

func extFor(language string) string {
	switch language {
	case "go":
		return "go"
	case "java":
		return "java"
	case "javascript":
		return "js"
	case "typescript":
		return "ts"
	case "tsx":
		return "tsx"
	case "python":
		return "py"
	case "rust":
		return "rs"
	case "yaml":
		return "yaml"
	case "gherkin":
		return "feature"
	}
	return language
}

func TestFunctionAt(t *testing.T) {
	cases := []struct {
		name         string
		path         string
		line         int
		wantKind     model.SymbolKind
		wantSymName  string
		wantReceiver string
		anonymous    bool
	}{
		{"go closure", "go/sample.go", 7, model.SymFunc, "", "", true},
		{"go method calling its closure", "go/sample.go", 9, model.SymMethod, "Greet", "Greeter", false},
		{"go function", "go/sample.go", 13, model.SymFunc, "Add", "", false},
		{"java method", "java/Sample.java", 11, model.SymMethod, "greet", "Greeter", false},
		{"js function", "javascript/sample.js", 2, model.SymFunc, "add", "", false},
		{"js method", "javascript/sample.js", 7, model.SymMethod, "increment", "Counter", false},
		{"ts class method", "typescript/sample.ts", 3, model.SymMethod, "greet", "Greeter", false},
		{"python def", "python/sample.py", 2, model.SymFunc, "add", "", false},
		{"python method", "python/sample.py", 7, model.SymMethod, "greet", "Greeter", false},
		{"rust fn", "rust/sample.rs", 2, model.SymFunc, "add", "", false},
		{"rust method", "rust/sample.rs", 11, model.SymMethod, "greet", "Greeter", false},
	}

	p := New(".")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := readFixture(t, tc.path)
			sym, err := p.FunctionAt(context.Background(), tc.path, src, tc.line)
			if err != nil {
				t.Fatalf("FunctionAt: %v", err)
			}
			if sym == nil {
				t.Fatalf("FunctionAt returned nil for line %d", tc.line)
			}
			if sym.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", sym.Kind, tc.wantKind)
			}
			if !tc.anonymous && sym.Name != tc.wantSymName {
				t.Errorf("Name = %q, want %q", sym.Name, tc.wantSymName)
			}
			if sym.Receiver != tc.wantReceiver {
				t.Errorf("Receiver = %q, want %q", sym.Receiver, tc.wantReceiver)
			}
			if sym.Range.StartLine > tc.line || sym.Range.EndLine < tc.line {
				t.Errorf("Range %v does not contain line %d", sym.Range, tc.line)
			}
		})
	}
}

func TestFunctionAtNoEnclosing(t *testing.T) {
	p := New(".")
	src := readFixture(t, "go/sample.go")
	sym, err := p.FunctionAt(context.Background(), "go/sample.go", src, 2) // blank line, top level
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym != nil {
		t.Fatalf("FunctionAt = %+v, want nil", sym)
	}
}

func TestOutline(t *testing.T) {
	p := New(".")

	t.Run("go", func(t *testing.T) {
		src := readFixture(t, "go/sample.go")
		out, err := p.Outline(context.Background(), "go/sample.go", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymClass, "Greeter", ""},
			{model.SymMethod, "Greet", "Greeter"},
			{model.SymFunc, "Add", ""},
		}
		assertOutline(t, out, want)
		if out[1].ID != "go/sample.go#Greeter.Greet" {
			t.Errorf("method ID = %q, want go/sample.go#Greeter.Greet", out[1].ID)
		}
		if !out[0].Exported || !out[2].Exported {
			t.Errorf("expected Greeter and Add to be Exported")
		}
	})

	t.Run("java", func(t *testing.T) {
		src := readFixture(t, "java/Sample.java")
		out, err := p.Outline(context.Background(), "java/Sample.java", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymClass, "Greeter", ""},
			{model.SymMethod, "Greeter", "Greeter"}, // constructor
			{model.SymMethod, "greet", "Greeter"},
		}
		assertOutline(t, out, want)
	})

	t.Run("javascript", func(t *testing.T) {
		src := readFixture(t, "javascript/sample.js")
		out, err := p.Outline(context.Background(), "javascript/sample.js", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymFunc, "add", ""},
			{model.SymClass, "Counter", ""},
			{model.SymMethod, "increment", "Counter"},
		}
		assertOutline(t, out, want)
	})

	t.Run("typescript", func(t *testing.T) {
		src := readFixture(t, "typescript/sample.ts")
		out, err := p.Outline(context.Background(), "typescript/sample.ts", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymClass, "Greeter", ""},
			{model.SymMethod, "greet", "Greeter"},
		}
		assertOutline(t, out, want)
	})

	t.Run("python", func(t *testing.T) {
		src := readFixture(t, "python/sample.py")
		out, err := p.Outline(context.Background(), "python/sample.py", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymFunc, "add", ""},
			{model.SymClass, "Greeter", ""},
			{model.SymMethod, "greet", "Greeter"},
		}
		assertOutline(t, out, want)
	})

	t.Run("rust", func(t *testing.T) {
		src := readFixture(t, "rust/sample.rs")
		out, err := p.Outline(context.Background(), "rust/sample.rs", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := []struct {
			kind     model.SymbolKind
			name     string
			receiver string
		}{
			{model.SymFunc, "add", ""},
			{model.SymClass, "Greeter", ""},
			{model.SymMethod, "greet", "Greeter"},
		}
		assertOutline(t, out, want)
	})

	t.Run("gherkin", func(t *testing.T) {
		src := readFixture(t, "gherkin/sample.feature")
		out, err := p.Outline(context.Background(), "gherkin/sample.feature", src)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if len(out) != 10 {
			t.Fatalf("len(out) = %d, want 10: %+v", len(out), out)
		}
		feature := out[0]
		if feature.Kind != model.SymFeature || feature.Name != "Greeting" || feature.Range.StartLine != 1 || feature.Range.EndLine != 17 {
			t.Errorf("Feature = %+v, want Kind=feature Name=Greeting Range=1-17", feature)
		}
		var scenarios, outlines, steps, backgrounds int
		for _, s := range out {
			switch s.Kind {
			case model.SymScenario:
				scenarios++
			case model.SymStep:
				steps++
				if s.Range.StartLine != s.Range.EndLine {
					t.Errorf("step %q range %v is not single-line", s.Name, s.Range)
				}
			case model.SymBackground:
				backgrounds++
			}
			if s.Name == "Say hello to several users" {
				outlines++
				if s.Range.StartLine != 11 || s.Range.EndLine != 17 {
					t.Errorf("Scenario Outline range = %v, want 11-17", s.Range)
				}
			}
		}
		if scenarios != 2 {
			t.Errorf("scenarios = %d, want 2", scenarios)
		}
		if outlines != 1 {
			t.Errorf("scenario outlines = %d, want 1", outlines)
		}
		if backgrounds != 1 {
			t.Errorf("backgrounds = %d, want 1", backgrounds)
		}
		if steps != 6 {
			t.Errorf("steps = %d, want 6", steps)
		}
	})
}

func assertOutline(t *testing.T, got []model.CodeSymbol, want []struct {
	kind     model.SymbolKind
	name     string
	receiver string
}) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Name != w.name || got[i].Receiver != w.receiver {
			t.Errorf("entry %d = {Kind:%s Name:%s Receiver:%s}, want {Kind:%s Name:%s Receiver:%s}",
				i, got[i].Kind, got[i].Name, got[i].Receiver, w.kind, w.name, w.receiver)
		}
	}
}

func TestCallersAndCallees(t *testing.T) {
	repoRoot := filepath.Join("testdata", "multifile")
	p := New(repoRoot)
	ctx := context.Background()

	aSrc, err := os.ReadFile(filepath.Join(repoRoot, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	bSrc, err := os.ReadFile(filepath.Join(repoRoot, "b.go"))
	if err != nil {
		t.Fatal(err)
	}

	aOutline, err := p.Outline(ctx, "a.go", aSrc)
	if err != nil {
		t.Fatalf("Outline(a.go): %v", err)
	}
	bOutline, err := p.Outline(ctx, "b.go", bSrc)
	if err != nil {
		t.Fatalf("Outline(b.go): %v", err)
	}
	if len(aOutline) != 1 || aOutline[0].Name != "Helper" {
		t.Fatalf("a.go outline = %+v, want one symbol named Helper", aOutline)
	}
	if len(bOutline) != 1 || bOutline[0].Name != "UseHelper" {
		t.Fatalf("b.go outline = %+v, want one symbol named UseHelper", bOutline)
	}
	helper, useHelper := aOutline[0], bOutline[0]

	t.Run("Callees finds Helper from UseHelper", func(t *testing.T) {
		edges, err := p.Callees(ctx, useHelper)
		if err != nil {
			t.Fatalf("Callees: %v", err)
		}
		if !hasEdgeTo(edges, helper.ID) {
			t.Errorf("Callees(%s) = %+v, want an edge to %s", useHelper.ID, edges, helper.ID)
		}
		for _, e := range edges {
			if e.Confidence != model.ConfHeuristic || e.Source != "name-search" {
				t.Errorf("edge %+v: want Confidence=heuristic Source=name-search", e)
			}
		}
	})

	t.Run("Callers finds UseHelper from Helper", func(t *testing.T) {
		edges, err := p.Callers(ctx, helper)
		if err != nil {
			t.Fatalf("Callers: %v", err)
		}
		if !hasEdgeFrom(edges, useHelper.ID) {
			t.Errorf("Callers(%s) = %+v, want an edge from %s", helper.ID, edges, useHelper.ID)
		}
	})
}

func hasEdgeTo(edges []model.NavigationEdge, to string) bool {
	for _, e := range edges {
		if e.To == to {
			return true
		}
	}
	return false
}

func hasEdgeFrom(edges []model.NavigationEdge, from string) bool {
	for _, e := range edges {
		if e.From == from {
			return true
		}
	}
	return false
}

// TestConcurrentUse drives one Provider from many goroutines across several
// languages at once, matching the "parsers aren't thread-safe; make the
// provider safe for concurrent use" requirement. Run with -race.
func TestConcurrentUse(t *testing.T) {
	p := New(".")
	files := []string{"go/sample.go", "java/Sample.java", "javascript/sample.js", "python/sample.py", "rust/sample.rs"}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		path := files[i%len(files)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, err := os.ReadFile(filepath.Join("testdata", path))
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Outline(context.Background(), path, src); err != nil {
				t.Error(err)
			}
			if _, err := p.FunctionAt(context.Background(), path, src, 2); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestImplementationsUnsupported(t *testing.T) {
	p := New(".")
	_, err := p.Implementations(context.Background(), model.CodeSymbol{})
	if err == nil {
		t.Fatal("Implementations: want an error")
	}
}
