package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func findSymbol(syms []model.CodeSymbol, id string) *model.CodeSymbol {
	for i := range syms {
		if syms[i].ID == id {
			return &syms[i]
		}
	}
	return nil
}

// lineOf returns the 1-based line number of the first line in path
// containing substr, failing the test if there is none.
func lineOf(t *testing.T, path, substr string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, substr) {
			return i + 1
		}
	}
	t.Fatalf("substring %q not found in %s", substr, path)
	return 0
}

// findIdent returns the 1-based line and column of the first occurrence of
// needle in src. needle should be specific enough not to also match inside a
// doc comment (e.g. "Widget()" rather than "Widget").
func findIdent(t *testing.T, src, needle string) (line, col int) {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if idx := strings.Index(l, needle); idx >= 0 {
			return i + 1, idx + 1
		}
	}
	t.Fatalf("needle %q not found", needle)
	return 0, 0
}

// gitDiffFixture builds a two-commit temp git repo — relPath holding
// oldContent, then either newContent (removeFile false) or deleted entirely
// (removeFile true) — and returns the real `git diff` between the two
// commits parsed by internal/core/diff, so ChangedSymbolsDetailed's tests get
// exactly the hunk data a real diff would produce instead of hand-computed
// line ranges.
func gitDiffFixture(t *testing.T, relPath, oldContent, newContent string, removeFile bool) []model.ChangedFile {
	t.Helper()
	dir := t.TempDir()
	env := []string{
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z",
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.com")

	if err := os.WriteFile(full, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "old")

	if removeFile {
		run("rm", "-q", relPath)
	} else {
		if err := os.WriteFile(full, []byte(newContent), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
	}
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "new")

	out := run("diff", "--no-color", "HEAD~1", "HEAD")
	files, err := diff.Parse([]byte(out))
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return files
}

func TestSymbols_ReceiverMethods(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"methods/methods.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	syms := Symbols(res)
	const pkg = "fixture.example/flowreviewtest/methods"

	value := findSymbol(syms, pkg+".(Counter).Value")
	if value == nil {
		t.Fatalf("missing value-receiver method symbol; got %+v", syms)
	}
	if value.Receiver != "Counter" {
		t.Errorf("Value.Receiver = %q, want %q", value.Receiver, "Counter")
	}
	if value.Kind != model.SymMethod {
		t.Errorf("Value.Kind = %v, want method", value.Kind)
	}

	add := findSymbol(syms, pkg+".(Counter).Add")
	if add == nil {
		t.Fatalf("missing pointer-receiver method symbol; got %+v", syms)
	}
	if add.Receiver != "*Counter" {
		t.Errorf("Add.Receiver = %q, want %q", add.Receiver, "*Counter")
	}
}

func TestSymbols_Generics(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"generics/generics.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	syms := Symbols(res)
	const pkg = "fixture.example/flowreviewtest/generics"

	max := findSymbol(syms, pkg+".Max")
	if max == nil {
		t.Fatalf("missing generic func symbol; got %+v", syms)
	}
	if max.Kind != model.SymFunc {
		t.Errorf("Max.Kind = %v, want func", max.Kind)
	}

	push := findSymbol(syms, pkg+".(Stack).Push")
	if push == nil {
		t.Fatalf("missing generic-type method symbol; got %+v", syms)
	}
	if push.Receiver != "*Stack" {
		t.Errorf("Push.Receiver = %q, want %q (type param dropped from the bare name)", push.Receiver, "*Stack")
	}

	if findSymbol(syms, pkg+".(Stack).Pop") == nil {
		t.Errorf("missing Pop method symbol; got %+v", syms)
	}
}

func TestSymbols_EmbeddedInterface(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"iface/iface.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	syms := Symbols(res)
	const pkg = "fixture.example/flowreviewtest/iface"

	rc := findSymbol(syms, pkg+".ReadCloser")
	if rc == nil {
		t.Fatalf("missing ReadCloser symbol; got %+v", syms)
	}
	if rc.Kind != model.SymInterface {
		t.Errorf("ReadCloser.Kind = %v, want interface", rc.Kind)
	}
	// go/types' TypeString lists an embedded interface by name, not flattened
	// into its promoted methods (matching `go doc`'s own formatting) — so the
	// embedding shows up as "Reader", not "Read() string".
	if !strings.Contains(rc.Signature, "Reader") || !strings.Contains(rc.Signature, "Close()") {
		t.Errorf("ReadCloser.Signature = %q, want both the embedded Reader and Close()", rc.Signature)
	}
}

func TestSymbolAt(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	fooLine := lineOf(t, filepath.Join(fixtureDir(t), "a", "a.go"), "func Foo")
	sym := SymbolAt(res, "a/a.go", fooLine)
	if sym == nil || sym.Name != "Foo" {
		t.Fatalf("SymbolAt(a/a.go, %d) = %+v, want Foo", fooLine, sym)
	}

	if sym := SymbolAt(res, "a/a.go", 1); sym != nil {
		t.Errorf("SymbolAt at the leading package comment = %+v, want nil", sym)
	}
}

func TestDefinition_CrossPackage(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	bPath := filepath.Join(fixtureDir(t), "b", "b.go")
	data, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatal(err)
	}
	line, col := findIdent(t, string(data), "Foo()")

	loc, err := Definition(res, "b/b.go", line, col)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if loc == nil {
		t.Fatal("Definition returned a nil location")
	}
	if loc.Path != "a/a.go" {
		t.Errorf("loc.Path = %q, want a/a.go", loc.Path)
	}
	wantLine := lineOf(t, filepath.Join(fixtureDir(t), "a", "a.go"), "func Foo")
	if loc.Line != wantLine {
		t.Errorf("loc.Line = %d, want %d", loc.Line, wantLine)
	}
}

