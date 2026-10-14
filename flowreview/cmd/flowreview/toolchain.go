package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/lang/toolchain"
)

const toolchainUsage = `usage: flowreview toolchain <command>

commands:
  list       show installed Go toolchains
  rm <v>     remove an installed toolchain (e.g. "1.22.3")
`

// toolchainRoot matches PLAN.md section 3's data layout: bin/go/<version>/
// under the same data root sessions and models live under.
func toolchainRoot() string {
	return filepath.Join(session.DefaultRoot(), "bin", "go")
}

// runToolchainCLI implements `flowreview toolchain list|rm <v>`.
func runToolchainCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, toolchainUsage)
		return 2
	}
	mgr := &toolchain.Manager{Root: toolchainRoot()}

	switch args[0] {
	case "list":
		installed, err := mgr.List()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(installed) == 0 {
			fmt.Fprintln(stdout, "no toolchains installed")
			return 0
		}
		for _, v := range installed {
			fmt.Fprintf(stdout, "%-10s %s\n", v.Version, v.GOROOT)
		}
		return 0

	case "rm":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "flowreview toolchain rm: a version is required")
			return 2
		}
		if err := mgr.Remove(args[1]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", args[1])
		return 0

	default:
		fmt.Fprintf(stderr, "flowreview toolchain: unknown command %q\n\n%s", args[0], toolchainUsage)
		return 2
	}
}
