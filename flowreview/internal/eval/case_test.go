package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCaseFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCasesSortedByFilename(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "002.json", `{"path":"b.go","hunk":"+b\n","expected":{"kind":"behavior"}}`)
	writeCaseFile(t, dir, "001.json", `{"path":"a.go","hunk":"+a\n","expected":{"kind":"refactor"}}`)
	writeCaseFile(t, dir, "notes.txt", "ignore me")

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("want 2 cases (txt file ignored), got %d", len(cases))
	}
	if cases[0].Path != "a.go" || cases[1].Path != "b.go" {
		t.Fatalf("want a.go then b.go in filename order, got %q then %q", cases[0].Path, cases[1].Path)
	}
}

func TestLoadCasesIDDefaultsToFilename(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "007.json", `{"path":"a.go","hunk":"+a\n","expected":{"kind":"behavior"}}`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cases[0].ID != "007" {
		t.Fatalf("want id \"007\" defaulted from filename, got %q", cases[0].ID)
	}
}

func TestLoadCasesExplicitID(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "x.json", `{"id":"custom","path":"a.go","hunk":"+a\n","expected":{"kind":"behavior"}}`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cases[0].ID != "custom" {
		t.Fatalf("want explicit id to win, got %q", cases[0].ID)
	}
}

func TestLoadCasesMissingDir(t *testing.T) {
	if _, err := LoadCases(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want an error for a missing directory")
	}
}
