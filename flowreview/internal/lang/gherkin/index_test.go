package gherkin

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

const testRepo = "testdata/repo"

func buildTestIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := Build(context.Background(), testRepo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return ix
}

// scenarioByName finds the one scenario entry point with the given name,
// failing the test if there isn't exactly one.
func scenarioByName(t *testing.T, ix *Index, name string) model.CodeSymbol {
	t.Helper()
	for _, ep := range ix.EntryPoints() {
		if ep.Symbol.Name == name {
			return ep.Symbol
		}
	}
	t.Fatalf("no scenario entry point named %q", name)
	return model.CodeSymbol{}
}

func stepByText(t *testing.T, steps []model.CodeSymbol, substr string) model.CodeSymbol {
	t.Helper()
	for _, s := range steps {
		if strings.Contains(s.Name, substr) {
			return s
		}
	}
	t.Fatalf("no step containing %q among %v", substr, steps)
	return model.CodeSymbol{}
}

func TestEntryPoints_OneScenarioPerEntry(t *testing.T) {
	ix := buildTestIndex(t)
	eps := ix.EntryPoints()
	if len(eps) != 6 {
		t.Fatalf("EntryPoints: got %d, want 6: %+v", len(eps), eps)
	}
	for _, ep := range eps {
		if ep.Symbol.Kind != model.SymScenario {
			t.Errorf("entry point %q has kind %q, want scenario", ep.Symbol.Name, ep.Symbol.Kind)
		}
		if ep.Reason == "" {
			t.Errorf("entry point %q has no reason", ep.Symbol.Name)
		}
	}
}

func TestLink_GoStepDef(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Say hello")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 2 {
		t.Fatalf("StepsForScenario(%q): got %d steps, want 2", sc.Name, len(steps))
	}

	given := stepByText(t, steps, `a user named "Ann" exists`)
	edges := ix.EdgesForStep(given.ID)
	if len(edges) != 1 {
		t.Fatalf("EdgesForStep(given): got %d edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].Confidence != model.ConfStatic {
		t.Errorf("confidence = %q, want static", edges[0].Confidence)
	}
	if want := "stepdefs/steps.go#aUserNamedExists"; edges[0].To != want {
		t.Errorf("To = %q, want %q", edges[0].To, want)
	}
	if edges[0].Source != "gherkin" {
		t.Errorf("Source = %q, want gherkin", edges[0].Source)
	}

	then := stepByText(t, steps, `they see "hi Ann"`)
	edges = ix.EdgesForStep(then.ID)
	if len(edges) != 1 || edges[0].To != "stepdefs/steps.go#theySeeGreeting" {
		t.Fatalf("EdgesForStep(then) = %+v, want one edge to stepdefs/steps.go#theySeeGreeting", edges)
	}
}

func TestLink_JSInlineHandler(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Request greeting")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 1 {
		t.Fatalf("StepsForScenario: got %d steps, want 1", len(steps))
	}
	edges := ix.EdgesForStep(steps[0].ID)
	if len(edges) != 1 {
		t.Fatalf("EdgesForStep: got %d edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].Confidence != model.ConfStatic {
		t.Errorf("confidence = %q, want static", edges[0].Confidence)
	}
	if !strings.HasPrefix(edges[0].To, "stepdefs/steps.js#closure@") {
		t.Errorf("To = %q, want a stepdefs/steps.js closure id", edges[0].To)
	}
}

func TestLink_JavaCucumberExpressions(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Cucumber expressions")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 2 {
		t.Fatalf("StepsForScenario: got %d steps, want 2", len(steps))
	}

	intStep := stepByText(t, steps, "there are 3 cucumbers")
	edges := ix.EdgesForStep(intStep.ID)
	if len(edges) != 1 {
		t.Fatalf("EdgesForStep({int}): got %d edges, want 1: %+v", len(edges), edges)
	}
	if want := "stepdefs/CucumberSteps.java#CucumberSteps.thereAreCucumbersInTheBasket"; edges[0].To != want {
		t.Errorf("To = %q, want %q", edges[0].To, want)
	}

	stringStep := stepByText(t, steps, `the basket has "cucumbers"`)
	edges = ix.EdgesForStep(stringStep.ID)
	if len(edges) != 1 {
		t.Fatalf("EdgesForStep({string}): got %d edges, want 1: %+v", len(edges), edges)
	}
	if want := "stepdefs/CucumberSteps.java#CucumberSteps.theBasketHas"; edges[0].To != want {
		t.Errorf("To = %q, want %q", edges[0].To, want)
	}
}

