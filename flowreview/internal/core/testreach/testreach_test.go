package testreach

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// changedFileAt returns a model.ChangedFile naming relPath as modified, with
// one 1-line hunk at the line in root/relPath containing needle. A 1-line
// hunk at a function's "func Name(" line always overlaps that function's
// declaration range (symbols.go's decl ranges run from the func keyword to
// the closing brace), so this is enough to mark it changed without having to
// know where the function ends.
func changedFileAt(t *testing.T, root, relPath, needle string) model.DiffHunk {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, needle) {
			return model.DiffHunk{NewStart: i + 1, NewLines: 1}
		}
	}
	t.Fatalf("needle %q not found in %s", needle, relPath)
	return model.DiffHunk{}
}

// loadFixture loads testdata/fixture's one package and returns a Result plus
// a files slice marking Direct, TransitiveTarget, (Impl).Do, Unreached and
// ExternalTarget as changed -- one function per FR-21 accept criterion.
func loadFixture(t *testing.T) (*golang.Result, []model.ChangedFile) {
	t.Helper()
	dir := fixtureDir(t)
	l := &golang.Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"pkg/pkg.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const relPath = "pkg/pkg.go"
	hunks := []model.DiffHunk{
		changedFileAt(t, dir, relPath, "func Direct("),
		changedFileAt(t, dir, relPath, "func TransitiveTarget("),
		changedFileAt(t, dir, relPath, "func (Impl) Do("),
		changedFileAt(t, dir, relPath, "func Unreached("),
		changedFileAt(t, dir, relPath, "func ExternalTarget("),
	}
	files := []model.ChangedFile{{
		NewPath: relPath,
		Status:  model.FileModified,
		Hunks:   hunks,
	}}
	return res, files
}

func findFunc(fns []FunctionReach, nameSuffix string) *FunctionReach {
	for i := range fns {
		if strings.HasSuffix(fns[i].Symbol.ID, nameSuffix) {
			return &fns[i]
		}
	}
	return nil
}

func TestCompute_DirectlyReached(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	fr := findFunc(report.Functions, ".Direct")
	if fr == nil {
		t.Fatal("Direct not in report.Functions")
	}
	if !fr.Reached {
		t.Error("Direct should be reached (called directly by TestDirect)")
	}
	if len(fr.By) != 1 || !strings.HasSuffix(fr.By[0], ".TestDirect") {
		t.Errorf("Direct.By = %v, want exactly [...TestDirect]", fr.By)
	}
	if len(fr.Path) != 2 {
		t.Errorf("Direct.Path = %v, want [TestDirect, Direct]", fr.Path)
	}
}

func TestCompute_TransitivelyReached(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	fr := findFunc(report.Functions, ".TransitiveTarget")
	if fr == nil {
		t.Fatal("TransitiveTarget not in report.Functions")
	}
	if !fr.Reached {
		t.Fatal("TransitiveTarget should be reached (TestTransitive -> CallsHelper -> TransitiveTarget)")
	}
	if len(fr.By) != 1 || !strings.HasSuffix(fr.By[0], ".TestTransitive") {
		t.Errorf("TransitiveTarget.By = %v, want exactly [...TestTransitive]", fr.By)
	}
	if len(fr.Path) != 3 {
		t.Errorf("TransitiveTarget.Path = %v, want a 3-symbol chain (test, CallsHelper, target)", fr.Path)
	}
}

func TestCompute_ReachedOnlyViaInterfaceCall(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	fr := findFunc(report.Functions, ".(Impl).Do")
	if fr == nil {
		t.Fatal("(Impl).Do not in report.Functions")
	}
	if !fr.Reached {
		t.Error("(Impl).Do should be reached (TestInterface calls it through the Doer interface)")
	}
	if len(fr.By) != 1 || !strings.HasSuffix(fr.By[0], ".TestInterface") {
		t.Errorf("(Impl).Do.By = %v, want exactly [...TestInterface]", fr.By)
	}
}

func TestCompute_Unreached(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	fr := findFunc(report.Functions, ".Unreached")
	if fr == nil {
		t.Fatal("Unreached not in report.Functions")
	}
	if fr.Reached {
		t.Errorf("Unreached should not be reached, got By=%v Path=%v", fr.By, fr.Path)
	}
	if len(fr.By) != 0 || len(fr.Path) != 0 {
		t.Errorf("an unreached function must carry no By/Path, got %+v", fr)
	}

	found := false
	for _, s := range report.Unreached {
		if strings.HasSuffix(s.ID, ".Unreached") {
			found = true
		}
	}
	if !found {
		t.Errorf("report.Unreached = %+v, missing Unreached", report.Unreached)
	}
}

func TestCompute_ReachedByExternalTestPackage(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	fr := findFunc(report.Functions, ".ExternalTarget")
	if fr == nil {
		t.Fatal("ExternalTarget not in report.Functions")
	}
	if !fr.Reached {
		t.Error("ExternalTarget should be reached by the external _test package's TestExternal")
	}
	if len(fr.By) != 1 || !strings.HasSuffix(fr.By[0], ".TestExternal") {
		t.Errorf("ExternalTarget.By = %v, want exactly [...TestExternal]", fr.By)
	}
}

// TestCompute_SuggestedCommand covers the per-package `go test -run`
// command: every test that reaches changed code lives in testdata/fixture's
// one directory ("pkg"), across both the internal and the external _test.go
// files, so one PackageCommand should list all five, sorted.
func TestCompute_SuggestedCommand(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(report.Commands) != 1 {
		t.Fatalf("Commands = %+v, want exactly one package dir", report.Commands)
	}
	cmd := report.Commands[0]
	if cmd.Dir != "pkg" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "pkg")
	}

	wantTests := []string{"TestDirect", "TestExternal", "TestInterface", "TestTransitive"}
	if len(cmd.Tests) != len(wantTests) {
		t.Fatalf("Tests = %v, want %v", cmd.Tests, wantTests)
	}
	for i, name := range wantTests {
		if cmd.Tests[i] != name {
			t.Errorf("Tests[%d] = %q, want %q (full: %v)", i, cmd.Tests[i], name, cmd.Tests)
		}
	}

	wantCmd := "go test ./pkg -run '^(TestDirect|TestExternal|TestInterface|TestTransitive)$'"
	if cmd.Command != wantCmd {
		t.Errorf("Command = %q, want %q", cmd.Command, wantCmd)
	}
}

func TestCompute_NoChangedFunctions(t *testing.T) {
	res, _ := loadFixture(t)
	report, err := Compute(res, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(report.Functions) != 0 || len(report.Unreached) != 0 || len(report.Commands) != 0 {
		t.Errorf("Compute(nil files) = %+v, want a zero Report", report)
	}
}

func TestCompute_NilResult(t *testing.T) {
	if _, err := Compute(nil, nil); err == nil {
		t.Error("Compute(nil, ...) should error")
	}
}

func TestText_OneScreen(t *testing.T) {
	res, files := loadFixture(t)
	report, err := Compute(res, files)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var buf strings.Builder
	if err := Text(report, &buf); err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Test reach:") {
		t.Errorf("Text output missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "go test ./pkg -run") {
		t.Errorf("Text output missing suggested command:\n%s", out)
	}
	if !strings.Contains(out, ".Unreached") {
		t.Errorf("Text output missing the unreached function:\n%s", out)
	}
}
