// Package generic exercises FR-15's generic-instantiation attribution: Max is
// called with two different type arguments from two call sites, and both
// edges must resolve to the one generic origin "generic.Max", never to two
// separate per-instantiation symbols.
package generic

// Max returns the larger of two ordered values.
func Max[T int | string](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// CallInt instantiates Max with int.
func CallInt() int { return Max(1, 2) }

// CallString instantiates Max with string.
func CallString() string { return Max("a", "b") }
