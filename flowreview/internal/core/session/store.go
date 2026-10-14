// Package session persists review sessions to disk: one JSON file per
// session under a data root, at sessions/<repo-key>/<session-id>.json. Writes
// are atomic (temp file + fsync + rename) and serialized per file by a
// sidecar "<file>.lock" held with flock, the same approach taskhound uses for
// its board file.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("session: not found")

// Store is sessions persisted under Root.
type Store struct {
	Root string
}

// New returns a Store rooted at root. Tests pass t.TempDir(); production code
// passes DefaultRoot().
func New(root string) *Store {
	return &Store{Root: root}
}

// DefaultRoot is the data root flowreview uses unless overridden:
// $FLOWREVIEW_HOME when set (tests and alternate installs use this), otherwise
// ~/Library/Application Support/FlowReview.
func DefaultRoot() string {
	if h := os.Getenv("FLOWREVIEW_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Library", "Application Support", "FlowReview")
}

func (s *Store) repoDir(repoKey string) string {
	return filepath.Join(s.Root, "sessions", repoKey)
}

func (s *Store) path(repoKey, id string) string {
	return filepath.Join(s.repoDir(repoKey), id+".json")
}

// lock takes an advisory flock on path's sidecar and returns the release
// func. It creates path's directory if needed, so a lock can be taken before
// the file it guards exists (Get and Create both need that).
func lock(path string, exclusive bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s.lock: %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// writeAtomic renders data to path by writing a temp file in the same
// directory, fsyncing it, then renaming it into place, so a concurrent reader
// always sees either the whole old file or the whole new one and no partial
// write is ever observable. The temp file is always cleaned up, including on
// the success path (os.Rename moves it, so the deferred Remove then finds
// nothing there).
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".flowreview-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readSession(path string) (*model.ReviewSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var sess model.ReviewSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("%s is not valid session JSON: %w", path, err)
	}
	return &sess, nil
}

func writeSession(path string, sess *model.ReviewSession) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// Create writes a new session, refusing to clobber an existing one.
func (s *Store) Create(sess *model.ReviewSession) error {
	if sess.RepoKey == "" || sess.ID == "" {
		return fmt.Errorf("session: RepoKey and ID are required")
	}
	path := s.path(sess.RepoKey, sess.ID)
	unlock, err := lock(path, true)
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("session: %s/%s already exists", sess.RepoKey, sess.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeSession(path, sess)
}

// Get reads one session.
func (s *Store) Get(repoKey, id string) (*model.ReviewSession, error) {
	path := s.path(repoKey, id)
	unlock, err := lock(path, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return readSession(path)
}

// List returns every session for repoKey, most recently updated first. A
// repoKey with no sessions yet returns an empty slice, not an error.
func (s *Store) List(repoKey string) ([]*model.ReviewSession, error) {
	entries, err := os.ReadDir(s.repoDir(repoKey))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*model.ReviewSession
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		sess, err := s.Get(repoKey, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // removed between the readdir and the get
			}
			return nil, err
		}
		out = append(out, sess)
	}
	sortByUpdated(out)
	return out, nil
}

// ListAll returns every session across every repo, most recently updated
// first.
func (s *Store) ListAll() ([]*model.ReviewSession, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "sessions"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*model.ReviewSession
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessions, err := s.List(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, sessions...)
	}
	sortByUpdated(out)
	return out, nil
}

func sortByUpdated(sessions []*model.ReviewSession) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

// Delete removes a session. Deleting one that is already gone is not an
// error.
func (s *Store) Delete(repoKey, id string) error {
	path := s.path(repoKey, id)
	unlock, err := lock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Update runs fn against the current session under an exclusive lock, then
// saves the result atomically. The read and the modification happen inside
// the same lock, so two concurrent writers can never both build their change
// on the same stale snapshot -- the second one always sees the first one's
// write.
func (s *Store) Update(repoKey, id string, fn func(*model.ReviewSession) error) error {
	path := s.path(repoKey, id)
	unlock, err := lock(path, true)
	if err != nil {
		return err
	}
	defer unlock()

	sess, err := readSession(path)
	if err != nil {
		return err
	}
	if err := fn(sess); err != nil {
		return err
	}
	sess.UpdatedAt = time.Now().UTC()
	return writeSession(path, sess)
}
