package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ErrDeclined is returned by Install when Confirm rejects the disclosure. No
// network request is made before Confirm is asked.
var ErrDeclined = errors.New("models: download declined")

// ErrUnknownModel is returned when name is not in the catalog.
var ErrUnknownModel = errors.New("models: unknown model")

// partSuffix marks a download in progress; Install resumes from one of these
// via an HTTP Range request rather than starting over.
const partSuffix = ".part"

// Disclosure is what the user is shown before a model downloads: PLAN.md §3
// requires size, licence and source be disclosed, and RAM requirement with
// it, before any byte is fetched.
type Disclosure struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Licence    string `json:"licence"`
	LicenceURL string `json:"licence_url"`
	MinRAMGB   int    `json:"min_ram_gb"`
	URL        string `json:"url"`
}

// Status is one catalog entry's install state.
type Status struct {
	Entry     Entry `json:"entry"`
	Installed bool  `json:"installed"`
	SizeBytes int64 `json:"size_bytes,omitempty"` // actual size on disk, when installed
}

// Manager installs and removes models under Root, a models/ directory
// (PLAN.md §3: "models/<name>.gguf" under the data root).
type Manager struct {
	Root string // directory containing installed .gguf files

	// HTTP is the client used for downloads. Nil uses http.DefaultClient.
	HTTP *http.Client

	// Confirm shows a Disclosure and reports whether the user agreed to
	// download it. Required: Install returns an error without it.
	Confirm func(Disclosure) bool
}

func (m *Manager) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return http.DefaultClient
}

func (m *Manager) finalPath(name string) string { return filepath.Join(m.Root, name+".gguf") }
func (m *Manager) partPath(name string) string  { return m.finalPath(name) + partSuffix }

// installedSize returns the size of the installed file for entry, or 0 if it
// is not installed.
func (m *Manager) installedSize(name string) int64 {
	fi, err := os.Stat(m.finalPath(name))
	if err != nil {
		return 0
	}
	return fi.Size()
}

// List returns the whole catalog with each entry's install state.
func (m *Manager) List() []Status {
	out := make([]Status, 0, len(Catalog))
	for _, e := range Catalog {
		size := m.installedSize(e.Name)
		out = append(out, Status{Entry: e, Installed: size > 0, SizeBytes: size})
	}
	return out
}

// Status reports only the installed models, for `flowreview model status`.
func (m *Manager) Status() []Status {
	var out []Status
	for _, s := range m.List() {
		if s.Installed {
			out = append(out, s)
		}
	}
	return out
}

// Install downloads name: it discloses size, licence and RAM requirement,
// asks Confirm, and only then starts fetching. The download is resumable —
// an interrupted attempt leaves a "<name>.gguf.part" that the next Install
// call continues via an HTTP Range request — and is sha256-verified before
// being renamed into place; a checksum mismatch deletes the partial file and
// returns an error, so a bad download never gets mistaken for an install.
// Calling Install on an already-installed model is a no-op.
func (m *Manager) Install(ctx context.Context, name string, progress func(done, total int64)) error {
	entry, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownModel, name)
	}
	if m.Confirm == nil {
		return errors.New("models: Confirm is required")
	}
	if m.installedSize(name) > 0 {
		return nil
	}

	disclosure := Disclosure{
		Name:       entry.Name,
		Size:       entry.Size,
		Licence:    entry.Licence,
		LicenceURL: entry.LicenceURL,
		MinRAMGB:   entry.MinRAMGB,
		URL:        entry.URL,
	}
	if !m.Confirm(disclosure) {
		return ErrDeclined
	}

	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return fmt.Errorf("models: %w", err)
	}
	return m.download(ctx, entry, progress)
}

func (m *Manager) download(ctx context.Context, entry Entry, progress func(done, total int64)) error {
	partPath := m.partPath(entry.Name)

	var offset int64
	if fi, err := os.Stat(partPath); err == nil {
		offset = fi.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("models: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := m.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("models: download %s: %w", entry.Name, err)
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		// The server ignored the Range (or none was requested): the body is
		// the whole file from byte 0, so restart the part file.
		offset = 0
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("models: download %s: unexpected status %s", entry.Name, resp.Status)
	}

	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}

	written := offset
	if progress != nil {
		progress(written, entry.Size)
	}
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("models: %w", werr)
			}
			written += int64(n)
			if progress != nil {
				progress(written, entry.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			// Leave the part file in place: the next Install resumes it.
			return fmt.Errorf("models: download %s: %w", entry.Name, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("models: %w", err)
	}

	sum, err := sha256File(partPath)
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}
	if sum != entry.SHA256 {
		os.Remove(partPath)
		return fmt.Errorf("models: %s: checksum mismatch: got %s, want %s", entry.Name, sum, entry.SHA256)
	}
	return os.Rename(partPath, m.finalPath(entry.Name))
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Remove deletes an installed model. Removing one that is not installed, or
// not in the catalog, is not an error — the end state (not present) is
// already true.
func (m *Manager) Remove(name string) error {
	if err := os.Remove(m.finalPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("models: %w", err)
	}
	return nil
}
