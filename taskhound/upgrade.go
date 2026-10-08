package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// releasesURL is where published builds live. $TASKHOUND_RELEASES_URL
// overrides it so the tests can stand up their own release server.
const releasesURL = "https://github.com/iszlai/tools/releases"

// cmdUpgrade replaces the running binary with the latest published build for
// this platform. The download lands next to the binary and is renamed over it,
// so a failed or interrupted upgrade leaves the old th untouched.
func cmdUpgrade(args []string) error {
	fs, _ := newFS("upgrade")
	parse(fs, args)

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}

	base := releasesURL
	if v := os.Getenv("TASKHOUND_RELEASES_URL"); v != "" {
		base = strings.TrimRight(v, "/")
	}
	url := fmt.Sprintf("%s/latest/download/th_%s_%s", base, runtime.GOOS, runtime.GOARCH)

	fmt.Fprintf(os.Stderr, "downloading latest for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
	tmp, err := download(url, filepath.Dir(self))
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // a no-op once it has been renamed into place

	out, err := exec.Command(tmp, "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded build does not run: %w", err)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(string(out)), "taskhound ")

	if latest == version {
		fmt.Printf("already on the latest release (%s)\n", version)
		return nil
	}
	if err := os.Rename(tmp, self); err != nil {
		return err
	}
	fmt.Printf("upgraded %s: %s -> %s\n", self, version, latest)
	return nil
}

// download fetches url into a new executable file in dir and returns its path.
func download(url, dir string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("no build published at %s (%s)", url, resp.Status)
	}

	f, err := os.CreateTemp(dir, ".th-upgrade-")
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
