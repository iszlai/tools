// Package changed is a small fixture for FR-14's ChangedSymbolsDetailed:
// symbols_test.go diffs this file's content (the "new" side) against an
// older revision supplied inline in the test (the "old" side).
package changed

// Kept never changes between the old and new fixture content; it must never
// show up as a Change.
func Kept() int { return 1 }

// Modified keeps its name and signature but changes its body between the old
// and new fixture content.
func Modified(x int) int {
	return x + 1
}

// Resig changes its signature, not just its body, between the old and new
// fixture content.
func Resig(x int) (int, error) {
	return x, nil
}

// Added exists only in the new fixture content.
func Added() string { return "new" }
