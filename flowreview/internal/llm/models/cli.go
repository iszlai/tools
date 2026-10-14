package models

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultDataRoot is the data root flowreview uses unless overridden:
// $FLOWREVIEW_HOME when set (tests and alternate installs use this),
// otherwise ~/Library/Application Support/FlowReview. It matches
// internal/core/session.DefaultRoot — models/ and config.json sit next to
// sessions/ under the same root (PLAN.md §3).
func DefaultDataRoot() string {
	if h := os.Getenv("FLOWREVIEW_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Library", "Application Support", "FlowReview")
}

const cliUsage = `usage: flowreview model <command>

commands:
  list            show the catalog and what is installed
  install <name>  download a model (asks for confirmation unless --yes)
  rm <name>       remove an installed model
  status          show installed models and the configured choice
`

// RunCLI implements `flowreview model list|install <name>|rm <name>|status`.
// It is exported so the CLI slice (FR-12+) can mount it as a subcommand; it
// resolves its own data root the same way the rest of flowreview does.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, cliUsage)
		return 2
	}

	root := DefaultDataRoot()
	mgr := &Manager{Root: filepath.Join(root, "models")}

	switch args[0] {
	case "list":
		return runList(mgr, stdout)
	case "install":
		return runInstall(mgr, root, args[1:], stdout, stderr)
	case "rm":
		return runRemove(mgr, args[1:], stdout, stderr)
	case "status":
		return runStatus(mgr, root, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "flowreview model: unknown command %q\n\n%s", args[0], cliUsage)
		return 2
	}
}

func runList(mgr *Manager, stdout io.Writer) int {
	for _, s := range mgr.List() {
		mark := " "
		if s.Installed {
			mark = "*"
		}
		fmt.Fprintf(stdout, "%s %-38s %-7s %6.1f GB  %s\n", mark, s.Entry.Name, s.Entry.Tier, float64(s.Entry.Size)/gb, s.Entry.Licence)
	}
	return 0
}

func runInstall(mgr *Manager, dataRoot string, args []string, stdout, stderr io.Writer) int {
	yes := false
	var name string
	for _, a := range args {
		if a == "--yes" {
			yes = true
			continue
		}
		name = a
	}
	if name == "" {
		fmt.Fprintln(stderr, "flowreview model install: a model name is required")
		return 2
	}

	mgr.Confirm = func(d Disclosure) bool {
		fmt.Fprintf(stdout, "about to download %s\n", d.Name)
		fmt.Fprintf(stdout, "  size:    %.1f GB\n", float64(d.Size)/gb)
		fmt.Fprintf(stdout, "  licence: %s (%s)\n", d.Licence, d.LicenceURL)
		fmt.Fprintf(stdout, "  needs:   %d GB RAM\n", d.MinRAMGB)
		fmt.Fprintf(stdout, "  source:  %s\n", d.URL)
		if yes {
			return true
		}
		fmt.Fprint(stdout, "proceed? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.EqualFold(strings.TrimSpace(answer), "y")
	}

	var lastPct = -1
	err := mgr.Install(context.Background(), name, func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct != lastPct {
			fmt.Fprintf(stdout, "\r%s: %3d%%", name, pct)
			lastPct = pct
		}
	})
	fmt.Fprintln(stdout)
	if err != nil {
		if err == ErrDeclined {
			fmt.Fprintln(stdout, "declined")
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s\n", name)
	return 0
}

func runRemove(mgr *Manager, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "flowreview model rm: a model name is required")
		return 2
	}
	if err := mgr.Remove(args[0]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s\n", args[0])
	return 0
}

func runStatus(mgr *Manager, dataRoot string, stdout, stderr io.Writer) int {
	cfg, err := LoadConfig(dataRoot)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	installed := mgr.Status()
	if len(installed) == 0 {
		fmt.Fprintln(stdout, "no models installed")
	}
	for _, s := range installed {
		mark := " "
		if cfg.Enabled && cfg.Model == s.Entry.Name {
			mark = "*"
		}
		fmt.Fprintf(stdout, "%s %-38s %-7s %6.1f GB\n", mark, s.Entry.Name, s.Entry.Tier, float64(s.SizeBytes)/gb)
	}
	if !cfg.Enabled {
		fmt.Fprintln(stdout, "summaries: off")
	} else if cfg.Model == "" {
		fmt.Fprintln(stdout, "summaries: on, no model configured")
	} else {
		fmt.Fprintf(stdout, "summaries: on, using %s\n", cfg.Model)
	}
	return 0
}
