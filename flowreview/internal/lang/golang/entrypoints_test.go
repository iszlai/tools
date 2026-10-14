package golang

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func entrypointsDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "entrypoints"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// allGoFiles lists every .go file under root, repo-relative and
// slash-separated — enough changed files that Loader.Load's reverse-importer
// expansion resolves to every package in the fixture module, regardless of
// which package imports which.
func allGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func loadEntrypointsFixture(t *testing.T) (*Result, string) {
	t.Helper()
	dir := entrypointsDir(t)
	l := &Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, allGoFiles(t, dir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res, dir
}

func entryPointsFor(eps []EntryPoint, id string) []EntryPoint {
	var out []EntryPoint
	for _, e := range eps {
		if e.Symbol.ID == id {
			out = append(out, e)
		}
	}
	return out
}

func hasKind(eps []EntryPoint, kind string) bool {
	for _, e := range eps {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func hasReasonContaining(eps []EntryPoint, substr string) bool {
	for _, e := range eps {
		if strings.Contains(e.Reason, substr) {
			return true
		}
	}
	return false
}

func kindsOf(eps []EntryPoint) []string {
	out := make([]string, len(eps))
	for i, e := range eps {
		out[i] = e.Kind
	}
	return out
}

// TestEntryPoints_Fixture covers every built-in heuristic (everything except
// the custom-pattern and HasCallers heuristics, which need their own Config
// — see TestEntryPoints_CustomConfig and TestEntryPoints_Unreferenced) against
// one fixture module load, and the "every entry point has a Reason" accept
// criterion.
func TestEntryPoints_Fixture(t *testing.T) {
	res, _ := loadEntrypointsFixture(t)
	eps := EntryPoints(res, Config{})
	if len(eps) == 0 {
		t.Fatal("EntryPoints returned nothing for a fixture full of entry points")
	}

	for _, e := range eps {
		if e.Reason == "" {
			t.Errorf("entry point %s (%s) has an empty Reason", e.Symbol.ID, e.Kind)
		}
	}

	const (
		mainPkg   = "fixture.example/entrypoints/cmd"
		httpPkg   = "fixture.example/entrypoints/httphandlers"
		webappPkg = "fixture.example/entrypoints/webapp"
		routesPkg = "fixture.example/entrypoints/routes"
		cliPkg    = "fixture.example/entrypoints/cli"
		grpcPkg   = "fixture.example/entrypoints/grpcservice"
		testPkg   = "fixture.example/entrypoints/testkinds"
	)

	cases := []struct {
		name string
		id   string
		kind string
	}{
		{"main", mainPkg + ".main", KindMain},
		{"init", mainPkg + ".init", KindInit},
		{"plain http handler", httpPkg + ".Plain", KindHTTPHandler},
		{"http.Handler impl", httpPkg + ".(handlerType).ServeHTTP", KindHTTPHandler},
		{"gin handler", webappPkg + ".GinHandler", KindHTTPHandler},
		{"echo handler", webappPkg + ".EchoHandler", KindHTTPHandler},
		{"fiber handler", webappPkg + ".FiberHandler", KindHTTPHandler},
		{"chi handler", routesPkg + ".ChiHandler", KindHTTPHandler},
		{"cobra RunE", cliPkg + ".runServe", KindCobraCommand},
		{"grpc direct impl", grpcPkg + ".(server).SayHello", KindGRPCMethod},
		{"grpc embed strong hint", grpcPkg + ".(partialServer).SayHello", KindGRPCMethod},
		{"TestDouble", testPkg + ".TestDouble", KindTest},
		{"BenchmarkDouble", testPkg + ".BenchmarkDouble", KindTest},
		{"ExampleDouble", testPkg + ".ExampleDouble", KindTest},
		{"FuzzDouble", testPkg + ".FuzzDouble", KindTest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found := entryPointsFor(eps, c.id)
			if len(found) == 0 {
				t.Fatalf("no entry point for %s", c.id)
			}
			if !hasKind(found, c.kind) {
				t.Errorf("%s: got kinds %v, want %q among them", c.id, kindsOf(found), c.kind)
			}
		})
	}

	// ViaHandleFunc/ViaHandle/ChiHandler also match the plain net/http
	// signature heuristic (checked above via their respective packages'
	// Plain-equivalents); here, specifically confirm the registration-based
	// heuristic fired too.
	if !hasReasonContaining(entryPointsFor(eps, httpPkg+".ViaHandleFunc"), "http.HandleFunc") {
		t.Error("ViaHandleFunc: no entry point registered via http.HandleFunc")
	}
	if !hasReasonContaining(entryPointsFor(eps, httpPkg+".ViaHandle"), "http.Handle") {
		t.Error("ViaHandle: no entry point registered via http.Handle")
	}
	if !hasReasonContaining(entryPointsFor(eps, routesPkg+".ChiHandler"), "chi") {
		t.Error("ChiHandler: no entry point registered via chi")
	}
	if !hasReasonContaining(entryPointsFor(eps, cliPkg+".runServe"), `"serve"`) {
		t.Error(`runServe: reason does not name the command "serve"`)
	}
}

// TestEntryPoints_CustomConfig covers .flowreview.yaml's entrypoints.patterns
// — both the func_regex and the signature form.
func TestEntryPoints_CustomConfig(t *testing.T) {
	res, dir := loadEntrypointsFixture(t)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.EntryPoints.Patterns) != 2 {
		t.Fatalf("LoadConfig: got %d patterns, want 2", len(cfg.EntryPoints.Patterns))
	}

	eps := EntryPoints(res, cfg)
	const pkg = "fixture.example/entrypoints/eventpkg"

	onEvent := entryPointsFor(eps, pkg+".OnEvent")
	if !hasKind(onEvent, KindCustom) || !hasReasonContaining(onEvent, "custom event name pattern") {
		t.Errorf("OnEvent: got %+v, want a custom entry point with the configured reason", onEvent)
	}

	handleEvent := entryPointsFor(eps, pkg+".HandleEvent")
	if !hasKind(handleEvent, KindCustom) || !hasReasonContaining(handleEvent, "event handler") {
		t.Errorf("HandleEvent: got %+v, want a custom entry point with the configured reason", handleEvent)
	}
}

// TestLoadConfig_MissingFile covers LoadConfig's "no .flowreview.yaml" case.
func TestLoadConfig_MissingFile(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.EntryPoints.Patterns) != 0 {
		t.Errorf("got %d patterns from a directory with no .flowreview.yaml, want 0", len(cfg.EntryPoints.Patterns))
	}
}

// TestEntryPoints_Unreferenced covers the optional HasCallers heuristic: off
// by default, and exported-only when wired up.
func TestEntryPoints_Unreferenced(t *testing.T) {
	dir := entrypointsDir(t)
	l := &Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"unreferenced/unreferenced.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const exportedID = "fixture.example/entrypoints/unreferenced.Exported"
	const unexportedID = "fixture.example/entrypoints/unreferenced.unexported"

	withoutHasCallers := EntryPoints(res, Config{})
	if len(entryPointsFor(withoutHasCallers, exportedID)) != 0 {
		t.Error("Exported flagged unreferenced with cfg.HasCallers == nil; the heuristic must be opt-in")
	}

	cfg := Config{HasCallers: func(string) bool { return false }}
	eps := EntryPoints(res, cfg)
	if !hasKind(entryPointsFor(eps, exportedID), KindUnreferenced) {
		t.Errorf("Exported: got %+v, want an unreferenced entry point", entryPointsFor(eps, exportedID))
	}
	if len(entryPointsFor(eps, unexportedID)) != 0 {
		t.Error("unexported must never be flagged unreferenced, even with HasCallers set")
	}
}
