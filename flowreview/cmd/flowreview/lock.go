package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/iszlai/tools/flowreview/internal/core/session"
)

// lockPath is the sidecar file one running flowreview instance holds an
// exclusive flock on, with its listen address written inside, for as long as
// it serves. A second invocation that cannot take the lock reads the address
// here and asks that instance to open its source instead of starting a
// competing server.
func lockPath() string {
	return filepath.Join(session.DefaultRoot(), "flowreview.lock")
}

// acquireLock takes an exclusive, non-blocking flock on path, creating it if
// needed. ok is false when another process already holds it -- flock is tied
// to the holding process's open file description, not to any byte in the
// file, so a crashed process's stale address never fools this into thinking
// it is still running. The returned file is always open and must be closed
// by the caller (releaseLock does this for the primary instance).
func acquireLock(path string) (f *os.File, ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return f, false, nil
		}
		f.Close()
		return nil, false, err
	}
	return f, true, nil
}

// releaseLock unlocks and closes f. The flock is also released automatically
// if the process dies without calling this, which is the point: the next
// invocation's acquireLock then succeeds instead of trusting stale content.
func releaseLock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// writeLockAddr records addr (e.g. "127.0.0.1:54321") as the primary
// instance's listen address, for the next invocation to read.
func writeLockAddr(f *os.File, addr string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.WriteAt([]byte(addr), 0)
	return err
}

// readLockAddr reads the address the lock holder wrote. An empty file means
// that instance is still between acquiring the lock and finishing its own
// Listen -- a narrow startup race, reported as a clear "try again" rather
// than an empty address.
func readLockAddr(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(data))
	if addr == "" {
		return "", fmt.Errorf("another flowreview instance is starting; try again")
	}
	return addr, nil
}
