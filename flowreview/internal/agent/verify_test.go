package agent

import (
	"fmt"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// fileReader stubs Verify's read callback from an in-memory set of files, so
// these tests exercise the matching logic without touching disk.
func fileReader(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("open %s: no such file", path)
		}
		return []byte(content), nil
	}
}

func TestVerify(t *testing.T) {
	read := fileReader(map[string]string{
		"pkg/foo.go": "package pkg\n\nfunc Foo() error {\n\treturn nil // bug: ignoring error\n}\n",
	})

	tests := []struct {
		name     string
		evidence model.Evidence
		wantOK   bool
		wantLine int // only checked when wantOK
	}{
		{
			name:     "exact line match",
			evidence: model.Evidence{Path: "pkg/foo.go", Line: 4, Excerpt: "return nil // bug: ignoring error"},
			wantOK:   true,
			wantLine: 4,
		},
		{
			name:     "whitespace trimmed on both sides",
			evidence: model.Evidence{Path: "pkg/foo.go", Line: 4, Excerpt: "  return nil // bug: ignoring error  \n"},
			wantOK:   true,
			wantLine: 4,
		},
		{
			name:     "shifted line within window",
			evidence: model.Evidence{Path: "pkg/foo.go", Line: 2, Excerpt: "return nil // bug: ignoring error"},
			wantOK:   true,
			wantLine: 4,
		},
		{
			name:     "shifted past window is not found",
			evidence: model.Evidence{Path: "pkg/foo.go", Line: 1, Excerpt: "return nil // bug: ignoring error"},
			wantOK:   false,
		},
		{
			name:     "hallucinated excerpt dropped",
			evidence: model.Evidence{Path: "pkg/foo.go", Line: 4, Excerpt: "this line does not exist anywhere"},
			wantOK:   false,
		},
		{
			name:     "missing file",
			evidence: model.Evidence{Path: "pkg/missing.go", Line: 1, Excerpt: "package pkg"},
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &model.AgentFinding{Evidence: []model.Evidence{tt.evidence}}
			ok := Verify(f, read)

			if ok != tt.wantOK {
				t.Fatalf("Verify() = %v, want %v (verification=%q)", ok, tt.wantOK, f.Verification)
			}
			if f.Verification == "" {
				t.Errorf("want a non-empty Verification message")
			}
			if tt.wantOK && f.Evidence[0].Line != tt.wantLine {
				t.Errorf("corrected line = %d, want %d", f.Evidence[0].Line, tt.wantLine)
			}
		})
	}
}

func TestVerifyNoEvidence(t *testing.T) {
	f := &model.AgentFinding{}
	if Verify(f, fileReader(nil)) {
		t.Fatal("Verify() with no evidence = true, want false")
	}
	if f.Verification == "" {
		t.Error("want a non-empty Verification message")
	}
}

func TestVerifyAcceptsIfAnyEvidenceVerifies(t *testing.T) {
	read := fileReader(map[string]string{
		"pkg/foo.go": "package pkg\nfunc Foo() {}\n",
	})
	f := &model.AgentFinding{Evidence: []model.Evidence{
		{Path: "pkg/foo.go", Line: 2, Excerpt: "func Foo() {}"},
		{Path: "pkg/foo.go", Line: 2, Excerpt: "this was never here"},
	}}

	if !Verify(f, read) {
		t.Fatalf("Verify() = false, want true (one of two evidence lines is real); verification=%q", f.Verification)
	}
	if f.Evidence[0].Line != 2 {
		t.Errorf("verified evidence line = %d, want 2", f.Evidence[0].Line)
	}
}
