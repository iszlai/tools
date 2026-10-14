package history

import (
	"fmt"
	"sort"
)

// Companion is a file that is not part of the current diff but usually
// changes together with one that is.
type Companion struct {
	Path       string  // the file not in the diff
	Of         string  // the diff file it usually changes with
	Support    int     // commits that touched both Path and Of
	Confidence float64 // Support / commits that touched Of
}

// Reason is a plain-English explanation of why c was surfaced, e.g. "usually
// changes with handler.go, not in this diff". The UI decides whether and how
// to show it next to a file.
func (c Companion) Reason() string {
	return fmt.Sprintf("usually changes with %s, not in this diff", c.Of)
}

// Companions returns files not in diffPaths that usually change together with
// one that is, sorted by confidence descending. A file reachable from more
// than one diff file is reported once, via its highest-confidence pairing.
// Companions is only meaningful after Mine has succeeded; before that it
// returns nil.
func (m *Miner) Companions(diffPaths []string) []Companion {
	if m.snap == nil {
		return nil
	}
	inDiff := make(map[string]bool, len(diffPaths))
	for _, p := range diffPaths {
		inDiff[p] = true
	}

	best := make(map[string]Companion)
	for _, a := range diffPaths {
		fs, ok := m.snap.Files[a]
		if !ok || fs.Commits == 0 {
			continue
		}
		for _, pl := range m.snap.byFile[a] {
			if inDiff[pl.other] || pl.support < m.opts.MinSupport {
				continue
			}
			conf := float64(pl.support) / float64(fs.Commits)
			if conf < m.opts.MinConfidence {
				continue
			}
			if cur, ok := best[pl.other]; !ok || conf > cur.Confidence {
				best[pl.other] = Companion{Path: pl.other, Of: a, Support: pl.support, Confidence: conf}
			}
		}
	}

	out := make([]Companion, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Path < out[j].Path // deterministic tie-break
	})
	return out
}