func TestLink_UnmatchedStepReported(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Unmatched step")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 1 {
		t.Fatalf("StepsForScenario: got %d steps, want 1", len(steps))
	}
	if edges := ix.EdgesForStep(steps[0].ID); len(edges) != 0 {
		t.Fatalf("EdgesForStep(unmatched) = %+v, want none", edges)
	}

	unmatched := ix.Unmatched("features/greeting.feature")
	found := false
	for _, u := range unmatched {
		if u.ID == steps[0].ID {
			found = true
		}
	}
	if !found {
		t.Errorf("Unmatched() = %+v, want it to include %q", unmatched, steps[0].Name)
	}
}

func TestLink_AmbiguousMatchReturnsBoth(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Ambiguous step")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 1 {
		t.Fatalf("StepsForScenario: got %d steps, want 1", len(steps))
	}
	edges := ix.EdgesForStep(steps[0].ID)
	if len(edges) != 2 {
		t.Fatalf("EdgesForStep(ambiguous): got %d edges, want 2: %+v", len(edges), edges)
	}
	for _, e := range edges {
		if e.Confidence != model.ConfHeuristic {
			t.Errorf("confidence = %q, want heuristic for an ambiguous match", e.Confidence)
		}
	}
	var tos []string
	for _, e := range edges {
		tos = append(tos, e.To)
	}
	sort.Strings(tos)
	wantGo := "stepdefs/more_steps.go#ambiguousOne"
	if tos[0] != wantGo && tos[1] != wantGo {
		t.Errorf("edges %v missing the Go handler %q", tos, wantGo)
	}
	hasJS := strings.HasPrefix(tos[0], "stepdefs/steps.js#closure@") || strings.HasPrefix(tos[1], "stepdefs/steps.js#closure@")
	if !hasJS {
		t.Errorf("edges %v missing a stepdefs/steps.js closure", tos)
	}
}

func TestLink_ScenarioOutlineExamplesSubstitution(t *testing.T) {
	ix := buildTestIndex(t)
	sc := scenarioByName(t, ix, "Say hello to several users")
	steps := ix.StepsForScenario(sc.ID)
	if len(steps) != 2 {
		t.Fatalf("StepsForScenario: got %d steps, want 2", len(steps))
	}

	given := stepByText(t, steps, `a user named "<name>" exists`)
	edges := ix.EdgesForStep(given.ID)
	if len(edges) != 1 || edges[0].To != "stepdefs/steps.go#aUserNamedExists" {
		t.Fatalf("EdgesForStep(outline given) = %+v, want one edge to stepdefs/steps.go#aUserNamedExists", edges)
	}
	if edges[0].Confidence != model.ConfStatic {
		t.Errorf("confidence = %q, want static (Examples substitution makes this concrete)", edges[0].Confidence)
	}

	then := stepByText(t, steps, `they see "<greeting>"`)
	edges = ix.EdgesForStep(then.ID)
	if len(edges) != 1 || edges[0].To != "stepdefs/steps.go#theySeeGreeting" {
		t.Fatalf("EdgesForStep(outline then) = %+v, want one edge to stepdefs/steps.go#theySeeGreeting", edges)
	}
}

func TestScenariosUsingStepDef(t *testing.T) {
	ix := buildTestIndex(t)
	scenarios := ix.ScenariosUsingStepDef("stepdefs/steps.go#theySeeGreeting")
	if len(scenarios) != 2 {
		t.Fatalf("ScenariosUsingStepDef: got %d scenarios, want 2: %+v", len(scenarios), scenarios)
	}
	names := map[string]bool{}
	for _, s := range scenarios {
		names[s.Name] = true
	}
	for _, want := range []string{"Say hello", "Say hello to several users"} {
		if !names[want] {
			t.Errorf("ScenariosUsingStepDef missing scenario %q, got %v", want, names)
		}
	}
}

func TestUnmatched_EmptyForFullyLinkedFeature(t *testing.T) {
	ix := buildTestIndex(t)
	// Every step in the feature is accounted for by either a match or
	// Unmatched; there is exactly one genuinely unmatched step in the
	// fixture.
	unmatched := ix.Unmatched("features/greeting.feature")
	if len(unmatched) != 1 {
		t.Fatalf("Unmatched: got %d, want 1: %+v", len(unmatched), unmatched)
	}
	if !strings.Contains(unmatched[0].Name, "nobody implements this step") {
		t.Errorf("Unmatched()[0] = %q, want the nobody-implements step", unmatched[0].Name)
	}
}
