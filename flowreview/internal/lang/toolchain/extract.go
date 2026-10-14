package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// verifySHA256 checks that path's content hashes to want (hex, case
// insensitive).
func verifySHA256(path, want string) error {
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
		return fmt.Errorf("toolchain: checksum mismatch for %s: got %s, want %s", filepath.Base(path), got, want)
	}
	return nil
}

// extractTarGz extracts a gzipped tar archive into destDir, which must
// already exist. Entries that would resolve outside destDir (absolute
// paths, "..", or a symlink whose target escapes destDir) are rejected.
// Regular files keep the mode bits from the archive.
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("toolchain: open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("toolchain: gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("toolchain: tar: %w", err)
		}

		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("toolchain: mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("toolchain: mkdir %s: %w", filepath.Dir(target), err)
			}
			if err := writeFile(target, tr, os.FileMode(hdr.Mode&0o777)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("toolchain: mkdir %s: %w", filepath.Dir(target), err)
			}
			if _, err := safeJoin(filepath.Dir(target), hdr.Linkname); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("toolchain: symlink %s: %w", target, err)
			}
		default:
			// Devices, fifos, etc. are not expected in a Go distribution
			// archive; skip rather than fail.
		}
	}
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("toolchain: create %s: %w", target, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return fmt.Errorf("toolchain: write %s: %w", target, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

// safeJoin joins dir and name, rejecting any name that would resolve outside
// dir (absolute paths, "..", or anything that normalizes past the top).
func safeJoin(dir, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("toolchain: unsafe absolute path in archive: %q", name)
	}
	target := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("toolchain: unsafe path in archive: %q", name)
	}
	return target, nil
}
