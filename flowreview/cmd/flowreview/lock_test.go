package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestAcquireLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowreview.lock")

	f1, ok1, err := acquireLock(path)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	defer f1.Close()
	if !ok1 {
		t.Fatal("first acquireLock: ok = false, want true (nothing else holds it)")
	}

	f2, ok2, err := acquireLock(path)
	if err != nil {
		t.Fatalf("second acquireLock: %v", err)
	}
	defer f2.Close()
	if ok2 {
		t.Fatal("second acquireLock: ok = true, want false (the first still holds it)")
	}

	if err := writeLockAddr(f1, "127.0.0.1:54321"); err != nil {
		t.Fatalf("writeLockAddr: %v", err)
	}
	addr, err := readLockAddr(f2)
	if err != nil {
		t.Fatalf("readLockAddr: %v", err)
	}
	if addr != "127.0.0.1:54321" {
		t.Errorf("addr = %q, want 127.0.0.1:54321", addr)
	}

	releaseLock(f1)

	f3, ok3, err := acquireLock(path)
	if err != nil {
		t.Fatalf("third acquireLock: %v", err)
	}
	defer f3.Close()
	if !ok3 {
		t.Fatal("third acquireLock: ok = false, want true (the lock was released)")
	}
}

func TestReadLockAddrEmptyIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowreview.lock")
	f, ok, err := acquireLock(path)
	if err != nil || !ok {
		t.Fatalf("acquireLock: ok=%v err=%v", ok, err)
	}
	defer f.Close()

	if _, err := readLockAddr(f); err == nil {
		t.Fatal("readLockAddr on an empty file: want an error, got nil")
	}
}

func TestAskRunningInstancePostsSource(t *testing.T) {
	var got struct {
		Source model.Source `json:"source"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/open" {
			t.Errorf("request = %s %s, want POST /api/open", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	src := model.Source{Kind: model.SourceCommit, Ref: "deadbeef", RepoPath: "/tmp/repo"}
	addr := ts.Listener.Addr().String()
	if err := askRunningInstance(addr, src); err != nil {
		t.Fatalf("askRunningInstance: %v", err)
	}
	if got.Source != src {
		t.Errorf("posted source = %+v, want %+v", got.Source, src)
	}
}

func TestAskRunningInstanceNonOKIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	err := askRunningInstance(ts.Listener.Addr().String(), model.Source{Kind: model.SourceWorktree})
	if err == nil {
		t.Fatal("want an error on a non-200 response")
	}
}
