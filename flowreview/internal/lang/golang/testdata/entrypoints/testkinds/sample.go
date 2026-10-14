// Package testkinds covers FR-16's Test*/Benchmark*/Example*/Fuzz* heuristic
// — see sample_test.go.
package testkinds

// Double is exercised by the tests in sample_test.go.
func Double(n int) int { return n * 2 }
