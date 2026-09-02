package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cycle cannot be created through th, so it has to be written directly —
// which is exactly how a real board gets one: a hand edit, a merge, or an
// older binary.
func handEdited(t *testing.T, yaml string) *Store {
	t.Helper()
	s := &Store{Path: filepath.Join(t.TempDir(), StoreName)}
	if err := os.WriteFile(s.Path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

const cyclicBoard = `version: 1
prefix: TH
next_id: 4
issues:
    - id: TH-1
      title: First
      status: todo
      blocked_by: [TH-2]
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
    - id: TH-2
      title: Second
      status: todo
      blocked_by: [TH-3]
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
    - id: TH-3
      title: Third
      status: todo
      blocked_by: [TH-1]
      priority: high
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
`

func TestCycleIsDetected(t *testing.T) {
	b, err := handEdited(t, cyclicBoard).Read()
	if err != nil {
		t.Fatal(err)
	}
	cycles := b.Cycles()
	if len(cycles) != 1 {
		t.Fatalf("found %d cycles, want 1: %v", len(cycles), cycles)
	}
	if len(cycles[0]) != 3 {
		t.Errorf("cycle should name all three members: %v", cycles[0])
	}
	// Nothing can be ready in a loop.
	for _, is := range b.Issues {
		if b.Ready(is) {
			t.Errorf("%s reported ready inside a cycle", is.ID)
		}
	}
}

func TestCycleForcesAPickByPriority(t *testing.T) {
	b, err := handEdited(t, cyclicBoard).Read()
	if err != nil {
		t.Fatal(err)
	}
	d := b.Diagnose(false)
	if d == nil || d.Forced == nil {
		t.Fatal("a deadlocked board must still hand back something to do")
	}
	// TH-3 is the only high one, so it is the one to start.
	if d.Forced.ID != "TH-3" {
		t.Errorf("forced pick = %s, want TH-3 (the highest priority)", d.Forced.ID)
	}
	if !strings.Contains(d.Reason, "loop") {
		t.Errorf("reason should name the loop: %q", d.Reason)
	}
}

func TestDanglingBlockerIsDetected(t *testing.T) {
	s := handEdited(t, `version: 1
prefix: TH
next_id: 2
issues:
    - id: TH-1
      title: Blocked by a ghost
      status: todo
      blocked_by: [TH-99]
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
`)
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Dangling(); len(got["TH-1"]) != 1 || got["TH-1"][0] != "TH-99" {
		t.Fatalf("dangling = %v, want TH-1 -> [TH-99]", got)
	}
	d := b.Diagnose(false)
	if d == nil || d.Forced == nil || d.Forced.ID != "TH-1" {
		t.Fatalf("a board blocked by a ghost must still force a pick: %+v", d)
	}
	if !strings.Contains(d.Reason, "not on the board") {
		t.Errorf("reason should say the blocker does not exist: %q", d.Reason)
	}
}

// A loop that is not blocking you today is still corrupt, so it is reported
// even when there is other work to get on with.
func TestCycleIsReportedEvenWhenSomethingIsReady(t *testing.T) {
	s := handEdited(t, cyclicBoard+`    - id: TH-4
      title: Perfectly fine
      status: todo
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
`)
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	d := b.Diagnose(true)
	if d == nil || len(d.Cycles) != 1 {
		t.Fatalf("the cycle should still be reported: %+v", d)
	}
	if d.Forced != nil {
		t.Error("nothing should be forced while real work is available")
	}
}

func TestHealthyBoardHasNoDiagnosis(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), StoreName)}
	if err := s.Create("TH"); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(b *Board) error {
		a := b.Add("First", "", StatusTodo, PriorityNormal, nil)
		second := b.Add("Second", "", StatusTodo, PriorityNormal, nil)
		return b.SetBlockedBy(second, []string{a.ID})
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Read()
	if d := b.Diagnose(true); d != nil {
		t.Errorf("a healthy board should diagnose clean: %+v", d)
	}
}

// A closed loop is history, not a problem.
func TestADoneCycleIsNotReported(t *testing.T) {
	s := handEdited(t, strings.ReplaceAll(cyclicBoard, "status: todo", "status: done"))
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Cycles(); len(got) != 0 {
		t.Errorf("a cycle among done issues should be ignored: %v", got)
	}
}