func TestDefinition_DotImport(t *testing.T) {
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"dotimport/user/user.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	userPath := filepath.Join(fixtureDir(t), "dotimport", "user", "user.go")
	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	// " Widget()" (leading space), not "Widget()": the declaration line is
	// "func UseWidget() int { return Widget() }", and "Widget()" alone is
	// also a substring of "UseWidget()" — the leading space anchors past the
	// declaration to the actual call.
	line, col := findIdent(t, string(data), " Widget()")
	col++ // skip the leading space; findIdent's column is the space's own column

	loc, err := Definition(res, "dotimport/user/user.go", line, col)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if loc == nil {
		t.Fatal("Definition returned a nil location (dot-imported identifier did not resolve)")
	}
	if loc.Path != "dotimport/lib/lib.go" {
		t.Errorf("loc.Path = %q, want dotimport/lib/lib.go", loc.Path)
	}
	wantLine := lineOf(t, filepath.Join(fixtureDir(t), "dotimport", "lib", "lib.go"), "func Widget")
	if loc.Line != wantLine {
		t.Errorf("loc.Line = %d, want %d", loc.Line, wantLine)
	}
}

func TestChangedSymbols(t *testing.T) {
	newContent, err := os.ReadFile(filepath.Join(fixtureDir(t), "changed", "changed.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Patch only Modified's body back to its pre-change form; Kept, Resig and
	// Added stay exactly as the fixture file already has them.
	oldContent := strings.Replace(string(newContent), "return x + 1", "return x", 1)

	files := gitDiffFixture(t, "changed/changed.go", oldContent, string(newContent), false)
	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(files))
	}

	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"changed/changed.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	changed := ChangedSymbols(res, files)
	byName := map[string]model.CodeSymbol{}
	for _, s := range changed {
		if !s.Changed {
			t.Errorf("symbol %s returned by ChangedSymbols without Changed set", s.Name)
		}
		byName[s.Name] = s
	}
	if _, ok := byName["Kept"]; ok {
		t.Error("Kept should not be reported as changed")
	}
	if _, ok := byName["Modified"]; !ok {
		t.Error("Modified should be reported as changed")
	}
}

