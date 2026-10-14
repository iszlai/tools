package sidecar

import (
	"context"
	"testing"
	"time"
)

// TestLazyStartsOnFirstUse checks PLAN.md §5 FR-28's "started on first
// summary request, not at app launch": constructing a Lazy must not spawn
// anything, and a later call must start exactly one sidecar that every
// subsequent call reuses.
func TestLazyStartsOnFirstUse(t *testing.T) {
	binDir := fakeBinDir(t)
	dataRoot := t.TempDir()

	l := NewLazy(Options{DataRoot: dataRoot, ModelPath: "fake.gguf", BinDir: binDir}, "lazy-test")
	if l.sc != nil {
		t.Fatal("NewLazy must not start a sidecar before first use")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := l.Health(ctx); err != nil {
		t.Fatalf("Health (first call, starts the sidecar): %v", err)
	}
	first := l.sc
	if first == nil {
		t.Fatal("Health did not start a sidecar")
	}

	if err := l.Health(ctx); err != nil {
		t.Fatalf("Health (second call): %v", err)
	}
	if l.sc != first {
		t.Error("a second call started a second sidecar instead of reusing the first")
	}

	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestLazyStopBeforeUse checks Stop is a no-op when nothing ever started.
func TestLazyStopBeforeUse(t *testing.T) {
	l := NewLazy(Options{DataRoot: t.TempDir(), ModelPath: "fake.gguf"}, "lazy-test")
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop before any use: %v", err)
	}
}
