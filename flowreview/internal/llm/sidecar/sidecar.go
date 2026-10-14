// Package sidecar manages the embedded llama-server process: extracting the
// pinned build from third_party/llama, starting and health-checking it, and
// speaking to it over its OpenAI-compatible HTTP API (see provider.go).
package sidecar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iszlai/tools/flowreview/third_party/llama"
)

// ErrNotEmbedded is returned by Extract when this binary has no embedded
// llama-server build: it was not built with -tags llama_embed, or
// third_party/llama/dist/ was empty when it was.
var ErrNotEmbedded = errors.New("sidecar: no llama-server embedded; run `make fetch-llama` and `make build`")

const (
	pidFileName = "llama-server.pid"
	logFileName = "llama-server.log"
	healthWait  = 30 * time.Second
	healthPoll  = 150 * time.Millisecond
)

// Extract ensures the embedded llama-server build is unpacked at
// root/bin/llama/<llama.Version>/ and returns that directory. If it is
// already there, extraction is skipped. Concurrent callers racing on the same
// root are safe: each unpacks to its own temp directory and renames it into
// place, so the loser's rename finds the winner's directory already there and
// just uses that.
func Extract(root string) (string, error) {
	if !llama.Available() {
		return "", ErrNotEmbedded
	}
	dir := filepath.Join(root, "bin", "llama", llama.Version)
	if hasServer(dir) {
		return dir, nil
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("sidecar: extract: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".extract-"+llama.Version+"-")
	if err != nil {
		return "", fmt.Errorf("sidecar: extract: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once the rename below succeeds

	err = fs.WalkDir(llama.FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(llama.FS, name)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(tmp, name), data, 0o755)
	})
	if err != nil {
		return "", fmt.Errorf("sidecar: extract: %w", err)
	}

	if err := os.Rename(tmp, dir); err != nil {
		if hasServer(dir) {
			return dir, nil // another process won the race; that is success
		}
		return "", fmt.Errorf("sidecar: extract: %w", err)
	}
	return dir, nil
}

func hasServer(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "llama-server"))
	return err == nil && fi.Mode().IsRegular()
}

// Options configures one sidecar run.
type Options struct {
	// DataRoot is the Application Support root: the extracted build lives at
	// DataRoot/bin/llama/<version>/ (unless BinDir overrides that), and the
	// stderr log and pidfile sit directly under DataRoot.
	DataRoot string
	// ModelPath is the .gguf to load. Required.
	ModelPath string
	// CtxSize is --ctx-size; 0 means the PLAN.md default of 8192.
	CtxSize int
	// Parallel is --parallel (server slots); 0 means the PLAN.md default of 2.
	Parallel int
	// ExtraArgs is appended after the standard flags.
	ExtraArgs []string
	// BinDir skips Extract and runs the llama-server found there instead.
	// Tests point it at a stand-in binary.
	BinDir string
}

// Sidecar is one running llama-server process.
type Sidecar struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	exited   atomic.Bool
	exitCh   chan error
	baseURL  string
	port     int
	pidPath  string
	logFile  *os.File
	stopOnce sync.Once
}

// BaseURL is where the sidecar's OpenAI-compatible HTTP API listens.
func (s *Sidecar) BaseURL() string { return s.baseURL }

// Port is the free TCP port the sidecar was started on.
func (s *Sidecar) Port() int { return s.port }

// Provider returns a ModelProvider talking to this running sidecar.
func (s *Sidecar) Provider(modelID string) *Provider {
	return NewProvider(s.baseURL, modelID)
}

