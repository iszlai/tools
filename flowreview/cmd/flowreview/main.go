// Command flowreview reviews a local diff or a pull request in code-flow
// order, in its own window.
package main

import (
	"fmt"
	"os"
)

// version is stamped by the Makefile.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("flowreview", version)
		return
	}
	fmt.Fprintln(os.Stderr, "flowreview: not wired up yet; see PLAN.md")
	os.Exit(2)
}
