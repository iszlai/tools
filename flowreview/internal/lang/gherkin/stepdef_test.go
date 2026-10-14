package gherkin

import (
	"context"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestDiscoverGoStepDefs_NamedHandlerResolved(t *testing.T) {
	src := []byte("package stepdefs\n\nfunc register(ctx *Ctx) {\n\tctx.Step(`^hi$`, sayHi)\n}\n")
	byName := map[string][]model.CodeSymbol{
		"sayHi": {{ID: "pkg/file.go#sayHi", Name: "sayHi"}},
	}
	defs := discoverGoStepDefs("pkg/file.go", src, byName)
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1: %+v", len(defs), defs)
	}
	if defs[0].HandlerID != "pkg/file.go#sayHi" {
		t.Errorf("HandlerID = %q, want pkg/file.go#sayHi", defs[0].HandlerID)
	}
	if !defs[0].Pattern.MatchString("hi") {
		t.Errorf("pattern %q should match %q", defs[0].Pattern, "hi")
	}
}

func TestDiscoverGoStepDefs_DottedHandlerNotResolved(t *testing.T) {
	// A method value on a variable (suite.Handler) needs type information
	// this package doesn't have; it's left unresolved rather than guessed.
	src := []byte("func register(ctx *Ctx) {\n\tctx.Step(`^hi$`, suite.Handler)\n}\n")
	defs := discoverGoStepDefs("pkg/file.go", src, map[string][]model.CodeSymbol{})
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1: %+v", len(defs), defs)
	}
	if defs[0].HandlerID != "" {
		t.Errorf("HandlerID = %q, want empty (dotted handler is not resolved)", defs[0].HandlerID)
	}
	if defs[0].ID == "" {
		t.Error("StepDef.ID should still be set even when the handler is unresolved")
	}
}

func TestDiscoverGoStepDefs_AmbiguousNameNotResolved(t *testing.T) {
	src := []byte("func register(ctx *Ctx) {\n\tctx.Step(`^hi$`, sayHi)\n}\n")
	byName := map[string][]model.CodeSymbol{
		"sayHi": {
			{ID: "pkg/a.go#sayHi", Name: "sayHi"},
			{ID: "pkg/b.go#sayHi", Name: "sayHi"},
		},
	}
	defs := discoverGoStepDefs("pkg/file.go", src, byName)
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].HandlerID != "" {
		t.Errorf("HandlerID = %q, want empty when the name is ambiguous repo-wide", defs[0].HandlerID)
	}
}

func TestDiscoverJSStepDefs_NamedHandlerResolved(t *testing.T) {
	src := []byte("Given('a word step {word}', myHandler);\n")
	byName := map[string][]model.CodeSymbol{
		"myHandler": {{ID: "foo.js#myHandler", Name: "myHandler"}},
	}
	// tsProv is never dereferenced for a named-identifier handler; only the
	// inline-closure path needs it.
	defs := discoverJSStepDefs(context.Background(), nil, "foo.js", src, byName)
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1: %+v", len(defs), defs)
	}
	if defs[0].HandlerID != "foo.js#myHandler" {
		t.Errorf("HandlerID = %q, want foo.js#myHandler", defs[0].HandlerID)
	}
	if !defs[0].Pattern.MatchString("a word step hello") {
		t.Errorf("pattern %q should match the word step", defs[0].Pattern)
	}
}

func TestDiscoverJavaStepDefs_ClassicRegexStyle(t *testing.T) {
	src := []byte("public class S {\n" +
		"    @Given(\"^a user named \\\"([^\\\"]*)\\\" exists$\")\n" +
		"    public void given(String name) {\n" +
		"    }\n" +
		"}\n")
	tsProv := treesitter.New(".")
	defs := discoverJavaStepDefs(context.Background(), tsProv, "S.java", src)
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1: %+v", len(defs), defs)
	}
	if !defs[0].Pattern.MatchString(`a user named "Ann" exists`) {
		t.Errorf("pattern %q should match the classic-regex step", defs[0].Pattern)
	}
	if want := "S.java#S.given"; defs[0].HandlerID != want {
		t.Errorf("HandlerID = %q, want %q", defs[0].HandlerID, want)
	}
}
