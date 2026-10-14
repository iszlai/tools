// Package generics exercises FR-14's symbol construction for a generic
// function and the methods of a generic type.
package generics

// Max returns the larger of two values of an ordered type. A generic
// top-level function: its ID has no receiver; its Signature carries the type
// parameter.
func Max[T int | float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Stack is a generic type with one type parameter.
type Stack[T any] struct {
	items []T
}

// Push adds an item. A method of a generic type with a pointer receiver:
// Receiver should be "*Stack" (the type parameter is dropped from the
// receiver's bare name); ID "...generics.(Stack).Push".
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop removes and returns the last item, and whether there was one.
func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, true
}
