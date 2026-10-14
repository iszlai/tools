package radius

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/diff"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// gitFixture builds a two-commit temp git repo: oldFiles as the first commit,
// then newFiles as the second (a path present in oldFiles but absent from
// newFiles is deleted; a path in both with identical content never appears in
// the diff — exactly a real multi-file commit, where most files are
// untouched). It returns the repo's directory and the real `git diff` between
// the two commits, parsed by internal/core/diff — the same approach
// internal/lang/golang's symbols_test.go uses (gitDiffFixture), generalized
// to more than one file so a changed interface, an updated implementer and an
// untouched one can all live in the same commit.
func gitFixture(t *testing.T, oldFiles, newFiles map[string]string) (dir string, files []model.ChangedFile) {
	t.Helper()
	dir = t.TempDir()
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
	write := func(set map[string]string) {
		for path, content := range set {
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	run("init", "-q", "-b", "main", ".")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.com")

	write(oldFiles)
	run("add", "-A")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "old")

	for path := range oldFiles {
		if _, ok := newFiles[path]; !ok {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(path))); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(newFiles)
	run("add", "-A")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "new")

	out := run("diff", "--no-color", "HEAD~1", "HEAD")
	files, err := diff.Parse([]byte(out))
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return dir, files
}

// oldContentFunc builds a golang.OldContent that reads from oldFiles —
// exactly what a real reviewer's "diff's old revision" reader would return,
// without needing a second git invocation per file.
func oldContentFunc(t *testing.T, oldFiles map[string]string) golang.OldContent {
	t.Helper()
	return func(path string) ([]byte, error) {
		content, ok := oldFiles[path]
		if !ok {
			t.Fatalf("unexpected old-content request for %q", path)
		}
		return []byte(content), nil
	}
}

func load(t *testing.T, dir string, changedFiles []string) *golang.Result {
	t.Helper()
	l := &golang.Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, changedFiles)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res
}

func findFunc(r Report, symbol string) *FuncRadius {
	for i := range r.Functions {
		if r.Functions[i].Symbol == symbol {
			return &r.Functions[i]
		}
	}
	return nil
}

func hasCaller(callers []Caller, symbol string) bool {
	for _, c := range callers {
		if c.Symbol == symbol {
			return true
		}
	}
	return false
}

func hasImpl(impls []Impl, symbol string) *Impl {
	for i := range impls {
		if impls[i].Symbol == symbol {
			return &impls[i]
		}
	}
	return nil
}

const modPath = "radius.example/flowreviewtest"

var goMod = map[string]string{"go.mod": "module " + modPath + "\n\ngo 1.25\n"}

func withGoMod(files map[string]string) map[string]string {
	out := make(map[string]string, len(files)+1)
	for k, v := range goMod {
		out[k] = v
	}
	for k, v := range files {
		out[k] = v
	}
	return out
}

// TestFuncRadius_UnchangedCallersInOtherFiles covers the plain-function
// scenario: Foo's body changes (not its signature); two callers in two other
// packages never change, and both must be listed.
func TestFuncRadius_UnchangedCallersInOtherFiles(t *testing.T) {
	oldFiles := withGoMod(map[string]string{
		"calc/calc.go": `package calc

// Foo is changed below: same signature, different body.
func Foo(x int) int { return x + 1 }
`,
		"user1/user1.go": `package user1

import "radius.example/flowreviewtest/calc"

// UseFoo1 calls Foo and never changes across this diff.
func UseFoo1() int { return calc.Foo(5) }
`,
		"user2/user2.go": `package user2

import "radius.example/flowreviewtest/calc"

// UseFoo2 calls Foo and never changes across this diff.
func UseFoo2() int { return calc.Foo(10) }
`,
	})
	newFiles := withGoMod(map[string]string{
		"calc/calc.go": `package calc

// Foo is changed below: same signature, different body.
func Foo(x int) int { return x * 2 }
`,
		"user1/user1.go": oldFiles["user1/user1.go"],
		"user2/user2.go": oldFiles["user2/user2.go"],
	})

	dir, files := gitFixture(t, oldFiles, newFiles)
	res := load(t, dir, []string{"calc/calc.go"})
	old := oldContentFunc(t, oldFiles)

	report, err := Compute(res, files, old)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	foo := findFunc(report, modPath+"/calc.Foo")
	if foo == nil {
		t.Fatalf("Foo missing from report; got %+v", report)
	}
	if foo.SignatureChanged {
		t.Error("Foo.SignatureChanged = true, want false (body-only change)")
	}
	if len(foo.UnchangedCallers) != 2 {
		t.Fatalf("Foo.UnchangedCallers = %+v, want exactly 2", foo.UnchangedCallers)
	}
	if !hasCaller(foo.UnchangedCallers, modPath+"/user1.UseFoo1") {
		t.Errorf("Foo.UnchangedCallers = %+v, missing UseFoo1", foo.UnchangedCallers)
	}
	if !hasCaller(foo.UnchangedCallers, modPath+"/user2.UseFoo2") {
		t.Errorf("Foo.UnchangedCallers = %+v, missing UseFoo2", foo.UnchangedCallers)
	}
	for _, c := range foo.UnchangedCallers {
		if c.InDiff {
			t.Errorf("caller %s InDiff = true, want false (its file never changed)", c.Symbol)
		}
		if c.Confidence != model.ConfStatic {
			t.Errorf("caller %s Confidence = %s, want static", c.Symbol, c.Confidence)
		}
	}
	if len(foo.CallSites) != 0 {
		t.Errorf("Foo.CallSites = %+v, want none (signature did not change)", foo.CallSites)
	}
}

