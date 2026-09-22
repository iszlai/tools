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
		b.Add(title, "", StatusTodo, PriorityNormal, nil)
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
		b.Add("multi", "first line\nsecond line", StatusTodo, PriorityNormal, []string{"x"})
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

// The merged board: two branches each filed an issue, so both bumped the
// counter to the same number and only the issues conflicted. Keeping both is
// the right call for the issues and leaves the counter describing neither.
const mergedBoard = `version: 1
prefix: TH
next_id: 4
issues:
    - id: TH-1
      title: First
      status: todo
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
    - id: TH-3
      title: Alpha work
      status: todo
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
    - id: TH-3
      title: Beta work
      status: todo
      created_at: 2026-09-02T10:00:01Z
      updated_at: 2026-09-02T10:00:01Z
`

func TestAddSkipsIDsTheBoardAlreadyHolds(t *testing.T) {
	s := handEdited(t, mergedBoard)
	var got []string
	for i := 0; i < 3; i++ {
		if err := s.Update(func(b *Board) error {
			got = append(got, b.Add("new", "", StatusTodo, PriorityNormal, nil).ID)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// next_id said TH-4, but TH-3 was already spoken for twice; the counter is
	// a floor, so minting starts above everything in hand.
	if want := []string{"TH-4", "TH-5", "TH-6"}; !equalStrings(got, want) {
		t.Fatalf("minted %v, want %v", got, want)
	}
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, is := range b.Issues {
		seen[is.ID]++
	}
	for id, n := range seen {
		if id != "TH-3" && n > 1 {
			t.Errorf("%s was handed out %d times", id, n)
		}
	}
}

// A counter behind the board is the state that keeps producing duplicates: it
// hands out the same ids on every add until someone notices. Minting has to
// climb past the board rather than trust it.
func TestAddClimbsPastACounterLeftBehind(t *testing.T) {
	s := handEdited(t, strings.Replace(mergedBoard, "next_id: 4", "next_id: 2", 1))
	var id string
	if err := s.Update(func(b *Board) error {
		id = b.Add("new", "", StatusTodo, PriorityNormal, nil).ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if id != "TH-4" {
		t.Fatalf("minted %s, want TH-4 — a counter behind the board must not be believed", id)
	}
}

// An id in the done log is not free either: reusing it would put the same name
// in two files, with the log entries that explain it pointing at whichever you
// read first.
func TestAddSkipsIDsTheDoneLogHolds(t *testing.T) {
	s := handEdited(t, mergedBoard)
	done := `version: 1
issues:
    - id: TH-6
      title: Finished months ago
      status: done
      created_at: 2026-08-01T10:00:00Z
      updated_at: 2026-08-01T10:00:00Z
      archived_at: 2026-08-15T10:00:00Z
`
	if err := os.WriteFile(s.ArchivePath(), []byte(done), 0o644); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.Update(func(b *Board) error {
		id = b.Add("new", "", StatusTodo, PriorityNormal, nil).ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if id != "TH-7" {
		t.Fatalf("minted %s, want TH-7 — the done log holds TH-6", id)
	}
}

// Someone else's ids turn up on a board: pasted into a title, tracked in a
// migration. TH-9 is not spent because FOO-9 exists.
func TestAnotherPrefixDoesNotSpendAnID(t *testing.T) {
	s := handEdited(t, mergedBoard+`    - id: FOO-90
      title: Not ours
      status: todo
      created_at: 2026-09-02T10:00:00Z
      updated_at: 2026-09-02T10:00:00Z
`)
	var id string
	if err := s.Update(func(b *Board) error {
		id = b.Add("new", "", StatusTodo, PriorityNormal, nil).ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if id != "TH-4" {
		t.Fatalf("minted %s, want TH-4 — FOO-90 is not this board's", id)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A done log that will not parse must not take the board down with it: reading
// still works, and only the write that would have to know which ids are spent
// is refused.
func TestABrokenDoneLogStopsWritesAndNotReads(t *testing.T) {
	s := handEdited(t, mergedBoard)
	if err := os.WriteFile(s.ArchivePath(), []byte("<<<<<<< HEAD\nissues: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := s.Read()
	if err != nil {
		t.Fatalf("reading the board should survive a broken done log: %v", err)
	}
	if len(b.Issues) != 3 {
		t.Fatalf("got %d issues, want 3", len(b.Issues))
	}
	err = s.Update(func(b *Board) error {
		b.Add("new", "", StatusTodo, PriorityNormal, nil)
		return nil
	})
	if err == nil {
		t.Fatal("an add must not mint an id while the done log is unreadable")
	}
	if !strings.Contains(err.Error(), "done log") {
		t.Errorf("the error should name the file that is broken: %v", err)
	}
}
