// Package toolchain downloads and caches Go toolchains on demand, so a repo
// can be opened and analyzed without `go` on PATH. Each version lives in its
// own directory under Manager.Root; versions already cached need no network.
package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// DefaultBaseURL is the official Go download index.
const DefaultBaseURL = "https://go.dev/dl/"

// ErrDeclined is returned by Ensure when Confirm rejects the Disclosure.
var ErrDeclined = errors.New("toolchain: download declined")

// Disclosure describes a download before it happens. The UI or CLI shows it
// to the user and Confirm reports their answer; Ensure never transfers a
// byte without an approved Disclosure.
type Disclosure struct {
	Version string
	URL     string
	Size    int64
	SHA256  string
	Source  string // human-readable origin, e.g. "go.dev/dl"
}

// Installed is one toolchain already cached under Root.
type Installed struct {
	Version string `json:"version"`
	GOROOT  string `json:"goroot"`
}

// Manager downloads and caches Go toolchains under Root, one directory per
// version. The zero value is unusable; Root is required, the rest have
// defaults.
type Manager struct {
	Root    string // e.g. <FlowReview data dir>/bin/go
	BaseURL string // defaults to DefaultBaseURL
	HTTP    *http.Client
	Confirm func(Disclosure) bool
}

func (m *Manager) baseURL() string {
	if m.BaseURL != "" {
		return m.BaseURL
	}
	return DefaultBaseURL
}

func (m *Manager) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return http.DefaultClient
}

// versionDir is where a version's extracted toolchain lives: versionDir/go
// is GOROOT.
func (m *Manager) versionDir(version string) string {
	return filepath.Join(m.Root, version)
}

func (m *Manager) goroot(version string) string {
	return filepath.Join(m.versionDir(version), "go")
}

// dlFile is one archive entry in the go.dev/dl JSON listing.
type dlFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
}

// dlRelease is one Go release in the go.dev/dl JSON listing.
type dlRelease struct {
	Version string   `json:"version"` // "go1.22.3"
	Stable  bool     `json:"stable"`
	Files   []dlFile `json:"files"`
}

// VersionForModule reports the Go version a module's go.mod asks for: the
// toolchain line's exact version when present (it always wins, per
// GOTOOLCHAIN=auto semantics), else the go directive's version. A go
// directive that names only a minor version ("go 1.22") is resolved to the
// latest 1.22.x release via the go.dev/dl listing; offline or when the
// listing can't be fetched, it falls back to "1.22.0". The returned version
// has no "go" prefix.
func (m *Manager) VersionForModule(goModPath string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("toolchain: read %s: %w", goModPath, err)
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", fmt.Errorf("toolchain: parse %s: %w", goModPath, err)
	}
	if f.Toolchain != nil && f.Toolchain.Name != "" {
		return strings.TrimPrefix(f.Toolchain.Name, "go"), nil
	}
	if f.Go == nil || f.Go.Version == "" {
		return "", fmt.Errorf("toolchain: %s has no go directive", goModPath)
	}

	v := f.Go.Version
	if strings.Count(v, ".") >= 2 {
		return v, nil // already a full patch version
	}
	return m.latestPatch(v), nil
}

// latestPatch resolves a minor version ("1.22") to its latest known patch
// release. It never fails: when the listing can't be fetched or cached, it
// falls back to "<minor>.0".
func (m *Manager) latestPatch(minor string) string {
	releases, err := m.listReleases(context.Background())
	if err != nil {
		return minor + ".0"
	}
	best := ""
	for _, r := range releases {
		v := strings.TrimPrefix(r.Version, "go")
		if v == minor || strings.HasPrefix(v, minor+".") {
			if best == "" || comparePatch(v, best) > 0 {
				best = v
			}
		}
	}
	if best == "" {
		return minor + ".0"
	}
	return best
}

// comparePatch compares two dotted version strings numerically, component by
// component. Missing components compare as 0.
func comparePatch(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an = atoiSafe(as[i])
		}
		if i < len(bs) {
			bn = atoiSafe(bs[i])
		}
		if an != bn {
			return an - bn
		}
	}
	return 0
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// listingCachePath is where the last successfully fetched go.dev/dl listing
// is cached, so VersionForModule can still resolve a minor version offline.
func (m *Manager) listingCachePath() string {
	return filepath.Join(m.Root, ".dl-listing.json")
}

func (m *Manager) listReleases(ctx context.Context) ([]dlRelease, error) {
	data, fetchErr := m.fetchListing(ctx)
	if fetchErr != nil {
		cached, err := os.ReadFile(m.listingCachePath())
		if err != nil {
			return nil, fetchErr
		}
		data = cached
	} else if m.Root != "" {
		if err := os.MkdirAll(m.Root, 0o755); err == nil {
			_ = os.WriteFile(m.listingCachePath(), data, 0o644)
		}
	}

	var releases []dlRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, fmt.Errorf("toolchain: parse listing: %w", err)
	}
	return releases, nil
}

