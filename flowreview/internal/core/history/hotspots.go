package history

// Score is a file's change frequency and churn, normalized against the rest
// of the mined repository.
type Score struct {
	Commits int     // commits that touched the file in the mined window
	Churn   int     // lines added + deleted across those commits
	Score   float64 // log-scaled commits x churn, normalized 0..1 across the repo
}

// Hotspot scores path by how often, and how heavily, it changes relative to
// every other file seen while mining. A path never touched in the mined
// window scores zero. Hotspot is only meaningful after Mine has succeeded;
// before that it also returns the zero Score.
func (m *Miner) Hotspot(path string) Score {
	if m.snap == nil {
		return Score{}
	}
	fs := m.snap.Files[path]
	churn := fs.Added + fs.Deleted
	return Score{
		Commits: fs.Commits,
		Churn:   churn,
		Score:   m.snap.normalize(rawScore(fs)),
	}
}
