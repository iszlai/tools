package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// withTestCatalog swaps the package Catalog for just entry, for the duration
// of the test, so Manager.Install can be exercised against an httptest
// fixture instead of Hugging Face.
func withTestCatalog(t *testing.T, entry Entry) {
	t.Helper()
	orig := Catalog
	Catalog = []Entry{entry}
	t.Cleanup(func() { Catalog = orig })
}

func fakeGGUF(size int) ([]byte, string) {
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i)
	}
	sum := sha256.Sum256(content)
	return content, hex.EncodeToString(sum[:])
}

// TestInstallResumesInterruptedDownload serves the first cutAt bytes of the
// fixture and then hangs up, as a real interrupted download would; the
// second Install call must pick up where the first left off via a Range
// request, not start over.
func TestInstallResumesInterruptedDownload(t *testing.T) {
	content, sum := fakeGGUF(50_000)
	const cutAt = 20_000

	var attempt int32
	var secondAttemptStart atomic.Int64
	secondAttemptStart.Store(-1)

	mux := http.NewServeMux()
	mux.HandleFunc("/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempt, 1)
		start := 0
		if rng := r.Header.Get("Range"); rng != "" {
			fmt.Sscanf(rng, "bytes=%d-", &start)
		}

		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(content)-start))
			if start > 0 {
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			w.Write(content[start:cutAt])
			w.(http.Flusher).Flush()
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("ResponseWriter does not support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close() // simulate a dropped connection mid-download
			return
		}

		secondAttemptStart.Store(int64(start))
		w.Header().Set("Content-Length", strconv.Itoa(len(content)-start))
		if start > 0 {
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		w.Write(content[start:])
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withTestCatalog(t, Entry{
		Name:     "test-model",
		URL:      srv.URL + "/model.gguf",
		SHA256:   sum,
		Size:     int64(len(content)),
		Licence:  "Test",
		MinRAMGB: 1,
		Tier:     TierSmall,
	})

	dir := t.TempDir()
	mgr := &Manager{Root: dir, Confirm: func(Disclosure) bool { return true }}

	if err := mgr.Install(context.Background(), "test-model", nil); err == nil {
		t.Fatal("first Install should have failed when the connection dropped")
	}
	partPath := filepath.Join(dir, "test-model.gguf.part")
	fi, err := os.Stat(partPath)
	if err != nil {
		t.Fatalf("part file missing after interrupted download: %v", err)
	}
	if fi.Size() == 0 || fi.Size() >= int64(len(content)) {
		t.Fatalf("part file size = %d, want something between 0 and %d", fi.Size(), len(content))
	}

	if err := mgr.Install(context.Background(), "test-model", nil); err != nil {
		t.Fatalf("second Install (resume) failed: %v", err)
	}
	if got := secondAttemptStart.Load(); got != fi.Size() {
		t.Errorf("second attempt started at byte %d, want %d (the Range the first attempt left off at)", got, fi.Size())
	}
	if _, err := os.Stat(partPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("part file should be gone after a completed install")
	}

	got, err := os.ReadFile(filepath.Join(dir, "test-model.gguf"))
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(got) != string(content) {
		t.Error("installed file content does not match the fixture")
	}
}

// TestInstallWrongChecksumDeletesFile asserts a model whose downloaded bytes
// do not match the catalog's sha256 is never left on disk as if it were
// installed.
func TestInstallWrongChecksumDeletesFile(t *testing.T) {
	content, _ := fakeGGUF(1_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	withTestCatalog(t, Entry{
		Name:     "bad-model",
		URL:      srv.URL,
		SHA256:   strings.Repeat("0", 64), // guaranteed not to match the fixture's real sha256
		Size:     int64(len(content)),
		MinRAMGB: 1,
		Tier:     TierSmall,
	})

	dir := t.TempDir()
	mgr := &Manager{Root: dir, Confirm: func(Disclosure) bool { return true }}

	err := mgr.Install(context.Background(), "bad-model", nil)
	if err == nil {
		t.Fatal("Install with a wrong checksum should fail")
	}

	for _, suffix := range []string{".gguf", ".gguf.part"} {
		if _, err := os.Stat(filepath.Join(dir, "bad-model"+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should not exist after a checksum mismatch", suffix)
		}
	}
}

// TestInstallDeclinedMakesNoRequest asserts Confirm returning false stops
// Install before any HTTP request is made.
func TestInstallDeclinedMakesNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when Confirm declines")
	}))
	defer srv.Close()

	withTestCatalog(t, Entry{
		Name:     "declined-model",
		URL:      srv.URL,
		SHA256:   "deadbeef",
		Size:     123,
		MinRAMGB: 1,
		Tier:     TierSmall,
	})

	dir := t.TempDir()
	var gotDisclosure Disclosure
	mgr := &Manager{Root: dir, Confirm: func(d Disclosure) bool {
		gotDisclosure = d
		return false
	}}

	err := mgr.Install(context.Background(), "declined-model", nil)
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined", err)
	}
	if gotDisclosure.Name != "declined-model" || gotDisclosure.Size != 123 {
		t.Errorf("disclosure = %+v, want it to describe the catalog entry", gotDisclosure)
	}
	if _, err := os.Stat(filepath.Join(dir, "declined-model.gguf.part")); !errors.Is(err, os.ErrNotExist) {
		t.Error("no part file should exist when the download was declined")
	}
}

func TestInstallUnknownModel(t *testing.T) {
	mgr := &Manager{Root: t.TempDir(), Confirm: func(Disclosure) bool { return true }}
	err := mgr.Install(context.Background(), "nope", nil)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v, want ErrUnknownModel", err)
	}
}

func TestManagerListAndRemove(t *testing.T) {
	content, sum := fakeGGUF(10)
	withTestCatalog(t, Entry{Name: "m1", SHA256: sum, Size: int64(len(content)), Tier: TierSmall})

	dir := t.TempDir()
	mgr := &Manager{Root: dir}

	statuses := mgr.List()
	if len(statuses) != 1 || statuses[0].Installed {
		t.Fatalf("List() before install = %+v, want one uninstalled entry", statuses)
	}
	if len(mgr.Status()) != 0 {
		t.Fatal("Status() should report nothing installed yet")
	}

	if err := os.WriteFile(filepath.Join(dir, "m1.gguf"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	statuses = mgr.List()
	if !statuses[0].Installed || statuses[0].SizeBytes != int64(len(content)) {
		t.Fatalf("List() after install = %+v", statuses)
	}
	if len(mgr.Status()) != 1 {
		t.Fatal("Status() should report the installed model")
	}

	if err := mgr.Remove("m1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if mgr.List()[0].Installed {
		t.Error("model should no longer be installed after Remove")
	}
	if err := mgr.Remove("m1"); err != nil {
		t.Errorf("Remove of an already-removed model should not error, got %v", err)
	}
}
