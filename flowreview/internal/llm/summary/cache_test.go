package summary

import (
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestCacheMemoryOnly(t *testing.T) {
	c := NewCache("")
	if _, ok := c.Get("missing"); ok {
		t.Fatal("empty cache must miss")
	}
	s := model.SnippetSummary{Key: "k1", HunkID: "h1", What: "did a thing", Kind: "behavior", CreatedAt: time.Now().UTC()}
	if err := c.Put(s); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("want a hit after Put")
	}
	if got.What != s.What {
		t.Errorf("What = %q, want %q", got.What, s.What)
	}
}

func TestCacheDiskRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	s := model.SnippetSummary{Key: "k2", HunkID: "h2", What: "did another thing", Kind: "refactor", CreatedAt: time.Now().UTC()}
	if err := c.Put(s); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A fresh Cache over the same dir, with an empty in-memory layer, must
	// still find it on disk.
	c2 := NewCache(dir)
	got, ok := c2.Get("k2")
	if !ok {
		t.Fatal("want a disk hit from a fresh Cache")
	}
	if got.What != s.What || got.Kind != s.Kind {
		t.Errorf("got %+v, want What/Kind to match %+v", got, s)
	}
}

func TestCacheMissingKey(t *testing.T) {
	c := NewCache(t.TempDir())
	if _, ok := c.Get("nope"); ok {
		t.Fatal("want a miss for a key never Put")
	}
}

// TestCachePromptVersionBump checks the cache-invalidation story end to end:
// SummaryKey already folds PromptVersion in (see model.SummaryKey's own
// tests), so bumping it naturally produces a different key that misses an
// old entry -- nothing in Cache itself needs to know about versions.
func TestCachePromptVersionBump(t *testing.T) {
	c := NewCache(t.TempDir())
	keyV1 := model.SummaryKey("m", "v1", "body", "func", "")
	keyV2 := model.SummaryKey("m", "v2", "body", "func", "")
	if keyV1 == keyV2 {
		t.Fatal("different prompt versions must produce different keys")
	}
	if err := c.Put(model.SnippetSummary{Key: keyV1, What: "old prompt's answer"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := c.Get(keyV2); ok {
		t.Error("a bumped prompt version must not see the old entry")
	}
}
