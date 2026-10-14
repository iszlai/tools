package render

import (
	"strings"
	"sync"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Cache memoizes File's rows per file, keyed by the file's id plus its
// hunks' ids. A hunk id is a content hash (model.HunkID), so a file whose
// diff changed since it was last rendered -- edited further, or refreshed
// onto a new diff -- naturally misses rather than serving stale HTML. Never
// evicted: one flowreview session reviews one diff, so the key set is
// bounded by that diff's files.
type Cache struct {
	mu   sync.Mutex
	rows map[string][]Row
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{rows: make(map[string][]Row)}
}

// Rows returns f's rendered rows, computing and caching them on first ask so
// a later ask for the same file and content is near-instant.
func (c *Cache) Rows(f model.ChangedFile) []Row {
	key := cacheKey(f)

	c.mu.Lock()
	if rows, ok := c.rows[key]; ok {
		c.mu.Unlock()
		return rows
	}
	c.mu.Unlock()

	rows := File(f)

	c.mu.Lock()
	c.rows[key] = rows
	c.mu.Unlock()
	return rows
}

func cacheKey(f model.ChangedFile) string {
	var b strings.Builder
	b.WriteString(f.ID)
	for _, h := range f.Hunks {
		b.WriteByte('|')
		b.WriteString(h.ID)
	}
	return b.String()
}
