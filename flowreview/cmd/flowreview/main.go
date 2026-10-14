// Command flowreview reviews a local diff or a pull request in code-flow
// order, in its own window.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/model"
	"github.com/iszlai/tools/flowreview/internal/web"
	"github.com/iszlai/tools/flowreview/internal/window"
)

// version is stamped by the Makefile.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("flowreview", version)
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "flowreview:", err)
		os.Exit(1)
	}
}

// options are the parsed flags and the source every invocation reviews.
// FR-12 grows the CLI surface (sessions list, pr, --json); this covers only
// what M1's local sources need.
type options struct {
	source  model.Source
	port    int
	browser bool
	noOpen  bool
}

// parseArgs recognizes "flowreview", "flowreview diff <ref|range>" and
// "flowreview --patch <file>", each with --port/--browser/--no-open. The
// "diff" subcommand must come first, before any flags; FR-12 is where a
// fuller argument parser belongs.
func parseArgs(args []string) (options, error) {
	var opts options
	rest := args

	if len(rest) > 0 && rest[0] == "diff" {
		if len(rest) < 2 {
			return opts, errors.New("usage: flowreview diff <ref|range>")
		}
		ref := rest[1]
		rest = rest[2:]
		if strings.Contains(ref, "..") {
			opts.source = model.Source{Kind: model.SourceRange, Ref: ref}
		} else {
			opts.source = model.Source{Kind: model.SourceCommit, Ref: ref}
		}
	}

	fs := flag.NewFlagSet("flowreview", flag.ContinueOnError)
	patch := fs.String("patch", "", "review a patch file instead of a repo diff")
	fs.IntVar(&opts.port, "port", 0, "HTTP port to serve on (0 picks a free one)")
	fs.BoolVar(&opts.browser, "browser", false, "open the default browser instead of the native window (Playwright smoke only)")
	fs.BoolVar(&opts.noOpen, "no-open", false, "serve only: print the URL on stdout and open nothing")
	if err := fs.Parse(rest); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	if *patch != "" {
		if opts.source.Kind != "" {
			return opts, errors.New("--patch cannot be combined with 'diff'")
		}
		opts.source = model.Source{Kind: model.SourcePatch, PatchPath: *patch}
	}
	if opts.source.Kind == "" {
		opts.source = model.Source{Kind: model.SourceWorktree}
	}
	return opts, nil
}

func run(args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	// Resolve RepoPath against this process's cwd now, while it is still
	// this process's: a second invocation sends its source to a different,
	// already-running process over /api/open, and an empty RepoPath would
	// resolve against that other process's cwd instead (even for a patch
	// source, where RepoPath is only a best-effort hint for finding a repo
	// to read file content from).
	if opts.source.RepoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		opts.source.RepoPath = cwd
	}

	f, isPrimary, err := acquireLock(lockPath())
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	if !isPrimary {
		defer f.Close()
		addr, err := readLockAddr(f)
		if err != nil {
			return err
		}
		return askRunningInstance(addr, opts.source)
	}
	defer releaseLock(f)

	store := session.New(session.DefaultRoot())
	ctx := context.Background()

	ws, err := review.Open(ctx, store, opts.source)
	if err != nil {
		return err
	}

	srv := web.New(store, ws, nil)
	ln, err := srv.Listen(opts.port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	if err := writeLockAddr(f, ln.Addr().String()); err != nil {
		return fmt.Errorf("lock: %w", err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(os.Stderr, "flowreview: server:", err)
		}
	}()

	url := srv.URL()
	switch {
	case opts.noOpen:
		fmt.Println(url + "/")
	case opts.browser:
		fmt.Println(url + "/")
		if err := openBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, "flowreview: could not open browser:", err)
		}
	default:
		srv.SetWindow(windowController{})
		return window.Run(window.Options{Title: ws.Session.Title, URL: url})
	}
	select {} // no-open and browser modes: served until the process is killed
}

// windowController adapts internal/window's package-level Focus/Navigate to
// web.WindowController, so web does not need to import a cgo package.
type windowController struct{}

func (windowController) Focus()            { window.Focus() }
func (windowController) Navigate(u string) { window.Navigate(u) }

// askRunningInstance hands src to the instance already serving at addr,
// instead of starting a second one, and asks it to focus its window.
func askRunningInstance(addr string, src model.Source) error {
	body, err := json.Marshal(struct {
		Source model.Source `json:"source"`
	}{src})
	if err != nil {
		return err
	}
	resp, err := http.Post("http://"+addr+"/api/open", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("could not reach the running instance at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("running instance at %s refused the request (status %d)", addr, resp.StatusCode)
	}
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