func TestChangedSymbolsDetailed(t *testing.T) {
	newContent, err := os.ReadFile(filepath.Join(fixtureDir(t), "changed", "changed.go"))
	if err != nil {
		t.Fatal(err)
	}
	oldContent := `// Package changed is a small fixture for FR-14's ChangedSymbolsDetailed:
// symbols_test.go diffs this file's content (the "new" side) against an
// older revision supplied inline in the test (the "old" side).
package changed

// Kept never changes between the old and new fixture content; it must never
// show up as a Change.
func Kept() int { return 1 }

// Modified keeps its name and signature but changes its body between the old
// and new fixture content.
func Modified(x int) int {
	return x
}

// Resig changes its signature, not just its body, between the old and new
// fixture content.
func Resig(x int) int {
	return x
}

// Removed exists only in the old fixture content.
func Removed() bool { return true }
`
	files := gitDiffFixture(t, "changed/changed.go", oldContent, string(newContent), false)
	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(files))
	}

	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"changed/changed.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	old := func(path string) ([]byte, error) {
		if path != "changed/changed.go" {
			return nil, fmt.Errorf("unexpected path %q", path)
		}
		return []byte(oldContent), nil
	}

	changes, err := ChangedSymbolsDetailed(res, files, old)
	if err != nil {
		t.Fatalf("ChangedSymbolsDetailed: %v", err)
	}

	byName := map[string]Change{}
	for _, c := range changes {
		byName[c.Symbol.Name] = c
	}

	if _, ok := byName["Kept"]; ok {
		t.Error("Kept should not appear in changes")
	}

	mod, ok := byName["Modified"]
	if !ok {
		t.Fatal("Modified missing from changes")
	}
	if mod.Kind != ChangeModified {
		t.Errorf("Modified.Kind = %v, want modified", mod.Kind)
	}
	if mod.SignatureChanged {
		t.Error("Modified.SignatureChanged = true, want false (body-only change)")
	}

	resig, ok := byName["Resig"]
	if !ok {
		t.Fatal("Resig missing from changes")
	}
	if resig.Kind != ChangeModified {
		t.Errorf("Resig.Kind = %v, want modified", resig.Kind)
	}
	if !resig.SignatureChanged {
		t.Errorf("Resig.SignatureChanged = false, want true (old=%q new=%q)", resig.OldSignature, resig.NewSignature)
	}

	added, ok := byName["Added"]
	if !ok {
		t.Fatal("Added missing from changes")
	}
	if added.Kind != ChangeAdded {
		t.Errorf("Added.Kind = %v, want added", added.Kind)
	}

	removed, ok := byName["Removed"]
	if !ok {
		t.Fatal("Removed missing from changes")
	}
	if removed.Kind != ChangeDeleted {
		t.Errorf("Removed.Kind = %v, want deleted", removed.Kind)
	}
}

// TestChangedSymbolsDetailed_WholeFileDeleted covers the case where the new
// side has no package at all to anchor an ID to: the deleted symbol's ID
// falls back to the structural-tier "path#Name" form (see structuralID).
func TestChangedSymbolsDetailed_WholeFileDeleted(t *testing.T) {
	oldContent := "package wholedelete\n\n// Gone exists only on the old side: the whole file is deleted.\nfunc Gone() int { return 1 }\n"
	files := gitDiffFixture(t, "wholedelete/wholedelete.go", oldContent, "", true)
	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(files))
	}
	if files[0].Status != model.FileDeleted {
		t.Fatalf("Status = %v, want FileDeleted", files[0].Status)
	}

	// res just needs to be a valid Result; wholedelete never had a package in
	// this module's scope, so nothing in it will ever match the deleted
	// symbol's ID.
	l := &Loader{Dir: fixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	old := func(path string) ([]byte, error) {
		if path != "wholedelete/wholedelete.go" {
			return nil, fmt.Errorf("unexpected path %q", path)
		}
		return []byte(oldContent), nil
	}

	changes, err := ChangedSymbolsDetailed(res, files, old)
	if err != nil {
		t.Fatalf("ChangedSymbolsDetailed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Kind != ChangeDeleted {
		t.Errorf("Kind = %v, want deleted", c.Kind)
	}
	const wantID = "wholedelete/wholedelete.go#Gone"
	if c.Symbol.ID != wantID {
		t.Errorf("ID = %q, want %q (structural-tier fallback: no live package to anchor to)", c.Symbol.ID, wantID)
	}
}
