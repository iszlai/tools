package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scenario this exists for: a board written by a newer th, opened by this
// build, which has no field for `priority` or for whatever comes after it.
const boardFromTheFuture = `version: 1
prefix: TH
next_id: 3
some_future_board_field: kept
issues:
    - id: TH-1
      title: An issue
      status: todo
      priority: must
      some_future_field: kept
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
    - id: TH-2
      title: Another
      status: todo
      labels:
        - keepme
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
`

func futureBoard(t *testing.T) *Store {
	t.Helper()
	s := &Store{Path: filepath.Join(t.TempDir(), StoreName)}
	if err := os.WriteFile(s.Path, []byte(boardFromTheFuture), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

func read(t *testing.T, s *Store) string {
	t.Helper()
	data, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestUnknownFieldsSurviveAWrite is the regression: this used to delete every
// key the struct had no field for, with no error.
func TestUnknownFieldsSurviveAWrite(t *testing.T) {
	s := futureBoard(t)
	if err := s.Update(func(b *Board) error {
		is, err := b.Get("TH-1")
		if err != nil {
			return err
		}
		is.Title = "Edited by an older th"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	out := read(t, s)
	for _, want := range []string{
		"some_future_board_field: kept", // unknown at board level
		"some_future_field: kept",       // unknown at issue level
		"Edited by an older th",         // and the edit still landed
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q after a write:\n%s", want, out)
		}
	}
}

// The other half, and the reason this cannot just copy everything back:
// removing a field this build *does* understand has to stick.
func TestKnownFieldsCanStillBeRemoved(t *testing.T) {
	s := futureBoard(t)
	if err := s.Update(func(b *Board) error {
		is, err := b.Get("TH-2")
		if err != nil {
			return err
		}
		is.Labels = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if out := read(t, s); strings.Contains(out, "keepme") {
		t.Errorf("a removed label came back:\n%s", out)
	}
}

func TestArchivingStillRemovesTheIssue(t *testing.T) {
	s := futureBoard(t)
	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-1")
		is.Status = StatusDone
		is.UpdatedAt = now().Add(-48 * 60 * 60 * 1e9)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveDone(now(), false); err != nil {
		t.Fatal(err)
	}
	out := read(t, s)
	if strings.Contains(out, "TH-1") {
		t.Errorf("an archived issue was resurrected by the merge:\n%s", out)
	}
	// ...while the surviving issue keeps its unknown board-level company.
	if !strings.Contains(out, "some_future_board_field: kept") {
		t.Errorf("board-level unknown key lost during archive:\n%s", out)
	}
}

// A file from a schema this build cannot represent is refused rather than
// rewritten into something lossy.
func TestANewerSchemaIsRefusedOnWrite(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), StoreName)}
	if err := os.WriteFile(s.Path, []byte("version: 99\nprefix: TH\nnext_id: 1\nissues: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reading is fine — you can still look at it.
	if _, err := s.Read(); err != nil {
		t.Fatalf("a newer board should still be readable: %v", err)
	}
	err := s.Update(func(b *Board) error {
		b.Add("nope", "", StatusTodo, PriorityNormal, nil)
		return nil
	})
	if err == nil {
		t.Fatal("writing a newer schema should be refused")
	}
	if !strings.Contains(err.Error(), "upgrade th") {
		t.Errorf("the error should say what to do, got: %v", err)
	}
	if !strings.Contains(read(t, s), "version: 99") {
		t.Error("the refused write still changed the file")
	}
}
