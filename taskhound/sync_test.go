package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeGH stands in for the gh CLI: it records every invocation and keeps just
// enough state (issue numbers, open/closed) for the sync to behave as it would
// against a real repository.
type fakeGH struct {
	calls  [][]string
	next   int
	closed map[string]bool
	bodies map[string]string
}

func newFakeGH() *fakeGH {
	return &fakeGH{next: 100, closed: map[string]bool{}, bodies: map[string]string{}}
}

func (f *fakeGH) run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	switch {
	case args[0] == "issue" && args[1] == "create":
		f.next++
		n := strconv.Itoa(f.next)
		f.bodies[n] = flagValue(args, "--body")
		return "https://github.com/owner/repo/issues/" + n, nil
	case args[0] == "issue" && args[1] == "edit":
		f.bodies[args[2]] = flagValue(args, "--body")
		return "", nil
	case args[0] == "issue" && args[1] == "view":
		if f.closed[args[2]] {
			return "CLOSED", nil
		}
		return "OPEN", nil
	case args[0] == "issue" && args[1] == "close":
		f.closed[args[2]] = true
		return "", nil
	case args[0] == "issue" && args[1] == "reopen":
		f.closed[args[2]] = false
		return "", nil
	}
	return "", nil
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeGH) count(prefix ...string) int {
	n := 0
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			n++
		}
	}
	return n
}

func syncFixture(t *testing.T) (*Store, *fakeGH) {
	t.Helper()
	s := &Store{Path: filepath.Join(t.TempDir(), StoreName)}
	if err := s.Create("TH"); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(b *Board) error {
		schema := b.Add("Add the ledger schema", "Tables and indexes.", StatusTodo, PriorityNormal, []string{"backend"})
		api := b.Add("Expose the ledger API", "", StatusTodo, PriorityNormal, nil)
		return b.SetBlockedBy(api, []string{schema.ID})
	}); err != nil {
		t.Fatal(err)
	}
	return s, newFakeGH()
}

func mustSync(t *testing.T, s *Store, f *fakeGH, dryRun bool) {
	t.Helper()
	if err := syncBoard(s, "owner/repo", dryRun, f.run, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFilesBlockersFirstAndReferencesThem(t *testing.T) {
	s, f := syncFixture(t)
	mustSync(t, s, f, false)

	if got := f.count("issue", "create"); got != 2 {
		t.Fatalf("created %d issues, want 2", got)
	}

	// The blocker has to go up first, or its number does not exist to cite.
	var titles []string
	for _, c := range f.calls {
		if c[0] == "issue" && c[1] == "create" {
			titles = append(titles, flagValue(c, "--title"))
		}
	}
	if titles[0] != "Add the ledger schema" {
		t.Errorf("blocker was not filed first: %v", titles)
	}

	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := b.Get("TH-1")
	api, _ := b.Get("TH-2")
	if schema.GitHub == nil || api.GitHub == nil {
		t.Fatal("the board did not record the issue numbers")
	}

	body := f.bodies[strconv.Itoa(api.GitHub.Number)]
	if want := fmt.Sprintf("**Blocked by:** #%d", schema.GitHub.Number); !strings.Contains(body, want) {
		t.Errorf("body does not cite the blocker by number:\n%s", body)
	}
	if !strings.Contains(body, "taskhound: `TH-2`") {
		t.Errorf("body has no backlink:\n%s", body)
	}
	if !strings.Contains(f.bodies[strconv.Itoa(schema.GitHub.Number)], "Tables and indexes.") {
		t.Error("the description did not make it into the body")
	}
	if f.count("label", "create", "backend") != 1 {
		t.Error("the board's label was not created on GitHub")
	}
}

// TestSyncIsIdempotent is the whole point of recording the number: running it
// again must edit what is there, never file it a second time.
func TestSyncIsIdempotent(t *testing.T) {
	s, f := syncFixture(t)
	mustSync(t, s, f, false)
	mustSync(t, s, f, false)

	if got := f.count("issue", "create"); got != 2 {
		t.Fatalf("second run filed more issues: %d creates total, want 2", got)
	}
	if got := f.count("issue", "edit"); got != 2 {
		t.Errorf("second run made %d edits, want 2", got)
	}
}

func TestSyncCarriesCommentsExactlyOnce(t *testing.T) {
	s, f := syncFixture(t)
	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-1")
		is.Comments = append(is.Comments, Comment{At: now(), Author: "lehel", Body: "migration is green"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	mustSync(t, s, f, false)
	if got := f.count("issue", "comment"); got != 1 {
		t.Fatalf("posted %d comments, want 1", got)
	}
	mustSync(t, s, f, false)
	if got := f.count("issue", "comment"); got != 1 {
		t.Fatalf("re-syncing reposted the comment: %d total", got)
	}

	// A new comment on the board is the only thing that should post again.
	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-1")
		is.Comments = append(is.Comments, Comment{At: now(), Author: "lehel", Body: "deployed"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, f, false)
	if got := f.count("issue", "comment"); got != 2 {
		t.Fatalf("a new comment did not go up: %d total, want 2", got)
	}
}

func TestSyncMapsStatusToOpenAndClosed(t *testing.T) {
	s, f := syncFixture(t)
	mustSync(t, s, f, false)
	if f.count("issue", "close") != 0 {
		t.Fatal("nothing is done, nothing should have been closed")
	}

	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-1")
		is.Status = StatusDone
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, f, false)
	if f.count("issue", "close") != 1 {
		t.Error("finishing an issue did not close it on GitHub")
	}

	// ...and it stays closed rather than being closed again every run.
	mustSync(t, s, f, false)
	if f.count("issue", "close") != 1 {
		t.Error("an already-closed issue was closed twice")
	}

	// Reopening on the board reopens it there.
	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-1")
		is.Status = StatusDoing
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, f, false)
	if f.count("issue", "reopen") != 1 {
		t.Error("reopening on the board did not reopen it on GitHub")
	}
}

func TestSyncLabelsWorkInFlight(t *testing.T) {
	s, f := syncFixture(t)
	if err := s.Update(func(b *Board) error {
		is, _ := b.Get("TH-2")
		is.Status = StatusDoing
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, f, false)

	// open/closed cannot express "in flight", so a label has to.
	found := false
	for _, c := range f.calls {
		if c[0] == "issue" && c[1] == "create" && flagValue(c, "--title") == "Expose the ledger API" {
			for i, a := range c {
				if a == "--label" && c[i+1] == StatusDoing {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("an issue in doing was filed without the doing label")
	}
}

func TestSyncDryRunTouchesNothing(t *testing.T) {
	s, f := syncFixture(t)
	mustSync(t, s, f, true)

	for _, c := range f.calls {
		if c[0] == "issue" {
			t.Fatalf("--dry-run called gh: %v", c)
		}
	}
	b, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range b.Issues {
		if is.GitHub != nil {
			t.Errorf("--dry-run wrote a link onto %s", is.ID)
		}
	}
}
