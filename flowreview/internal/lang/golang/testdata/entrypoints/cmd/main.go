// Package main is the fixture's program entry point: covers the "main" and
// "init" FR-16 kinds.
package main

func main() {
	run()
}

func init() {
	// No-op; exists only so init is a decl FR-16 can find.
}

func run() {}
