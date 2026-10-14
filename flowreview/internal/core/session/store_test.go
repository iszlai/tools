package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func newSession(repoKey, id string) *model.ReviewSession {
	now := time.Now().UTC()
	return &model.ReviewSession{
		ID:        id,
		RepoKey:   repoKey,
		Title:     "test session",
		Source:    model.Source{Kind: model.SourceWorktree, RepoPath: "/repo"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestCreateGet(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sess1" || got.Title != "test session" {
		t.Errorf("got %+v", got)
	}
}

func TestCreateRefusesClobber(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(sess); err == nil {
		t.Fatal("want error creating over an existing session")
	}
}

func TestGetMissing(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.Get("repo1", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListAndListAll(t *testing.T) {
	s := New(t.TempDir())
	for _, rk := range []string{"repo1", "repo2"} {
		for i := 0; i < 2; i++ {
			sess := newSession(rk, fmt.Sprintf("sess%d", i))
			sess.UpdatedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
			if err := s.Create(sess); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := s.List("repo1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if got[0].UpdatedAt.Before(got[1].UpdatedAt) {
		t.Error("List must be most-recently-updated first")
	}

	all, err := s.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 sessions across both repos, got %d", len(all))
	}
}

func TestListEmptyRepoKey(t *testing.T) {
	s := New(t.TempDir())
	got, err := s.List("never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestDelete(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("repo1", "sess1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("repo1", "sess1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
	// Deleting again is not an error.
	if err := s.Delete("repo1", "sess1"); err != nil {
		t.Fatalf("deleting a gone session must not error: %v", err)
	}
}

func TestUpdateReadModifyWrite(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}

	err = s.Update("repo1", "sess1", func(sess *model.ReviewSession) error {
		sess.Title = "changed"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "changed" {
		t.Errorf("want title changed, got %q", after.Title)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Error("UpdatedAt should advance on Update")
	}
}

func TestUpdateFnErrorLeavesFileUnchanged(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("boom")
	err := s.Update("repo1", "sess1", func(sess *model.ReviewSession) error {
		sess.Title = "should not stick"
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want wrapped boom error, got %v", err)
	}
	got, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title == "should not stick" {
		t.Error("a failed Update must not persist the mutation")
	}
}

// TestAtomicWriteLeavesNoTempFiles writes many times and checks the directory
// holds only the session file and its lock sidecar afterwards -- no
// ".flowreview-*.tmp" litter.
func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		err := s.Update("repo1", "sess1", func(sess *model.ReviewSession) error {
			sess.Title = fmt.Sprintf("title-%d", i)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, "sessions", "repo1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"sess1.json": true, "sess1.json.lock": true}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}

// TestConcurrentUpdates runs 16 goroutines, each with its own Store pointed
// at the same root, all updating the same session at once. Flock must
// serialize them so every one of the 16 writes lands -- a broken lock would
// lose some of them to a read-modify-write race.
func TestConcurrentUpdates(t *testing.T) {
	root := t.TempDir()
	setup := New(root)
	sess := newSession("repo1", "sess1")
	if err := setup.Create(sess); err != nil {
		t.Fatal(err)
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := New(root) // its own Store instance, same root
			_, err := store.UpsertComment("repo1", "sess1", model.CommentDraft{
				Body: fmt.Sprintf("comment-%d", i),
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	got, err := New(root).Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != n {
		t.Fatalf("want %d comments landed, got %d", n, len(got.Comments))
	}
	seen := map[string]bool{}
	for _, c := range got.Comments {
		if seen[c.ID] {
			t.Errorf("duplicate comment id %s", c.ID)
		}
		seen[c.ID] = true
		if c.ID == "" {
			t.Error("comment must have an assigned id")
		}
	}
}
