package render

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
)

// update regenerates testdata/golden_go.rows.json from the current renderer.
// Run `go test ./internal/core/render/... -run TestGoldenGoDiff -update`
// after a deliberate rendering change; review the diff before committing it.
var update = flag.Bool("update", false, "update golden files")

// TestGoldenGoDiff renders a small, real Go diff (testdata/golden_go.diff: a
// one-line body edit inside Greet) and checks the result against a stored
// golden, pinning pairing, positions, chroma's Go lexer output and the
// intra-line diff together the way a hand-picked unit test cannot.
func TestGoldenGoDiff(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden_go.diff")
	if err != nil {
		t.Fatal(err)
	}
	files, err := diff.Parse(raw)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}

	rows := File(files[0])
	got, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	const goldenPath = "testdata/golden_go.rows.json"
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("rows changed; got:\n%s\nwant:\n%s\n(run with -update if this is expected)", got, want)
	}
}
