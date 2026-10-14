package order

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestFlow_NoAnalysis checks that Flow degrades to "everything unclassified"
// when neither Go nor Gherkin analysis is available -- the common case
// right after a session opens, before FR-13's background Load finishes.
func TestFlow_NoAnalysis(t *testing.T) {
	in := Inputs{Files: []model.ChangedFile{
		changedFile("1", "b.go"),
		changedFile("2", "a.go"),
	}}
	o := Flow(in)
	if o.Kind != KindFlow {
		t.Errorf("Kind = %q, want %q", o.Kind, KindFlow)
	}
	if len(o.Groups) != 0 {
		t.Fatalf("Groups = %+v, want none", o.Groups)
	}
	assertPaths(t, o.Unclassified, "a.go", "b.go")
}

// TestFlow_ChangedScenarioNoGo checks the board comment's "a changed
// scenario... makes the scenario an entry point" in isolation, with no Go
// analysis at all connecting it to anything: the .feature file should still
// get its own Group, titled by the scenario, rather than falling through to
// Unclassified.
func TestFlow_ChangedScenarioNoGo(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("testdata", "scenario-only"))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := gherkin.Build(context.Background(), dir)
	if err != nil {
		t.Fatalf("gherkin.Build: %v", err)
	}

	in := Inputs{
		Gherkin: ix,
		Files: []model.ChangedFile{{
			ID:      "f1",
			OldPath: "features/standalone.feature",
			NewPath: "features/standalone.feature",
			Status:  model.FileModified,
			Hunks: []model.DiffHunk{
				{OldStart: 3, OldLines: 2, NewStart: 3, NewLines: 2},
			},
		}},
	}

	o := Flow(in)
	if len(o.Unclassified) != 0 {
		t.Errorf("Unclassified = %+v, want none", o.Unclassified)
	}
	if len(o.Groups) != 1 {
		t.Fatalf("Groups = %+v, want exactly one", o.Groups)
	}
	g := o.Groups[0]
	if g.EntryPoint == nil || g.EntryPoint.Kind != model.SymScenario {
		t.Errorf("EntryPoint = %+v, want the scenario symbol", g.EntryPoint)
	}
	if len(g.Files) != 1 || g.Files[0].Path != "features/standalone.feature" {
		t.Errorf("Files = %+v, want just the feature file", g.Files)
	}
}

// TestTopoOrder_BreaksCyclesDeterministically checks that a cycle (A and B
// call each other) still produces a full, stable order rather than hanging
// or depending on map iteration.
func TestTopoOrder_BreaksCyclesDeterministically(t *testing.T) {
	outgoing := map[string][]string{
		"A": {"B"},
		"B": {"A", "C"},
	}
	ids := []string{"A", "B", "C"}
	for i := 0; i < 5; i++ {
		got := topoOrder(ids, outgoing)
		if len(got) != 3 {
			t.Fatalf("topoOrder = %v, want 3 elements", got)
		}
		want := []string{"A", "B", "C"}
		for i, id := range want {
			if got[i] != id {
				t.Fatalf("topoOrder = %v, want %v", got, want)
			}
		}
	}
}

// TestTopoOrder_CallersFirst checks the non-cyclic case: a linear chain
// orders root to leaf.
func TestTopoOrder_CallersFirst(t *testing.T) {
	outgoing := map[string][]string{
		"handler": {"service"},
		"service": {"repo"},
	}
	got := topoOrder([]string{"repo", "service", "handler"}, outgoing)
	want := []string{"handler", "service", "repo"}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("topoOrder = %v, want %v", got, want)
		}
	}
}

func TestConnectedComponents(t *testing.T) {
	// A-B connected (via outgoing A->B); C isolated.
	incoming := map[string][]string{"B": {"A"}}
	outgoing := map[string][]string{"A": {"B"}}
	comps := connectedComponents([]string{"A", "B", "C"}, incoming, outgoing)
	if len(comps) != 2 {
		t.Fatalf("components = %v, want 2", comps)
	}
	if len(comps[0]) != 2 || comps[0][0] != "A" || comps[0][1] != "B" {
		t.Errorf("comps[0] = %v, want [A B]", comps[0])
	}
	if len(comps[1]) != 1 || comps[1][0] != "C" {
		t.Errorf("comps[1] = %v, want [C]", comps[1])
	}
}
