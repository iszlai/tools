package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBuild writes an executable shell script to path that prints
// "flowreview <ver>" when invoked with "version", standing in for a real
// downloaded binary without needing to compile one for every test case.
func fakeBuild(t *testing.T, path, ver string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview %s; fi\n", ver)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeReleaseServer serves assetBody at /latest/download/flowreview_<os>_<arch>
// and, when sha256 is non-empty, a matching ".sha256" file alongside it.
func fakeReleaseServer(t *testing.T, assetBody []byte, sha256Body string) *httptest.Server {
	t.Helper()
	asset := fmt.Sprintf("flowreview_%s_%s", runtime.GOOS, runtime.GOARCH)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/" + asset:
			w.Write(assetBody)
		case "/latest/download/" + asset + ".sha256":
			if sha256Body == "" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(sha256Body))
		default:
			http.NotFound(w, r)
		}
	}))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestUpgrade_NewerVersionReplacesSelf(t *testing.T) {
	version = "v1.0.0"
	t.Cleanup(func() { version = "dev" })

	dir := t.TempDir()
	self := filepath.Join(dir, "flowreview")
	fakeBuild(t, self, "v1.0.0")

	newBuild := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview v2.0.0; fi\n")
	srv := fakeReleaseServer(t, newBuild, "")
	defer srv.Close()

	var out bytes.Buffer
	if err := upgrade(self, srv.URL, &out); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if !strings.Contains(out.String(), "upgraded") || !strings.Contains(out.String(), "v1.0.0 -> v2.0.0") {
		t.Errorf("output = %q, want an upgraded message naming both versions", out.String())
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBuild) {
		t.Error("self was not replaced with the downloaded build")
	}
}

func TestUpgrade_SameVersionIsANoOp(t *testing.T) {
	version = "v1.0.0"
	t.Cleanup(func() { version = "dev" })

	dir := t.TempDir()
	self := filepath.Join(dir, "flowreview")
	original := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview v1.0.0; fi\n")
	if err := os.WriteFile(self, original, 0o755); err != nil {
		t.Fatal(err)
	}

	// The "latest" release happens to report the same version as self, just
	// with different bytes (e.g. rebuilt, same source) -- upgrade must still
	// leave self's content untouched rather than replacing it pointlessly.
	sameVersionDifferentBytes := []byte("#!/bin/sh\n# a different build of the same release\nif [ \"$1\" = version ]; then echo flowreview v1.0.0; fi\n")
	srv := fakeReleaseServer(t, sameVersionDifferentBytes, "")
	defer srv.Close()

	var out bytes.Buffer
	if err := upgrade(self, srv.URL, &out); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if !strings.Contains(out.String(), "already on the latest release") {
		t.Errorf("output = %q, want a no-op message", out.String())
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Error("self was replaced even though the version did not change")
	}
}

func TestUpgrade_ChecksumMismatchRefuses(t *testing.T) {
	version = "v1.0.0"
	t.Cleanup(func() { version = "dev" })

	dir := t.TempDir()
	self := filepath.Join(dir, "flowreview")
	original := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview v1.0.0; fi\n")
	if err := os.WriteFile(self, original, 0o755); err != nil {
		t.Fatal(err)
	}

	newBuild := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview v2.0.0; fi\n")
	wrongChecksum := sha256Hex([]byte("not the asset"))
	srv := fakeReleaseServer(t, newBuild, wrongChecksum+"  flowreview_"+runtime.GOOS+"_"+runtime.GOARCH+"\n")
	defer srv.Close()

	var out bytes.Buffer
	err := upgrade(self, srv.URL, &out)
	if err == nil {
		t.Fatal("upgrade: want an error on checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("err = %v, want a checksum mismatch error", err)
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Error("self was replaced despite the checksum mismatch")
	}

	// The failed download's temp file is cleaned up, not left next to self.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries after a refused upgrade, want 1 (just self)", len(entries))
	}
}

func TestUpgrade_MatchingChecksumSucceeds(t *testing.T) {
	version = "v1.0.0"
	t.Cleanup(func() { version = "dev" })

	dir := t.TempDir()
	self := filepath.Join(dir, "flowreview")
	fakeBuild(t, self, "v1.0.0")

	newBuild := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo flowreview v2.0.0; fi\n")
	srv := fakeReleaseServer(t, newBuild, sha256Hex(newBuild)+"  flowreview_"+runtime.GOOS+"_"+runtime.GOARCH+"\n")
	defer srv.Close()

	var out bytes.Buffer
	if err := upgrade(self, srv.URL, &out); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if !strings.Contains(out.String(), "v1.0.0 -> v2.0.0") {
		t.Errorf("output = %q, want an upgraded message", out.String())
	}
}

func TestUpgrade_NoBuildPublishedIsAnError(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "flowreview")
	fakeBuild(t, self, "v1.0.0")

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var out bytes.Buffer
	if err := upgrade(self, srv.URL, &out); err == nil {
		t.Fatal("upgrade: want an error when no build is published, got nil")
	}
}

func TestRunUpgradeCLI_UnknownArgIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runUpgradeCLI([]string{"--bogus"}, &out, &errOut)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunUpgradeCLI_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runUpgradeCLI([]string{"-h"}, &out, &errOut)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "usage: flowreview upgrade") {
		t.Errorf("output = %q, want the usage text", out.String())
	}
}
