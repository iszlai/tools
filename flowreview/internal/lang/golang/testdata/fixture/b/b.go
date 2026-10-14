// Package b imports a, so it is a reverse importer of a's changed file.
package b

import "fixture.example/flowreviewtest/a"

func Bar() int { return a.Foo() + 1 }
