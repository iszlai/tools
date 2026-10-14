package history

import (
	"math"
	"time"
)

// snapshot is the mined co-change and hotspot data for one repository at one
// HEAD. It is the exact shape cached to disk as JSON: Companions and Hotspot
// read Files and Pairs at query time, so a cached snapshot keeps answering
// correctly even if a Miner's thresholds (MinSupport, MinConfidence) change
// between calls -- only Since, MaxCommits and MaxFilesPerCommit change what
// was mined, so only those invalidate a cache hit (see loadCache).
type snapshot struct {
	HeadSHA           string               `json:"head_sha"`
	Commits           int                  `json:"commits"`
	Degraded          string               `json:"degraded,omitempty"`
	Since             time.Duration        `json:"since"`
	MaxCommits        int                  `json:"max_commits"`
	MaxFilesPerCommit int                  `json:"max_files_per_commit"`
	Files             map[string]FileStats `json:"files"`
	Pairs             []PairStats          `json:"pairs"`

	// Derived at load/mine time, never serialized (unexported fields are
	// skipped by encoding/json automatically).
	byFile   map[string][]pairLookup
	lo, hi   float64
	rangeSet bool
}

// FileStats is one file's raw activity across the mined commits.
type FileStats struct {
	Commits int `json:"commits"`
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// PairStats is how often two files changed in the same commit. A and B are
// stored in the order they came out of mining (sorted, so each unordered pair
// appears once); byFile indexes both directions for lookup.
type PairStats struct {
	A       string `json:"a"`
	B       string `json:"b"`
	Support int    `json:"support"`
}

type pairLookup struct {
	other   string
	support int
}

func (s *snapshot) stats() Stats {
	return Stats{HeadSHA: s.HeadSHA, Commits: s.Commits, Degraded: s.Degraded}
}

// buildIndex populates byFile from Pairs so Companions can look up "what
// changes with path" in O(1) instead of scanning every pair.
func (s *snapshot) buildIndex() {
	s.byFile = make(map[string][]pairLookup, len(s.Files))
	for _, p := range s.Pairs {
		s.byFile[p.A] = append(s.byFile[p.A], pairLookup{other: p.B, support: p.Support})
		s.byFile[p.B] = append(s.byFile[p.B], pairLookup{other: p.A, support: p.Support})
	}
}

// normalize maps a raw hotspot score into 0..1 against the range seen across
// every file in this snapshot, computed once and cached.
func (s *snapshot) normalize(raw float64) float64 {
	lo, hi := s.scoreRange()
	if hi <= lo {
		return 0
	}
	v := (raw - lo) / (hi - lo)
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func (s *snapshot) scoreRange() (float64, float64) {
	if s.rangeSet {
		return s.lo, s.hi
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, fs := range s.Files {
		raw := rawScore(fs)
		if raw < lo {
			lo = raw
		}
		if raw > hi {
			hi = raw
		}
	}
	if math.IsInf(lo, 1) {
		lo, hi = 0, 0
	}
	s.lo, s.hi, s.rangeSet = lo, hi, true
	return lo, hi
}

// rawScore is commits x churn, log-scaled so a handful of huge commits does
// not drown out files that change often in small amounts.
func rawScore(fs FileStats) float64 {
	return math.Log1p(float64(fs.Commits)) * math.Log1p(float64(fs.Added+fs.Deleted))
}
