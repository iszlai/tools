package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// releasesURL is where published builds live. $FLOWREVIEW_RELEASES_URL
// overrides it so tests can stand up their own release server instead of
// talking to GitHub.
const releasesURL = "https://github.com/iszlai/tools/releases"

const upgradeUsage = `usage: flowreview upgrade

Replace this binary with the latest published flowreview-v* release for
this platform (flowreview_<os>_<arch>), verifying its checksum first when
the release publishes one.
`

// runUpgradeCLI implements `flowreview upgrade`.
func runUpgradeCLI(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, upgradeUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "flowreview upgrade: unknown argument %q\n\n%s", a, upgradeUsage)
			return 2
		}
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "flowreview upgrade:", err)
		return 1
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		fmt.Fprintln(stderr, "flowreview upgrade:", err)
		return 1
	}

	if err := upgrade(self, releasesBaseURL(), stdout); err != nil {
		fmt.Fprintln(stderr, "flowreview upgrade:", err)
		return 1
	}
	return 0
}

func releasesBaseURL() string {
	if v := os.Getenv("FLOWREVIEW_RELEASES_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return releasesURL
}

// upgrade does the actual work against an explicit binary path and release
// base URL (rather than os.Executable() and releasesURL directly), so tests
// can point both at a scratch file and an httptest server without touching
// the real running binary or the network. It downloads the asset for this
// platform next to self, verifies it against a published ".sha256" checksum
// when the release has one (refusing and leaving self untouched on a
// mismatch), confirms the download actually runs, and renames it over self
// only when its reported version differs from the one running now. A failed
// or interrupted upgrade always leaves self untouched: the download happens
// next to it, under a temp name, and only the final os.Rename ever touches
// self itself.
func upgrade(self, base string, stdout io.Writer) error {
	asset := fmt.Sprintf("flowreview_%s_%s", runtime.GOOS, runtime.GOARCH)
	url := base + "/latest/download/" + asset

	fmt.Fprintf(stdout, "downloading latest for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
	tmp, err := downloadExecutable(url, filepath.Dir(self))
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // a no-op once it has been renamed into place

	if sum, err := fetchChecksum(url + ".sha256"); err == nil {
		if err := verifyChecksum(tmp, sum); err != nil {
			return err
		}
	} // no ".sha256" published, or it could not be fetched: proceed unverified

	out, err := exec.Command(tmp, "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded build does not run: %w", err)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(string(out)), "flowreview ")

	if latest == version {
		fmt.Fprintf(stdout, "already on the latest release (%s)\n", version)
		return nil
	}
	if err := os.Rename(tmp, self); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "upgraded %s: %s -> %s\n", self, version, latest)
	return nil
}

// downloadExecutable fetches url into a new executable file in dir and
// returns its path.
func downloadExecutable(url, dir string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("no build published at %s (%s)", url, resp.Status)
	}

	f, err := os.CreateTemp(dir, ".flowreview-upgrade-")
	if err != nil {
		return "", fmt.Errorf("cannot write next to the binary: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// fetchChecksum returns the hex digest named first in url's body (the format
// both "shasum -a 256 file" and a bare hex digest produce), or an error when
// url can't be fetched -- the caller treats that as "no checksum published"
// rather than a hard failure, since not every release need publish one.
func fetchChecksum(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("no checksum published at %s (%s)", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file at %s", url)
	}
	return fields[0], nil
}

// verifyChecksum checks that path's content hashes to want (hex, case
// insensitive).
func verifyChecksum(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(path), got, want)
	}
	return nil
}
