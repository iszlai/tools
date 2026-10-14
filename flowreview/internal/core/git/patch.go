package git

import (
	"bytes"
	"fmt"
	"os"
)

// PatchFile reads path and returns its bytes, after a light sanity check that
// it looks like a unified diff. The FR-3 parser does the real parsing; this
// just turns "pointed at the wrong file" into a clear error here instead of a
// confusing one there.
func PatchFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !looksLikeDiff(data) {
		return nil, fmt.Errorf("git: %s does not look like a diff", path)
	}
	return data, nil
}

// looksLikeDiff checks for markers present in any unified diff git or
// `diff -u` produces.
func looksLikeDiff(data []byte) bool {
	if bytes.Contains(data, []byte("diff --git ")) {
		return true
	}
	if bytes.Contains(data, []byte("@@ -")) {
		return true
	}
	// Plain `diff -u` output has no git header; both markers must be present.
	return bytes.Contains(data, []byte("--- ")) && bytes.Contains(data, []byte("+++ "))
}
