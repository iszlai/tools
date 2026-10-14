// Command flowreview reviews a local diff or a pull request in code-flow
// order, in its own window.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/llm/models"
	"github.com/iszlai/tools/flowreview/internal/model"
	"github.com/iszlai/tools/flowreview/internal/web"
	"github.com/iszlai/tools/flowreview/internal/window"
)

// version is stamped by the Makefile.
var version = "dev"

const usage = `flowreview reviews a local diff or a pull request in its own window.

usage:
  flowreview                          review the worktree diff (cwd)
  flowreview diff <ref|range>         review one commit, or an A..B / A...B range
  flowreview pr <url|owner/repo#N|N>  review a GitHub pull request
  flowreview patch <file>             review a patch file
  flowreview --patch <file>           same as "flowreview patch <file>"
  flowreview sessions [--json] [--all]
                                       list review sessions
  flowreview order [--json] [<ref|range>]
                                       print the diff in flow order
  flowreview model <command>          manage the embedded summarizer model
  flowreview toolchain <command>      manage downloaded Go toolchains
  flowreview upgrade                  replace flowreview with the latest published release
  flowreview version                  print the version
  flowreview help                     show this message

flags (review commands only, may appear before or after the positional argument):
  --no-open      serve only: print the URL on stdout, open nothing
  --browser      open the default browser instead of the native window
  --port <n>     HTTP port to serve on (0, the default, picks a free one)

exit codes:
  0  ok
  1  runtime error
  2  usage error
  3  not a git repository, or the source was not found
  4  auth or host error (no GitHub token, rate limited, host unreachable)
`

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

// dispatch resolves the top-level command and runs it, returning the process
// exit code. "model", "toolchain", "sessions", "upgrade", "version" and
// "help" each own their whole remaining argument list; anything else is the
// review-opening grammar parseArgs and run handle.
func dispatch(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version":
			fmt.Fprintln(stdout, "flowreview", version)
			return 0
		case "help", "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		case "model":
			return models.RunCLI(args[1:], stdout, stderr)
		case "toolchain":
			return runToolchainCLI(args[1:], stdout, stderr)
		case "sessions":
			return runSessionsCLI(args[1:], stdout, stderr)
		case "upgrade":
			return runUpgradeCLI(args[1:], stdout, stderr)
		case "order":
			return runOrderCLI(args[1:], stdout, stderr)
		}
	}

	if err := run(args, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "flowreview:", err)
		return exitCode(err)
	}
	return 0
}

// options are the parsed flags and the source every invocation reviews.
type options struct {
	source  model.Source
	prRaw   string // the raw argument to "pr", resolved into source.PR once cwd is known
	port    int
	browser bool
	noOpen  bool
}

// parseArgs recognizes the review-opening grammar: an optional subcommand
// ("diff <ref|range>", "pr <url|owner/repo#N|N>", "patch <file>") naming the
// source, plus --port/--browser/--no-open/--patch in any order -- a flag may
// come before or after the positional argument, unlike the standard flag
// package (which stops parsing at the first non-flag token). "model",
// "toolchain", "sessions", "version" and "help" are dispatched before this is
// ever called; this only parses the source-opening commands.
func parseArgs(args []string) (options, error) {
	var opts options
	var positional []string
	var patchFlag string
	sawPatchFlag := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-open":
			opts.noOpen = true
		case a == "--browser":
			opts.browser = true
		case a == "--port":
			v, next, err := flagValue(args, i, "--port")
			if err != nil {
				return opts, err
			}
			i = next
			port, perr := strconv.Atoi(v)
			if perr != nil {
				return opts, fmt.Errorf("--port: %w", perr)
			}
			opts.port = port
		case strings.HasPrefix(a, "--port="):
			port, perr := strconv.Atoi(strings.TrimPrefix(a, "--port="))
			if perr != nil {
				return opts, fmt.Errorf("--port: %w", perr)
			}
			opts.port = port
		case a == "--patch":
			v, next, err := flagValue(args, i, "--patch")
			if err != nil {
				return opts, err
			}
			i = next
			patchFlag, sawPatchFlag = v, true
		case strings.HasPrefix(a, "--patch="):
			patchFlag, sawPatchFlag = strings.TrimPrefix(a, "--patch="), true
		case strings.HasPrefix(a, "-"):
			return opts, fmt.Errorf("unknown flag %q", a)
		default:
			positional = append(positional, a)
		}
	}

	if sawPatchFlag {
		if len(positional) > 0 {
			return opts, errors.New("--patch cannot be combined with a diff/pr/patch subcommand")
		}
		opts.source = model.Source{Kind: model.SourcePatch, PatchPath: patchFlag}
		return opts, nil
	}

	switch {
	case len(positional) == 0:
		opts.source = model.Source{Kind: model.SourceWorktree}
	case positional[0] == "diff":
		if len(positional) != 2 {
			return opts, errors.New("usage: flowreview diff <ref|range>")
		}
		ref := positional[1]
		if strings.Contains(ref, "..") {
			opts.source = model.Source{Kind: model.SourceRange, Ref: ref}
		} else {
			opts.source = model.Source{Kind: model.SourceCommit, Ref: ref}
		}
	case positional[0] == "pr":
		if len(positional) != 2 {
			return opts, errors.New("usage: flowreview pr <url|owner/repo#N|N>")
		}
		opts.prRaw = positional[1]
	case positional[0] == "patch":
		if len(positional) != 2 {
			return opts, errors.New("usage: flowreview patch <file>")
		}
		opts.source = model.Source{Kind: model.SourcePatch, PatchPath: positional[1]}
	default:
		return opts, fmt.Errorf("unexpected argument %q", positional[0])
	}
	return opts, nil
}

// flagValue returns the value of a flag given as two tokens ("--port" "4242"
// rather than "--port=4242") and the index its value was consumed from.
func flagValue(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("%s requires a value", name)
	}
	return args[i+1], i + 1, nil
}

func run(args []string, stdout, stderr io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return usageErr(err)
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

	ctx := context.Background()

	if opts.prRaw != "" {
		ref, err := resolvePRRef(ctx, opts.prRaw, opts.source.RepoPath)
		if err != nil {
			return err
		}
		opts.source = model.Source{Kind: model.SourcePR, RepoPath: opts.source.RepoPath, PR: &ref}
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

	dataRoot := session.DefaultRoot()
	store := session.New(dataRoot)

	ws, err := review.Open(ctx, store, opts.source)
	if err != nil {
		return classifyOpenErr(err)
	}

	pipeline, stopSummaries := buildSummaryPipeline(dataRoot)
	defer stopSummaries()

	srv := web.New(store, ws, nil)
	srv.SetSummaries(pipeline)
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
			fmt.Fprintln(stderr, "flowreview: server:", err)
		}
	}()

	url := srv.URL()
	switch {
	case opts.noOpen:
		fmt.Fprintln(stdout, url+"/")
	case opts.browser:
		fmt.Fprintln(stdout, url+"/")
		if err := openBrowser(url); err != nil {
			fmt.Fprintln(stderr, "flowreview: could not open browser:", err)
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
