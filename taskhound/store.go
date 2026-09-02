package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// StoreName is the file every command reads and writes. It is found by walking
// up from the working directory, the way git finds .git.
const StoreName = ".taskhound.yaml"

// The status set is deliberately tiny: a card is waiting, being worked on, or
// finished. "blocked" is not a status — it is derived from the dependency
// edges, so it can never drift out of sync with them.
const (
	StatusTodo  = "todo"
	StatusDoing = "doing"
	StatusDone  = "done"
)

var Statuses = []string{StatusTodo, StatusDoing, StatusDone}

func validStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

type Comment struct {
	At     time.Time `yaml:"at" json:"at"`
	Author string    `yaml:"author,omitempty" json:"author,omitempty"`
	Body   string    `yaml:"body" json:"body"`
}

type Issue struct {
	ID          string    `yaml:"id" json:"id"`
	Title       string    `yaml:"title" json:"title"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	Status      string    `yaml:"status" json:"status"`
	BlockedBy   []string  `yaml:"blocked_by,omitempty" json:"blocked_by"`
	Labels      []string  `yaml:"labels,omitempty" json:"labels,omitempty"`
	CreatedAt   time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at" json:"updated_at"`
	Comments    []Comment `yaml:"comments,omitempty" json:"comments,omitempty"`
}

// Board is the whole file. Edges live in exactly one place — each issue's
// BlockedBy — and the reverse direction is computed by Blocks.
type Board struct {
	Version int      `yaml:"version" json:"version"`
	Prefix  string   `yaml:"prefix" json:"prefix"`
	NextID  int      `yaml:"next_id" json:"next_id"`
	Issues  []*Issue `yaml:"issues" json:"issues"`
}

func NewBoard(prefix string) *Board {
	return &Board{Version: 1, Prefix: prefix, NextID: 1, Issues: []*Issue{}}
}

// NormalizeID accepts "7", "th-7" or "TH-7" and returns the canonical "TH-7".
func (b *Board) NormalizeID(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if n, err := strconv.Atoi(ref); err == nil {
		return fmt.Sprintf("%s-%d", b.Prefix, n)
	}
	if i := strings.LastIndex(ref, "-"); i > 0 {
		if n, err := strconv.Atoi(ref[i+1:]); err == nil {
			return fmt.Sprintf("%s-%d", strings.ToUpper(ref[:i]), n)
		}
	}
	return strings.ToUpper(ref)
}

func (b *Board) Get(ref string) (*Issue, error) {
	id := b.NormalizeID(ref)
	for _, is := range b.Issues {
		if is.ID == id {
			return is, nil
		}
	}
	return nil, fmt.Errorf("no such issue: %s", id)
}

func (b *Board) Add(title, description, status string, labels []string) *Issue {
	now := time.Now().UTC().Truncate(time.Second)
	is := &Issue{
		ID:          fmt.Sprintf("%s-%d", b.Prefix, b.NextID),
		Title:       title,
		Description: description,
		Status:      status,
		Labels:      labels,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	b.NextID++
	b.Issues = append(b.Issues, is)
	return is
}

// Blocks returns the issues that name id as a blocker — the reverse of
// BlockedBy, and the answer to "what is waiting on this?".
func (b *Board) Blocks(id string) []string {
	var out []string
	for _, is := range b.Issues {
		for _, dep := range is.BlockedBy {
			if dep == id {
				out = append(out, is.ID)
				break
			}
		}
	}
	sort.Sort(byIDNum{out, b.Prefix})
	return out
}

// Ready reports whether an issue can be started right now: not already done,
// and every issue blocking it is done.
func (b *Board) Ready(is *Issue) bool {
	if is.Status == StatusDone {
		return false
	}
	for _, dep := range is.BlockedBy {
		d, err := b.Get(dep)
		if err != nil || d.Status != StatusDone {
			return false
		}
	}
	return true
}

// OpenBlockers lists the blockers of is that are not done yet.
func (b *Board) OpenBlockers(is *Issue) []string {
	var out []string
	for _, dep := range is.BlockedBy {
		if d, err := b.Get(dep); err != nil || d.Status != StatusDone {
			out = append(out, dep)
		}
	}
	return out
}

// walk collects ids reachable from start along next, breadth first, excluding
// start itself. Used for both dependency directions.
func (b *Board) walk(start string, next func(string) []string) []string {
	seen := map[string]bool{start: true}
	queue := next(start)
	var out []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		queue = append(queue, next(id)...)
	}
	return out
}

