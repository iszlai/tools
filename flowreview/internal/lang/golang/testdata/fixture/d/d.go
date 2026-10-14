// Package d exists only to carry a _test.go file, unrelated to a/b/broken,
// so a Load of a changed _test.go file is exercised on its own.
package d

func Qux() int { return 7 }
