package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func board(t *testing.T) *Board {
	t.Helper()
	b := NewBoard("TH")
	for _, title := range []string{"schema", "api", "ui", "docs"} {
		b.Add(title, "", StatusTodo, nil)
	}
	return b
}

func TestNormalizeID(t *testing.T) {
	b := NewBoard("TH")
	for in, want := range map[string]string{"3": "TH-3", "th-3": "TH-3", "TH-3": "TH-3"} {
		if got := b.NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlocksIsTheReverseOfBlockedBy(t *testing.T) {
	b := board(t)
	api, _ := b.Get("TH-2")
	if err := b.SetBlockedBy(api, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if got := b.Blocks("TH-1"); len(got) != 1 || got[0] != "TH-2" {
		t.Fatalf("Blocks(TH-1) = %v, want [TH-2]", got)
	}
}

func TestReadyFollowsBlockerStatus(t *testing.T) {
	b := board(t)
	schema, _ := b.Get("TH-1")
	api, _ := b.Get("TH-2")
	if err := b.SetBlockedBy(api, []string{"TH-1"}); err != nil {
		t.Fatal(err)
	}
	if b.Ready(api) {
		t.Fatal("TH-2 should not be ready while TH-1 is todo")
	}
	if !b.Ready(schema) {
		t.Fatal("TH-1 has no blockers, should be ready")
	}
	schema.Status = StatusDone
	if !b.Ready(api) {
		t.Fatal("TH-2 should be ready once TH-1 is done")
	}
	if b.Ready(schema) {
		t.Fatal("a done issue is never ready")
	}
}

func TestTransitiveEdges(t *testing.T) {
	b := board(t)
	// schema <- api <- ui <- docs
	for _, pair := range [][2]string{{"TH-2", "TH-1"}, {"TH-3", "TH-2"}, {"TH-4", "TH-3"}} {
		is, _ := b.Get(pair[0])
		if err := b.SetBlockedBy(is, []string{pair[1]}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(b.Deps("TH-4"), ","); got != "TH-3,TH-2,TH-1" {
		t.Errorf("Deps(TH-4) = %q", got)
	}
	if got := strings.Join(b.Dependents("TH-1"), ","); got != "TH-2,TH-3,TH-4" {
		t.Errorf("Dependents(TH-1) = %q", got)
	}
}

func TestCyclesAreRefused(t *testing.T) {
	b := board(t)
	api, _ := b.Get("TH-2")
	ui, _ := b.Get("TH-3")
	if err := b.SetBlockedBy(api, []string{"TH-1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBlockedBy(ui, []string{"TH-2"}); err != nil {
		t.Fatal(err)
	}
	schema, _ := b.Get("TH-1")
	if err := b.SetBlockedBy(schema, []string{"TH-3"}); err == nil {
		t.Fatal("TH-1 blocked by TH-3 closes a cycle and should be refused")
	}
	if len(schema.BlockedBy) != 0 {
		t.Fatalf("a refused edge must not be left behind: %v", schema.BlockedBy)
	}
	if err := b.SetBlockedBy(schema, []string{"TH-1"}); err == nil {
		t.Fatal("an issue must not be allowed to block itself")
	}
}

func TestSaveRoundTripsAndKeepsDescriptionsReadable(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Path: filepath.Join(dir, StoreName)}
	if err := s.Create("TH"); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(b *Board) error {
		b.Add("multi", "first line\nsecond line", StatusTodo, []string{"x"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	// A YAML block scalar is what makes the file worth committing: an edited
	// description shows up as a one-line diff, not an escaped blob.
	if !strings.Contains(string(raw), "description: |-") {
		t.Errorf("want a literal block scalar for the description, got:\n%s", raw)
	}
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Issues) != 1 || b.Issues[0].Description != "first line\nsecond line" {
		t.Fatalf("round trip lost data: %+v", b.Issues)
	}
}

func TestFindStoreWalksUp(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, StoreName)
	if err := (&Store{Path: want}).Create("TH"); err != nil {
		t.Fatal(err)
	}
	got, err := FindStore(deep)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(got); resolved != mustResolve(t, want) {
		t.Errorf("FindStore = %q, want %q", got, want)
	}
	if _, err := FindStore(t.TempDir()); err == nil {
		t.Error("FindStore should fail when there is no board anywhere above")
	}
}

func mustResolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
