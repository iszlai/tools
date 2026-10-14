package git

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleUnifiedDiff = `diff --git a/a.txt b/a.txt
index e69de29..0000000 100644
--- a/a.txt
+++ b/a.txt
@@ -0,0 +1 @@
+hello
`

func TestPatchFile_valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "change.patch")
	if err := os.WriteFile(path, []byte(sampleUnifiedDiff), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := PatchFile(path)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if string(data) != sampleUnifiedDiff {
		t.Errorf("PatchFile returned different bytes than written")
	}
}

func TestPatchFile_plainDiffNoGitHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "change.patch")
	plain := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"
	if err := os.WriteFile(path, []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := PatchFile(path); err != nil {
		t.Fatalf("PatchFile on a plain diff -u patch: %v", err)
	}
}

func TestPatchFile_notADiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("just some notes, not a diff at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := PatchFile(path); err == nil {
		t.Fatal("PatchFile on a non-diff file: want error, got nil")
	}
}

func TestPatchFile_missing(t *testing.T) {
	if _, err := PatchFile(filepath.Join(t.TempDir(), "missing.patch")); err == nil {
		t.Fatal("PatchFile on a missing file: want error, got nil")
	}
}
