package history

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// loadCache reads a cached snapshot from path and reports whether it still
// answers opts: a snapshot mined with a different Since, MaxCommits or
// MaxFilesPerCommit covers a different window of history, so it is treated as
// a miss and re-mined. MinSupport/MinConfidence don't affect what was mined
// (Companions applies them at query time), so changing those alone still
// hits the cache.
func loadCache(path string, opts Options) (*snapshot, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false
	}
	if s.Since != opts.Since || s.MaxCommits != opts.MaxCommits || s.MaxFilesPerCommit != opts.MaxFilesPerCommit {
		return nil, false
	}
	return &s, true
}

// saveCache writes snap to path as JSON, creating its directory if needed.
// This is a regenerable derived cache, not source-of-truth data (unlike
// internal/core/session), so a plain write is enough: no flock, no
// temp-file-plus-rename, and a losing concurrent writer just means the next
// Mine call re-derives the same answer from git.
func saveCache(path string, snap *snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
