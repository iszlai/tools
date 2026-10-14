// Package history mines a repository's commit log for two signals: which
// files tend to change together ("co-change companions") and which files
// change most, and most heavily ("hotspots"). Both come from one pass over
// `git log --numstat`, shelled out directly rather than through
// internal/core/git (FR-2's Repo keeps its git invocation private), cached to
// disk per repo+HEAD so a second Mine for an unchanged HEAD costs no git call
// at all.
package history

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/git"
)

// Options tunes how much history Mine reads and the thresholds Companions
// applies. The zero value selects the defaults below.
type Options struct {
	Since             time.Duration // how far back to mine; zero means 365 days
	MaxCommits        int           // cap on commits scanned; zero means 5000
	MinSupport        int           // minimum co-change count to report a companion; zero means 5
	MinConfidence     float64       // minimum confidence to report a companion; zero means 0.5
	MaxFilesPerCommit int           // commits touching more files than this are skipped as mechanical; zero means 50
}

const (
	defaultSince             = 365 * 24 * time.Hour
	defaultMaxCommits        = 5000
	defaultMinSupport        = 5
	defaultMinConfidence     = 0.5
	defaultMaxFilesPerCommit = 50
)

func (o Options) withDefaults() Options {
	if o.Since <= 0 {
		o.Since = defaultSince
	}
	if o.MaxCommits <= 0 {
		o.MaxCommits = defaultMaxCommits
	}
	if o.MinSupport <= 0 {
		o.MinSupport = defaultMinSupport
	}
	if o.MinConfidence <= 0 {
		o.MinConfidence = defaultMinConfidence
	}
	if o.MaxFilesPerCommit <= 0 {
		o.MaxFilesPerCommit = defaultMaxFilesPerCommit
	}
	return o
}

// Miner mines co-change and hotspot statistics for one repository. Create one
// with New, call Mine once per HEAD you care about, then query Companions and
// Hotspot as needed; both read the snapshot Mine produced, with no further
// git invocation.
type Miner struct {
	repo     *git.Repo
	cacheDir string
	opts     Options

	snap *snapshot // nil until Mine succeeds
}

// New returns a Miner for repo. cacheDir is where mined snapshots are cached
// as JSON, one file per repo+HEAD; pass "" to always mine fresh.
func New(repo *git.Repo, cacheDir string, opts Options) *Miner {
	return &Miner{repo: repo, cacheDir: cacheDir, opts: opts.withDefaults()}
}

// Stats summarizes one Mine call.
type Stats struct {
	HeadSHA  string
	Commits  int    // commits actually scanned, after the MaxCommits cap
	Degraded string // e.g. "capped at 5000 commits"; empty when the full window was mined
}

// Mine loads the cached snapshot for headSHA, or mines fresh history and
// caches it when no valid cache exists. headSHA is supplied by the caller
// (typically git.Repo.HeadSHA) rather than resolved here, so a cache hit
// needs no git invocation whatsoever.
func (m *Miner) Mine(ctx context.Context, headSHA string) (Stats, error) {
	if headSHA == "" {
		return Stats{}, fmt.Errorf("history: Mine: headSHA is required")
	}

	path := m.cachePath(headSHA)
	if path != "" {
		if snap, ok := loadCache(path, m.opts); ok {
			snap.buildIndex()
			m.snap = snap
			return snap.stats(), nil
		}
	}

	snap, err := mine(ctx, m.repo, m.opts)
	if err != nil {
		return Stats{}, err
	}
	snap.HeadSHA = headSHA
	snap.buildIndex()
	m.snap = snap

	if path != "" {
		if err := saveCache(path, snap); err != nil {
			return Stats{}, fmt.Errorf("history: cache %s: %w", path, err)
		}
	}
	return snap.stats(), nil
}

// cachePath is where the snapshot for headSHA lives under cacheDir, namespaced
// by repo so one cacheDir can safely serve more than one repository.
func (m *Miner) cachePath(headSHA string) string {
	if m.cacheDir == "" {
		return ""
	}
	return filepath.Join(m.cacheDir, repoTag(m.repo.Root())+"-"+headSHA+".json")
}

func repoTag(root string) string {
	sum := sha1.Sum([]byte(root))
	return hex.EncodeToString(sum[:])[:16]
}
