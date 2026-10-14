package golang

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate testdata/callgraph/golden/*.txt from ContextText")

// TestContextText_Golden pins ContextText's plain-text output for one
// representative symbol from each fixture package: a static call with a
// callee and no callers, a static call with callers and no callees, an
// interface-dispatch call site, an interface type's implementations, a
// closure's enclosing function, and a generic function's origin. Run with
// -update after a deliberate, reviewed change to ContextText's format.
func TestContextText_Golden(t *testing.T) {
	res := loadCallgraphFixture(t)

	symbols := []string{
		modPath + "/direct.Top",
		modPath + "/direct.Leaf",
		modPath + "/iface.Announce",
		modPath + "/iface.Speaker",
		modPath + "/closure.Helper",
		modPath + "/generic.Max",
	}

	for _, id := range symbols {
		t.Run(id, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ContextText(res, id, &buf); err != nil {
				t.Fatalf("ContextText(%s): %v", id, err)
			}
			goldenPath := filepath.Join("testdata", "callgraph", "golden", goldenFileName(id))

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read %s: %v (run `go test ./internal/lang/golang/ -run TestContextText_Golden -update`)", goldenPath, err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("ContextText(%s) does not match %s; run with -update if the change is deliberate\n--- got ---\n%s\n--- want ---\n%s",
					id, goldenPath, buf.String(), want)
			}
		})
	}
}

// goldenFileName turns a symbol ID into a filesystem-safe golden file name.
func goldenFileName(symbolID string) string {
	r := strings.NewReplacer("/", "_", "(", "", ")", "", ".", "_")
	return r.Replace(symbolID) + ".txt"
}