func (m *Manager) fetchListing(ctx context.Context) ([]byte, error) {
	u := strings.TrimRight(m.baseURL(), "/") + "/?mode=json&include=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("toolchain: list %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// findArchive picks the darwin/arm64 archive for version out of a listing.
func findArchive(releases []dlRelease, version string) (dlFile, error) {
	want := "go" + version
	for _, r := range releases {
		if r.Version != want {
			continue
		}
		for _, f := range r.Files {
			if f.OS == "darwin" && f.Arch == "arm64" && f.Kind == "archive" {
				return f, nil
			}
		}
	}
	return dlFile{}, fmt.Errorf("toolchain: no darwin/arm64 archive for %s", want)
}

// Ensure makes sure version is installed under Root and returns its GOROOT.
// If Root/<version>/go/bin/go already exists, Ensure returns it without any
// network request. Otherwise it looks up the darwin/arm64 archive in the
// go.dev/dl listing, asks Confirm before downloading, downloads to a
// resumable .part file, verifies its sha256 (deleting it on mismatch),
// extracts it safely, and atomically moves the result into place.
func (m *Manager) Ensure(ctx context.Context, version string) (string, error) {
	goroot := m.goroot(version)
	if _, err := os.Stat(filepath.Join(goroot, "bin", "go")); err == nil {
		return goroot, nil
	}

	releases, err := m.listReleases(ctx)
	if err != nil {
		return "", fmt.Errorf("toolchain: list releases: %w", err)
	}
	file, err := findArchive(releases, version)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(m.baseURL(), "/") + "/" + file.Filename
	disclosure := Disclosure{
		Version: version,
		URL:     url,
		Size:    file.Size,
		SHA256:  file.SHA256,
		Source:  "go.dev/dl",
	}
	if m.Confirm != nil && !m.Confirm(disclosure) {
		return "", ErrDeclined
	}

	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return "", fmt.Errorf("toolchain: mkdir %s: %w", m.Root, err)
	}

	partPath := filepath.Join(m.Root, ".download-"+version+".tar.gz.part")
	if err := m.download(ctx, url, partPath); err != nil {
		return "", err
	}

	if err := verifySHA256(partPath, file.SHA256); err != nil {
		os.Remove(partPath)
		return "", err
	}

	extractDir, err := os.MkdirTemp(m.Root, ".extract-"+version+"-")
	if err != nil {
		os.Remove(partPath)
		return "", fmt.Errorf("toolchain: mkdir temp: %w", err)
	}
	if err := extractTarGz(partPath, extractDir); err != nil {
		os.RemoveAll(extractDir)
		os.Remove(partPath)
		return "", err
	}
	os.Remove(partPath)

	dest := m.versionDir(version)
	os.RemoveAll(dest) // any stale partial install from a previous failed run
	if err := os.Rename(extractDir, dest); err != nil {
		os.RemoveAll(extractDir)
		return "", fmt.Errorf("toolchain: move extracted toolchain into place: %w", err)
	}

	return goroot, nil
}

// download fetches url into dest, resuming from dest's current size with an
// HTTP Range request when dest already exists (a previous attempt's .part
// file). If the server ignores the Range request and sends the whole body
// again (200 instead of 206), download restarts dest from scratch.
func (m *Manager) download(ctx context.Context, url, dest string) error {
	var startAt int64
	if fi, err := os.Stat(dest); err == nil {
		startAt = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("toolchain: download request: %w", err)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if startAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startAt))
	}

	resp, err := m.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("toolchain: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		startAt = 0
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("toolchain: download %s: %s", url, resp.Status)
	}

	f, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return fmt.Errorf("toolchain: open %s: %w", dest, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("toolchain: write %s: %w", dest, err)
	}
	return nil
}

// Env returns the environment for running go tools against goroot: PATH
// prefixed with goroot's bin, GOROOT set, GOTOOLCHAIN=auto (so a go.mod with
// a newer toolchain line still upgrades itself), GOFLAGS=-mod=readonly,
// GOCACHE under cacheDir, and telemetry off. It starts from the current
// process environment so everything else (HOME, GOPROXY, …) passes through.
func Env(goroot, cacheDir string) []string {
	overrides := map[string]string{
		"GOROOT":      goroot,
		"PATH":        filepath.Join(goroot, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GOTOOLCHAIN": "auto",
		"GOFLAGS":     "-mod=readonly",
		"GOCACHE":     cacheDir,
		"GOTELEMETRY": "off",
	}

	env := os.Environ()
	out := make([]string, 0, len(env)+len(overrides))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if _, overridden := overrides[key]; overridden {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// List returns every toolchain version fully installed under Root, for
// `flowreview toolchain list`.
func (m *Manager) List() ([]Installed, error) {
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("toolchain: list %s: %w", m.Root, err)
	}

	var out []Installed
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue // scratch files/dirs (listing cache, in-progress extracts)
		}
		goroot := filepath.Join(m.Root, e.Name(), "go")
		if _, err := os.Stat(filepath.Join(goroot, "bin", "go")); err != nil {
			continue
		}
		out = append(out, Installed{Version: e.Name(), GOROOT: goroot})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Remove deletes a cached toolchain, for `flowreview toolchain rm`.
func (m *Manager) Remove(version string) error {
	dir := m.versionDir(version)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("toolchain: %s is not installed", version)
	}
	return os.RemoveAll(dir)
}