// TestInterfaceRadius_MissingImplementerFlagged is FR-19's headline scenario:
// Speaker.Speak's signature changes; dogpkg.Dog is updated to match (in the
// diff), catpkg.Cat is not (its file never changes) — Cat must be flagged.
func TestInterfaceRadius_MissingImplementerFlagged(t *testing.T) {
	oldFiles := withGoMod(map[string]string{
		"iface/iface.go": `package iface

// Speaker can speak. Speak's signature changes below.
type Speaker interface {
	Speak(x int) string
}
`,
		"dogpkg/dog.go": `package dogpkg

type Dog struct{}

func (Dog) Speak(x int) string { return "woof" }
`,
		"catpkg/cat.go": `package catpkg

// Blank-imported so catpkg is iface's in-module reverse importer (loader.go
// pulls it into scope on that edge alone) without asserting Cat satisfies
// Speaker anywhere — catpkg never changes across this diff, which is exactly
// how a forgotten implementer looks in a real PR: nothing here even notices
// the interface moved.
import _ "radius.example/flowreviewtest/iface"

type Cat struct{}

func (Cat) Speak(x int) string { return "meow" }
`,
		"caller/caller.go": `package caller

import (
	"radius.example/flowreviewtest/dogpkg"
	"radius.example/flowreviewtest/iface"
)

// Announce calls through the interface; it is updated below to pass the new
// argument, so it stays a valid call site.
func Announce(s iface.Speaker) string { return s.Speak(1) }

// Demo connects Announce to a concrete implementer with an actual call, so
// VTA can resolve the interface dispatch (declaring Speaker and Dog alone is
// not enough — see internal/lang/golang's own iface fixture).
func Demo() string { return Announce(dogpkg.Dog{}) }
`,
	})
	newFiles := withGoMod(map[string]string{
		"iface/iface.go": `package iface

// Speaker can speak. Speak's signature changes below.
type Speaker interface {
	Speak(x int, loud bool) string
}
`,
		"dogpkg/dog.go": `package dogpkg

type Dog struct{}

func (Dog) Speak(x int, loud bool) string { return "woof" }
`,
		"catpkg/cat.go": oldFiles["catpkg/cat.go"],
		"caller/caller.go": `package caller

import (
	"radius.example/flowreviewtest/dogpkg"
	"radius.example/flowreviewtest/iface"
)

// Announce calls through the interface; it is updated below to pass the new
// argument, so it stays a valid call site.
func Announce(s iface.Speaker) string { return s.Speak(1, true) }

// Demo connects Announce to a concrete implementer with an actual call, so
// VTA can resolve the interface dispatch (declaring Speaker and Dog alone is
// not enough — see internal/lang/golang's own iface fixture).
func Demo() string { return Announce(dogpkg.Dog{}) }
`,
	})

	dir, files := gitFixture(t, oldFiles, newFiles)
	// catpkg/cat.go never changes, so it is not in the changed-files list at
	// all — only the packages that do change are named here; the loader pulls
	// catpkg and caller in on its own, as in-module reverse importers of
	// iface (see loader.go's scopeFor).
	res := load(t, dir, []string{"iface/iface.go", "dogpkg/dog.go", "caller/caller.go"})
	old := oldContentFunc(t, oldFiles)

	report, err := Compute(res, files, old)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	methodID := modPath + "/iface.(Speaker).Speak"
	fr := findFunc(report, methodID)
	if fr == nil {
		t.Fatalf("interface method %s missing from report; got %+v", methodID, report)
	}
	if !fr.SignatureChanged {
		t.Error("Speak.SignatureChanged = false, want true")
	}

	dogMethod := modPath + "/dogpkg.(Dog).Speak"
	dogImpl := hasImpl(fr.Implementers, dogMethod)
	if dogImpl == nil {
		t.Fatalf("Implementers = %+v, missing Dog", fr.Implementers)
	}
	if !dogImpl.InDiff {
		t.Error("Dog.Speak InDiff = false, want true (dog.go is part of the diff)")
	}
	if dogImpl.Confidence != model.ConfStatic {
		t.Errorf("Dog.Speak Confidence = %s, want static (still satisfies the interface)", dogImpl.Confidence)
	}

	catMethod := modPath + "/catpkg.(Cat).Speak"
	catImpl := hasImpl(fr.Implementers, catMethod)
	if catImpl == nil {
		t.Fatalf("Implementers = %+v, missing Cat (the forgotten implementer)", fr.Implementers)
	}
	if catImpl.InDiff {
		t.Error("Cat.Speak InDiff = true, want false (cat.go never changed)")
	}
	if catImpl.Confidence != model.ConfHeuristic {
		t.Errorf("Cat.Speak Confidence = %s, want heuristic (no longer satisfies the interface by type)", catImpl.Confidence)
	}

	wantWarning := catMethod + " implements " + methodID + " but is not changed in this diff"
	found := false
	for _, w := range fr.Warnings {
		if w == wantWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want %q", fr.Warnings, wantWarning)
	}

	// Announce's call site was updated to match the new signature, so it must
	// show up as a call site, in the diff.
	if len(fr.CallSites) == 0 {
		t.Fatal("CallSites is empty, want Announce's call through Dog")
	}
	siteOK := false
	for _, s := range fr.CallSites {
		if s.Caller == modPath+"/caller.Announce" && s.InDiff {
			siteOK = true
		}
	}
	if !siteOK {
		t.Errorf("CallSites = %+v, want Announce's call site flagged in diff", fr.CallSites)
	}
}

// TestText_NoChanges covers the empty case.
func TestText_NoChanges(t *testing.T) {
	var buf strings.Builder
	Text(Report{}, &buf)
	if buf.String() == "" {
		t.Error("Text wrote nothing for an empty report")
	}
}
