package sidecar

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHelperProcess is not a real test: it is re-executed as a stand-in
// llama-server by the fake "llama-server" scripts the tests below write, so
// Start/Stop/pidfile/log-capture can be exercised without the real ~30 MB
// binary. The classic os/exec self-exec pattern; see TestMain-less packages
// like os/exec's own tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	port := ""
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if port == "" {
		fmt.Fprintln(os.Stderr, "fake llama-server: no --port given")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "fake llama-server: starting")

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "fake llama-server:", err)
		os.Exit(1)
	}
}

// writeFakeLlamaServer drops a shell script named "llama-server" into dir
// that re-execs this test binary as TestHelperProcess, so Options.BinDir can
// point at it like a real extracted build.
func writeFakeLlamaServer(t *testing.T, dir string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestHelperProcess$ -- \"$@\"\n", self)
	if err := os.WriteFile(filepath.Join(dir, "llama-server"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeBinDir(t *testing.T) string {
	t.Helper()
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	dir := t.TempDir()
	writeFakeLlamaServer(t, dir)
	return dir
}

func TestStartStop(t *testing.T) {
	binDir := fakeBinDir(t)
	dataRoot := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sc, err := Start(ctx, Options{DataRoot: dataRoot, ModelPath: "fake.gguf", BinDir: binDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	pidData, err := os.ReadFile(filepath.Join(dataRoot, pidFileName))
	if err != nil {
		t.Fatalf("pidfile: %v", err)
	}
	if strings.TrimSpace(string(pidData)) == "" {
		t.Error("pidfile is empty")
	}

	if err := sc.Provider("fake").Health(ctx); err != nil {
		t.Errorf("Health after Start: %v", err)
	}

	if err := sc.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, pidFileName)); !os.IsNotExist(err) {
		t.Error("pidfile should be removed after Stop")
	}

	logData, err := os.ReadFile(filepath.Join(dataRoot, logFileName))
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	if !strings.Contains(string(logData), "fake llama-server: starting") {
		t.Errorf("log file missing expected stderr capture: %q", logData)
	}

	// Stop must be idempotent.
	if err := sc.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestStart_KillsStaleOrphan(t *testing.T) {
	binDir := fakeBinDir(t)
	dataRoot := t.TempDir()

	ctx := context.Background()
	first, err := Start(ctx, Options{DataRoot: dataRoot, ModelPath: "fake.gguf", BinDir: binDir})
	if err != nil {
		t.Fatalf("Start (first): %v", err)
	}
	orphanPID := first.cmd.Process.Pid
	// Simulate a crash: the pidfile is left behind, but Stop is never called
	// on first, so its process keeps running.

	second, err := Start(ctx, Options{DataRoot: dataRoot, ModelPath: "fake.gguf", BinDir: binDir})
	if err != nil {
		t.Fatalf("Start (second): %v", err)
	}
	t.Cleanup(func() { second.Stop() })

	if err := syscall.Kill(orphanPID, 0); err == nil {
		t.Error("orphan from the first Start should have been killed by the second")
	}
	if err := second.Provider("fake").Health(ctx); err != nil {
		t.Errorf("Health on the second sidecar: %v", err)
	}
}

func TestStart_StopsOnContextCancel(t *testing.T) {
	binDir := fakeBinDir(t)
	dataRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	sc, err := Start(ctx, Options{DataRoot: dataRoot, ModelPath: "fake.gguf", BinDir: binDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := sc.cmd.Process.Pid

	cancel()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // gone, as expected
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("process still alive after context cancellation")
}

func TestExtract_NotEmbedded(t *testing.T) {
	// This runs without -tags llama_embed (the default `go test`), so
	// llama.Available() is false and Extract must say so plainly rather than
	// unpack nothing silently.
	_, err := Extract(t.TempDir())
	if err == nil {
		t.Fatal("want ErrNotEmbedded in a build without -tags llama_embed")
	}
}
