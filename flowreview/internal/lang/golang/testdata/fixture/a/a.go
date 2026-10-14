// Package a is the package the fixture's "changed file" lives in.
package a

// Foo is called by package b, so b is a's reverse importer.
func Foo() int { return 42 }
