// Package c imports nothing from a or b: it must stay out of the scope
// loaded for a change in a.
package c

func Baz() string { return "unrelated" }
