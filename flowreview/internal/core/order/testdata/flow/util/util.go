// Package util is the fixture's "unrelated changed util": nothing in the
// module calls helper and it calls nothing back into handler/service/repo,
// so flow ordering should give it its own, entry-point-less group. helper
// is deliberately unexported -- an exported no-callers function would
// itself match golang's "unreferenced" entry-point heuristic, which is a
// different case this fixture does not mean to test.
package util

// helper is changed and isolated.
func helper() int {
	return 42
}
