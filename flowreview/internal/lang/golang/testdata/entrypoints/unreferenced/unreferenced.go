// Package unreferenced covers FR-16's optional "exported, no in-module
// callers" heuristic (cfg.HasCallers).
package unreferenced

// Exported has no in-module caller (per the test's stub HasCallers) and is
// exported, so it should be flagged when cfg.HasCallers is set.
func Exported() {}

// unexported also has no caller, but is never flagged: the heuristic only
// applies to exported symbols.
func unexported() {}
