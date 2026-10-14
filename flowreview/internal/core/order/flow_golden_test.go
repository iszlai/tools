package order

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

var updateGolden = flag.Bool("update", false, "regenerate testdata/golden/* from Flow/Text")

// flowFixtureDir is testdata/flow, a self-contained Go module (its own
// go.mod) with a handler -> service -> repo call chain, an unrelated util,
// and a Gherkin scenario reaching into the service package through a step
// definition -- see testdata/flow/README.md.
func flowFixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "flow"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// wholeFileChange builds a model.ChangedFile for dir/path whose single hunk
// covers the file's whole current line count, simulating "this whole file
// is new content" -- enough for ChangedSymbols to find every declaration in
// it, which is all the golden fixture needs. Counting lines from disk
// rather than hardcoding them keeps the fixture honest when its source
// files are edited.
func wholeFileChange(t *testing.T, dir, id, path, language string) model.ChangedFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Count(data, []byte("\n"))
	return model.ChangedFile{
		ID:       id,
		OldPath:  path,
		NewPath:  path,
		Status:   model.FileModified,
		Language: language,
		Hunks: []model.DiffHunk{
			{OldStart: 1, OldLines: lines, NewStart: 1, NewLines: lines},
		},
	}
}

func loadFlowFixture(t *testing.T) (*golang.Result, *gherkin.Index) {
	t.Helper()
	dir := flowFixtureDir(t)

	l := &golang.Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{
		"service/service.go",
		"service/validate.go",
		"repo/repo.go",
		"util/util.go",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ix, err := gherkin.Build(ctx, dir)
	if err != nil {
		t.Fatalf("gherkin.Build: %v", err)
	}
	return res, ix
}

func flowFixtureFiles(t *testing.T, dir string) []model.ChangedFile {
	t.Helper()
	return []model.ChangedFile{
		wholeFileChange(t, dir, "f-service", "service/service.go", "go"),
		wholeFileChange(t, dir, "f-validate", "service/validate.go", "go"),
		wholeFileChange(t, dir, "f-repo", "repo/repo.go", "go"),
		wholeFileChange(t, dir, "f-util", "util/util.go", "go"),
		wholeFileChange(t, dir, "f-readme", "README.md", "markdown"),
	}
}

// TestFlow_Golden pins Flow's output on the handler/service/repo/util/
// gherkin fixture: service and repo changed (reached from the unchanged
// handler entry point), util changed and isolated, README changed with no
// symbol, and service.Validate changed and reached only through the
// Gherkin scenario's step definition. Run with -update after a deliberate,
// reviewed change to Flow or Text.
func TestFlow_Golden(t *testing.T) {
	res, ix := loadFlowFixture(t)
	in := Inputs{Files: flowFixtureFiles(t, flowFixtureDir(t)), Go: res, Gherkin: ix}

	o := Flow(in)

	jsonGot, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	jsonGot = append(jsonGot, '\n')

	var textGot bytes.Buffer
	Text(o, &textGot)

	checkGolden(t, filepath.Join("testdata", "golden", "flow.json"), jsonGot)
	checkGolden(t, filepath.Join("testdata", "golden", "flow.txt"), textGot.Bytes())
}

func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `go test ./internal/core/order/ -run TestFlow_Golden -update`)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s does not match; run with -update if the change is deliberate\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
