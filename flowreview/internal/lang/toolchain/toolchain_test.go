package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- test fixtures -----------------------------------------------------

// buildTarball makes a minimal gzipped tar archive shaped like a real Go
// distribution: go/bin/go is the only file that matters to callers.
func buildTarball(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	dirs := []string{"go/", "go/bin/", "go/pkg/"}
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	content := []byte(script)
	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildTraversalTarball makes an archive whose single entry tries to escape
// the extraction directory.
func buildTraversalTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("evil")
	if err := tw.WriteHeader(&tar.Header{Name: "../../evil.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// serveRangeable serves data, honoring a simple "bytes=N-" Range request
// with a 206 response, the way go.dev/dl's CDN does.
func serveRangeable(w http.ResponseWriter, r *http.Request, data []byte) {
	rangeHdr := r.Header.Get("Range")
	if rangeHdr == "" {
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}
	var start int
	if _, err := fmt.Sscanf(rangeHdr, "bytes=%d-", &start); err != nil || start < 0 || start > len(data) {
		start = 0
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
	w.WriteHeader(http.StatusPartialContent)
	w.Write(data[start:])
}

// newDLServer serves a go.dev/dl-shaped JSON listing for one version plus
// its archive, and returns the server and the archive's filename.
func newDLServer(t *testing.T, version string, tarball []byte) (*httptest.Server, string) {
	t.Helper()
	sum := sha256.Sum256(tarball)
	hexSum := hex.EncodeToString(sum[:])
	filename := fmt.Sprintf("go%s.darwin-arm64.tar.gz", version)

	listing := []dlRelease{{
		Version: "go" + version,
		Stable:  true,
		Files: []dlFile{{
			Filename: filename,
			OS:       "darwin",
			Arch:     "arm64",
			Version:  "go" + version,
			SHA256:   hexSum,
			Size:     int64(len(tarball)),
			Kind:     "archive",
		}},
	}}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") != "json" {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "include=all") {
			t.Errorf("listing request missing include=all: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, r *http.Request) {
		serveRangeable(w, r, tarball)
	})
	return httptest.NewServer(mux), filename
}

func writeGoMod(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// recordingTransport counts HTTP requests and records their path and Range
// header, so tests can assert "no network" or "resumed with Range".
type recordingTransport struct {
	mu   sync.Mutex
	reqs []struct{ path, rangeHeader string }
	rt   http.RoundTripper
}

func (c *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.reqs = append(c.reqs, struct{ path, rangeHeader string }{req.URL.Path, req.Header.Get("Range")})
	c.mu.Unlock()
	return c.rt.RoundTrip(req)
}

func (c *recordingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func (c *recordingTransport) hit(pathSuffix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.reqs {
		if strings.HasSuffix(r.path, pathSuffix) {
			return true
		}
	}
	return false
}

func (c *recordingTransport) sawRange() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.reqs {
		if r.rangeHeader != "" {
			return true
		}
	}
	return false
}

// --- VersionForModule ----------------------------------------------------

func TestVersionForModuleToolchainLineWins(t *testing.T) {
	dir := t.TempDir()
	path := writeGoMod(t, dir, "module example.com/m\n\ngo 1.21\n\ntoolchain go1.22.4\n")

	mgr := &Manager{Root: t.TempDir()}
	got, err := mgr.VersionForModule(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.22.4" {
		t.Errorf("got %q, want 1.22.4 (the toolchain line)", got)
	}
}

func TestVersionForModuleResolvesMinorToLatestPatch(t *testing.T) {
	listing := []dlRelease{
		{Version: "go1.22.1", Stable: true, Files: []dlFile{{Filename: "go1.22.1.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: "a", Size: 1}}},
		{Version: "go1.22.5", Stable: true, Files: []dlFile{{Filename: "go1.22.5.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: "b", Size: 1}}},
		{Version: "go1.23.0", Stable: true},
	}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	modPath := writeGoMod(t, dir, "module example.com/m\n\ngo 1.22\n")
	mgr := &Manager{Root: t.TempDir(), BaseURL: srv.URL}

	got, err := mgr.VersionForModule(modPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.22.5" {
		t.Errorf("got %q, want 1.22.5 (latest 1.22.x)", got)
	}
}

func TestVersionForModuleOfflineFallsBackToDotZero(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // guarantee connection refused

	dir := t.TempDir()
	modPath := writeGoMod(t, dir, "module example.com/m\n\ngo 1.24\n")
	mgr := &Manager{Root: t.TempDir(), BaseURL: srv.URL}

	got, err := mgr.VersionForModule(modPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.24.0" {
		t.Errorf("got %q, want offline fallback 1.24.0", got)
	}
}

func TestVersionForModuleUsesCachedListingWhenOffline(t *testing.T) {
	listing := []dlRelease{{Version: "go1.25.2", Stable: true, Files: []dlFile{{Filename: "go1.25.2.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: "x", Size: 1}}}}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	srv := httptest.NewServer(mux)

	root := t.TempDir()
	mgr := &Manager{Root: root, BaseURL: srv.URL}
	if _, err := mgr.listReleases(context.Background()); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	srv.Close() // now offline; VersionForModule must fall back to the cache

	dir := t.TempDir()
	modPath := writeGoMod(t, dir, "module example.com/m\n\ngo 1.25\n")
	got, err := mgr.VersionForModule(modPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.25.2" {
		t.Errorf("got %q, want cached 1.25.2, not the .0 fallback", got)
	}
}

// --- Ensure ----------------------------------------------------------------

func TestEnsureDownloadsVerifiesAndInstalls(t *testing.T) {
	tarball := buildTarball(t, "#!/bin/sh\necho fake-go\n")
	srv, filename := newDLServer(t, "1.22.3", tarball)
	defer srv.Close()

	dir := t.TempDir()
	var gotDisclosure Disclosure
	mgr := &Manager{
		Root:    dir,
		BaseURL: srv.URL,
		Confirm: func(d Disclosure) bool { gotDisclosure = d; return true },
	}

	goroot, err := mgr.Ensure(context.Background(), "1.22.3")
	if err != nil {
		t.Fatal(err)
	}
	wantGoroot := filepath.Join(dir, "1.22.3", "go")
	if goroot != wantGoroot {
		t.Errorf("GOROOT = %q, want %q", goroot, wantGoroot)
	}
	if gotDisclosure.Version != "1.22.3" || gotDisclosure.SHA256 == "" || gotDisclosure.Size == 0 || !strings.Contains(gotDisclosure.URL, filename) {
		t.Errorf("disclosure incomplete: %+v", gotDisclosure)
	}

	binPath := filepath.Join(goroot, "bin", "go")
	content, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "#!/bin/sh\necho fake-go\n" {
		t.Errorf("go binary content = %q", content)
	}
	fi, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Error("want the extracted go binary to keep its executable bit")
	}

	// No leftover scratch files.
	if _, err := os.Stat(filepath.Join(dir, ".download-1.22.3.tar.gz.part")); !os.IsNotExist(err) {
		t.Error("want the partial download removed after a successful install")
	}
}

func TestEnsureCachedVersionMakesNoRequest(t *testing.T) {
	tarball := buildTarball(t, "cached")
	srv, _ := newDLServer(t, "1.23.0", tarball)
	defer srv.Close()

	dir := t.TempDir()
	mgr := &Manager{Root: dir, BaseURL: srv.URL, Confirm: func(Disclosure) bool { return true }}
	if _, err := mgr.Ensure(context.Background(), "1.23.0"); err != nil {
		t.Fatalf("priming install: %v", err)
	}

	rt := &recordingTransport{rt: http.DefaultTransport}
	mgr.HTTP = &http.Client{Transport: rt}
	mgr.Confirm = func(Disclosure) bool {
		t.Error("Confirm must not be called for an already-cached version")
		return true
	}

	goroot, err := mgr.Ensure(context.Background(), "1.23.0")
	if err != nil {
		t.Fatal(err)
	}
	if goroot != filepath.Join(dir, "1.23.0", "go") {
		t.Errorf("unexpected GOROOT: %s", goroot)
	}
	if n := rt.count(); n != 0 {
		t.Errorf("want 0 HTTP requests for a cached version, got %d", n)
	}
}

func TestEnsureConfirmFalseDeclines(t *testing.T) {
	tarball := buildTarball(t, "x")
	srv, filename := newDLServer(t, "1.21.0", tarball)
	defer srv.Close()

	dir := t.TempDir()
	rt := &recordingTransport{rt: http.DefaultTransport}
	mgr := &Manager{
		Root:    dir,
		BaseURL: srv.URL,
		HTTP:    &http.Client{Transport: rt},
		Confirm: func(Disclosure) bool { return false },
	}

	_, err := mgr.Ensure(context.Background(), "1.21.0")
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("got %v, want ErrDeclined", err)
	}
	if rt.hit(filename) {
		t.Error("archive must not be downloaded when Confirm declines")
	}
	if _, err := os.Stat(filepath.Join(dir, "1.21.0")); !os.IsNotExist(err) {
		t.Error("declined Ensure must not create the version directory")
	}
}

func TestEnsureCorruptedArchiveDeletedAndErrors(t *testing.T) {
	good := buildTarball(t, "good")
	corrupted := append([]byte(nil), good...)
	corrupted[0] ^= 0xFF // tamper with the bytes after the checksum was computed

	version := "1.20.1"
	sum := sha256.Sum256(good)
	filename := fmt.Sprintf("go%s.darwin-arm64.tar.gz", version)
	listing := []dlRelease{{
		Version: "go" + version,
		Files: []dlFile{{
			Filename: filename, OS: "darwin", Arch: "arm64", Kind: "archive",
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(good)),
		}},
	}}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, r *http.Request) { serveRangeable(w, r, corrupted) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	mgr := &Manager{Root: dir, BaseURL: srv.URL, Confirm: func(Disclosure) bool { return true }}

	_, err = mgr.Ensure(context.Background(), version)
	if err == nil {
		t.Fatal("want a checksum error for a corrupted archive")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".download-"+version+".tar.gz.part")); !os.IsNotExist(statErr) {
		t.Error("corrupted partial archive should have been deleted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, version)); !os.IsNotExist(statErr) {
		t.Error("a corrupted archive must not leave an installed version directory behind")
	}
}

func TestEnsureResumesPartialDownload(t *testing.T) {
	tarball := buildTarball(t, "resumed-content")
	version := "1.19.0"
	srv, _ := newDLServer(t, version, tarball)
	defer srv.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(dir, ".download-"+version+".tar.gz.part")
	half := len(tarball) / 2
	if err := os.WriteFile(partPath, tarball[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	rt := &recordingTransport{rt: http.DefaultTransport}
	mgr := &Manager{Root: dir, BaseURL: srv.URL, HTTP: &http.Client{Transport: rt}, Confirm: func(Disclosure) bool { return true }}

	goroot, err := mgr.Ensure(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	if !rt.sawRange() {
		t.Error("want a Range request to resume the existing .part file")
	}
	content, err := os.ReadFile(filepath.Join(goroot, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "resumed-content" {
		t.Errorf("resumed download content = %q", content)
	}
}

func TestEnsureNewerToolchainLineInstallsIndependently(t *testing.T) {
	oldTarball := buildTarball(t, "old-go")
	newTarball := buildTarball(t, "new-go")
	oldSum := sha256.Sum256(oldTarball)
	newSum := sha256.Sum256(newTarball)
	oldFile, newFile := "go1.21.0.darwin-arm64.tar.gz", "go1.22.5.darwin-arm64.tar.gz"

	listing := []dlRelease{
		{Version: "go1.21.0", Files: []dlFile{{Filename: oldFile, OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: hex.EncodeToString(oldSum[:]), Size: int64(len(oldTarball))}}},
		{Version: "go1.22.5", Files: []dlFile{{Filename: newFile, OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: hex.EncodeToString(newSum[:]), Size: int64(len(newTarball))}}},
	}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	mux.HandleFunc("/"+oldFile, func(w http.ResponseWriter, r *http.Request) { serveRangeable(w, r, oldTarball) })
	mux.HandleFunc("/"+newFile, func(w http.ResponseWriter, r *http.Request) { serveRangeable(w, r, newTarball) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	modPath := writeGoMod(t, dir, "module example.com/m\n\ngo 1.21\n\ntoolchain go1.22.5\n")
	mgr := &Manager{Root: t.TempDir(), BaseURL: srv.URL, Confirm: func(Disclosure) bool { return true }}

	// The repo was opened once before the toolchain line was bumped.
	if _, err := mgr.Ensure(context.Background(), "1.21.0"); err != nil {
		t.Fatalf("installing the old version: %v", err)
	}

	// go.mod gains a newer toolchain line; VersionForModule reports it and
	// Ensure installs it alongside the old one (GOTOOLCHAIN=auto semantics).
	version, err := mgr.VersionForModule(modPath)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.22.5" {
		t.Fatalf("got %q, want the newer toolchain line 1.22.5", version)
	}

	goroot, err := mgr.Ensure(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(goroot, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new-go" {
		t.Errorf("got %q, want the newer toolchain's binary", content)
	}

	list, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("want both versions cached side by side, got %+v", list)
	}
}

// --- extraction safety -----------------------------------------------------

func TestExtractTarGzRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(archive, buildTraversalTarball(t), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archive, dest); err == nil {
		t.Fatal("want an error for a path-traversal entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !os.IsNotExist(err) {
		t.Error("traversal entry must not escape the destination directory")
	}
}

// --- Env ---------------------------------------------------------------

func TestEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	env := Env("/opt/go", "/cache/go")

	get := func(key string) (string, bool) {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == key {
				return v, true
			}
		}
		return "", false
	}

	cases := map[string]string{
		"GOROOT":      "/opt/go",
		"PATH":        "/opt/go/bin:/usr/bin",
		"GOTOOLCHAIN": "auto",
		"GOFLAGS":     "-mod=readonly",
		"GOCACHE":     "/cache/go",
		"GOTELEMETRY": "off",
	}
	for key, want := range cases {
		got, ok := get(key)
		if !ok {
			t.Errorf("%s missing from Env()", key)
		} else if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOROOT=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly one GOROOT entry, got %d", count)
	}
}

// --- List / Remove -----------------------------------------------------

func TestListAndRemove(t *testing.T) {
	root := t.TempDir()
	mkToolchain := func(version string) {
		dir := filepath.Join(root, version, "go", "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go"), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mkToolchain("1.21.0")
	mkToolchain("1.22.3")
	// Scratch directory from an in-progress extraction; must be excluded.
	if err := os.MkdirAll(filepath.Join(root, ".extract-1.23.0-abcd"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := &Manager{Root: root}
	list, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(list), list)
	}
	if list[0].Version != "1.21.0" || list[1].Version != "1.22.3" {
		t.Errorf("want sorted versions, got %+v", list)
	}

	if err := mgr.Remove("1.21.0"); err != nil {
		t.Fatal(err)
	}
	list, err = mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != "1.22.3" {
		t.Errorf("remove did not take effect: %+v", list)
	}

	if err := mgr.Remove("1.99.0"); err == nil {
		t.Error("want an error removing a version that is not installed")
	}
}

func TestListOnMissingRoot(t *testing.T) {
	mgr := &Manager{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	list, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("want an empty list, got %+v", list)
	}
}
