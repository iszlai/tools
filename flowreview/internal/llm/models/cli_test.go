package models

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCLI(t *testing.T) {
	content, sum := fakeGGUF(2_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	withTestCatalog(t, Entry{
		Name:     "cli-model",
		Display:  "CLI Model",
		URL:      srv.URL,
		SHA256:   sum,
		Size:     int64(len(content)),
		Licence:  "Test",
		MinRAMGB: 1,
		Tier:     TierSmall,
	})

	home := t.TempDir()
	t.Setenv("FLOWREVIEW_HOME", home)

	var stdout, stderr bytes.Buffer
	if code := RunCLI([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list exited %d, stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("cli-model")) {
		t.Errorf("list output missing the catalog entry:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := RunCLI([]string{"install", "cli-model", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("install exited %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "models", "cli-model.gguf")); err != nil {
		t.Fatalf("model not installed: %v", err)
	}

	stdout.Reset()
	if code := RunCLI([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status exited %d, stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("cli-model")) {
		t.Errorf("status output missing the installed model:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := RunCLI([]string{"rm", "cli-model"}, &stdout, &stderr); code != 0 {
		t.Fatalf("rm exited %d, stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "models", "cli-model.gguf")); !os.IsNotExist(err) {
		t.Error("model should be gone after rm")
	}
}

func TestRunCLIUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCLI(nil, &stdout, &stderr); code != 2 {
		t.Errorf("RunCLI(nil) = %d, want 2", code)
	}
	if code := RunCLI([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Errorf("RunCLI(bogus) = %d, want 2", code)
	}
}