// Deps returns everything start transitively waits on.
func (b *Board) Deps(start string) []string {
	return b.walk(start, func(id string) []string {
		is, err := b.Get(id)
		if err != nil {
			return nil
		}
		return is.BlockedBy
	})
}

// Dependents returns everything that transitively waits on start.
func (b *Board) Dependents(start string) []string {
	return b.walk(start, b.Blocks)
}

// SetBlockedBy replaces an issue's blockers, rejecting unknown ids, self-edges
// and anything that would close a cycle.
func (b *Board) SetBlockedBy(is *Issue, refs []string) error {
	var next []string
	seen := map[string]bool{}
	for _, ref := range refs {
		dep, err := b.Get(ref)
		if err != nil {
			return err
		}
		if dep.ID == is.ID {
			return fmt.Errorf("%s cannot block itself", is.ID)
		}
		if seen[dep.ID] {
			continue
		}
		seen[dep.ID] = true
		next = append(next, dep.ID)
	}
	prev := is.BlockedBy
	is.BlockedBy = next
	for _, dep := range next {
		// dep must not already depend on is, directly or indirectly.
		for _, reachable := range b.Deps(dep) {
			if reachable == is.ID {
				is.BlockedBy = prev
				return fmt.Errorf("%s → %s would create a dependency cycle", dep, is.ID)
			}
		}
	}
	return nil
}

// byIDNum sorts "TH-2" before "TH-10" instead of lexically.
type byIDNum struct {
	ids    []string
	prefix string
}

func (s byIDNum) Len() int      { return len(s.ids) }
func (s byIDNum) Swap(i, j int) { s.ids[i], s.ids[j] = s.ids[j], s.ids[i] }
func (s byIDNum) Less(i, j int) bool {
	ni, oki := idNum(s.ids[i])
	nj, okj := idNum(s.ids[j])
	if oki && okj {
		return ni < nj
	}
	return s.ids[i] < s.ids[j]
}

func idNum(id string) (int, bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	return n, err == nil
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// Store is one board file plus the sidecar lock that serialises access to it.
//
// The lock lives beside the file rather than on it because saving renames a
// temp file over the store: that swaps the inode, and a lock held on the old
// inode would guard nothing.
type Store struct {
	Path string
}

func (s *Store) lockPath() string { return s.Path + ".lock" }

// FindStore walks up from dir looking for the board file, returning the first
// one it finds.
func FindStore(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, StoreName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s here or in any parent directory (run `th init`)", StoreName)
		}
		dir = parent
	}
}

// lock takes an advisory flock on the sidecar and returns the release func.
func (s *Store) lock(exclusive bool) (func(), error) {
	f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", s.lockPath(), err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func (s *Store) readUnlocked() (*Board, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var b Board
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s is not valid taskhound YAML: %w", s.Path, err)
	}
	if b.Prefix == "" {
		b.Prefix = "TH"
	}
	if b.Issues == nil {
		b.Issues = []*Issue{}
	}
	return &b, nil
}

// Read returns a snapshot of the board under a shared lock.
func (s *Store) Read() (*Board, error) {
	unlock, err := s.lock(false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.readUnlocked()
}

// Update runs fn against the board while holding the exclusive lock, then saves
// the result atomically. Read and write happen inside the same lock, so two
// concurrent instances can never build on the same stale snapshot.
func (s *Store) Update(fn func(*Board) error) error {
	unlock, err := s.lock(true)
	if err != nil {
		return err
	}
	defer unlock()

	b, err := s.readUnlocked()
	if err != nil {
		return err
	}
	if err := fn(b); err != nil {
		return err
	}
	return s.saveUnlocked(b)
}

func (s *Store) saveUnlocked(b *Board) error {
	data, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".taskhound-*.tmp")
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
	return os.Rename(tmpName, s.Path)
}

// Create writes a fresh board, refusing to clobber an existing one.
func (s *Store) Create(prefix string) error {
	if _, err := os.Stat(s.Path); err == nil {
		return fmt.Errorf("%s already exists", s.Path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	unlock, err := s.lock(true)
	if err != nil {
		return err
	}
	defer unlock()
	return s.saveUnlocked(NewBoard(prefix))
}