// Start extracts the build if needed, kills any orphan left by a previous
// crash, picks a free port, spawns llama-server on it with the flags fixed by
// PLAN.md §3, and waits for /health before returning. The returned Sidecar
// must be stopped with Stop; it also stops if ctx is cancelled.
func Start(ctx context.Context, opts Options) (*Sidecar, error) {
	if opts.ModelPath == "" {
		return nil, errors.New("sidecar: ModelPath is required")
	}
	if opts.DataRoot == "" {
		return nil, errors.New("sidecar: DataRoot is required")
	}
	ctxSize := opts.CtxSize
	if ctxSize == 0 {
		ctxSize = 8192
	}
	parallel := opts.Parallel
	if parallel == 0 {
		parallel = 2
	}

	binDir := opts.BinDir
	if binDir == "" {
		var err error
		if binDir, err = Extract(opts.DataRoot); err != nil {
			return nil, err
		}
	}
	bin := filepath.Join(binDir, "llama-server")

	if err := os.MkdirAll(opts.DataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	pidPath := filepath.Join(opts.DataRoot, pidFileName)
	killStaleOrphan(pidPath)

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)

	args := []string{
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--model", opts.ModelPath,
		"--parallel", strconv.Itoa(parallel),
		"--ctx-size", strconv.Itoa(ctxSize),
		"--cache-prompt",
		"--jinja",
	}
	args = append(args, opts.ExtraArgs...)

	logPath := filepath.Join(opts.DataRoot, logFileName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	// This build's LC_RPATH is @loader_path, so the dylibs next to
	// llama-server resolve on their own (verified against the pinned
	// release; see third_party/llama/README.md). DYLD_LIBRARY_PATH is set
	// below regardless, as a cheap fallback for any build where that is not
	// true — recorded here for anyone reading the log.
	fmt.Fprintf(logFile, "flowreview: bin=%s loader-path-rpath=%v\n", bin, usesLoaderPathRPath(bin))

	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+binDir)
	cmd.Stderr = logFile
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second // escalates to SIGKILL if SIGTERM is ignored

	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		return nil, fmt.Errorf("sidecar: start llama-server: %w", err)
	}

	sc := &Sidecar{
		cmd: cmd, cancel: cancel, exitCh: make(chan error, 1),
		baseURL: baseURL, port: port, pidPath: pidPath, logFile: logFile,
	}
	go func() {
		err := cmd.Wait()
		sc.exited.Store(true)
		sc.exitCh <- err
	}()

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		cancel()
		<-sc.exitCh
		logFile.Close()
		return nil, fmt.Errorf("sidecar: %w", err)
	}

	if err := waitHealthy(ctx, baseURL, &sc.exited); err != nil {
		cancel()
		<-sc.exitCh
		logFile.Close()
		os.Remove(pidPath)
		return nil, fmt.Errorf("sidecar: %w (log: %s)", err, logPath)
	}

	return sc, nil
}

// Stop sends SIGTERM (escalating to SIGKILL after 5s if needed, via
// cmd.Cancel/WaitDelay), removes the pidfile and closes the log. Safe to call
// more than once; only the first call does anything.
func (s *Sidecar) Stop() error {
	var closeErr error
	s.stopOnce.Do(func() {
		s.cancel()
		<-s.exitCh // the exit error here is expected (we caused it); nothing to report
		os.Remove(s.pidPath)
		closeErr = s.logFile.Close()
	})
	return closeErr
}

func waitHealthy(ctx context.Context, baseURL string, exited *atomic.Bool) error {
	deadline := time.NewTimer(healthWait)
	defer deadline.Stop()
	ticker := time.NewTicker(healthPoll)
	defer ticker.Stop()
	client := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("timed out waiting for /health")
		case <-ticker.C:
			if exited.Load() {
				return errors.New("llama-server exited before becoming healthy")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				continue // not listening yet
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

// killStaleOrphan kills whatever process a leftover pidfile names, if it is
// still alive. A pidfile only survives to the next Start like this when a
// previous flowreview process crashed before calling Stop; without this, two
// llama-servers would fight over the same model's memory and port.
func killStaleOrphan(pidPath string) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return // nothing running with that pid anymore
	}
	proc.Signal(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	proc.Signal(syscall.SIGKILL) // in case it ignored SIGTERM
}

// freePort asks the OS for a free TCP port by binding to :0 and closing
// immediately, so llama-server can be told --port before it starts listening.
// Another process could take the port between the close and llama-server's
// bind; an accepted, documented race (PLAN.md §3).
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// usesLoaderPathRPath reports whether bin's Mach-O LC_RPATH is @loader_path
// or @executable_path, meaning dylibs placed next to it resolve without
// DYLD_LIBRARY_PATH. Best-effort: a missing otool (non-macOS dev machine)
// just reports false.
func usesLoaderPathRPath(bin string) bool {
	out, err := exec.Command("otool", "-l", bin).Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte("@loader_path")) || bytes.Contains(out, []byte("@executable_path"))
}
