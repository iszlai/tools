// Package lib is dot-imported by package user, to exercise FR-14's
// Definition lookup through a dot import.
package lib

// Widget returns a constant, just so there is something to jump to.
func Widget() int { return 7 }
