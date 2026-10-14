package summary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Cache persists summaries by model.SummaryKey under Dir, one file per key
// (PLAN.md §3: "cache/summaries/<hash>.json"), with an in-memory layer in
// front so a hot key never round-trips through disk twice. Since the key
// already folds in PromptVersion, bumping that constant invalidates every
// cached entry simply by no longer matching any key a fresh request computes
// -- old files are never read again, and are harmless left on disk.
type Cache struct {
	Dir string // "" keeps everything in memory only, e.g. for tests

	mu  sync.RWMutex
	mem map[string]model.SnippetSummary
}

// NewCache returns a Cache persisting to dir (created on first write).
func NewCache(dir string) *Cache {
	return &Cache{Dir: dir, mem: make(map[string]model.SnippetSummary)}
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.Dir, key+".json")
}

// Get returns the cached summary for key, checking memory first and then
// disk (populating memory on a disk hit, so the next Get for the same key
// skips the file read).
func (c *Cache) Get(key string) (model.SnippetSummary, bool) {
	c.mu.RLock()
	s, ok := c.mem[key]
	c.mu.RUnlock()
	if ok {
		return s, true
	}
	if c.Dir == "" {
		return model.SnippetSummary{}, false
	}

	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return model.SnippetSummary{}, false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return model.SnippetSummary{}, false
	}
	c.mu.Lock()
	c.mem[key] = s
	c.mu.Unlock()
	return s, true
}

// Remember stores s in memory only: for this process's lifetime, never on
// disk.
func (c *Cache) Remember(s model.SnippetSummary) {
	c.mu.Lock()
	c.mem[s.Key] = s
	c.mu.Unlock()
}

// Put stores s under s.Key, in memory always and on disk when Dir is set.
// The disk write is atomic (temp file + rename), matching
// internal/llm/models.SaveConfig's pattern, so a crash mid-write never leaves
// a truncated cache entry for a later Get to choke on.
func (c *Cache) Put(s model.SnippetSummary) error {
	c.mu.Lock()
	c.mem[s.Key] = s
	c.mu.Unlock()
	if c.Dir == "" {
		return nil
	}

	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, ".summary-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, c.path(s.Key))
}
