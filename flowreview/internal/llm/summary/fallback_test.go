package summary

import (
	"fmt"
	"strings"
	"testing"
)

func TestFallbackCountsAndKind(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		kind    string
		added   int
		removed int
	}{
		{
			name:    "behavior default",
			in:      Input{Path: "internal/core/diff/hunk.go", Language: "go", Hunk: " \tctx\n+\treturn x\n+\treturn z\n-\treturn y\n"},
			kind:    "behavior",
			added:   2,
			removed: 1,
		},
		{
			name:    "go test file",
			in:      Input{Path: "internal/core/diff/hunk_test.go", Language: "go", Hunk: "+func TestX(t *testing.T) {}\n"},
			kind:    "test",
			added:   1,
			removed: 0,
		},
		{
			name:    "yaml config",
			in:      Input{Path: "deploy/values.yaml", Language: "yaml", Hunk: "+replicas: 2\n-replicas: 1\n"},
			kind:    "config",
			added:   1,
			removed: 1,
		},
		{
			name:    "gherkin feature counts as test",
			in:      Input{Path: "features/login.feature", Language: "gherkin", Hunk: "+Scenario: ok\n"},
			kind:    "test",
			added:   1,
			removed: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Fallback("key", "hunk1", c.in)
			if !s.Fallback {
				t.Error("want Fallback=true")
			}
			if s.Confidence != 0 {
				t.Errorf("Confidence = %v, want 0", s.Confidence)
			}
			if s.Kind != c.kind {
				t.Errorf("Kind = %q, want %q", s.Kind, c.kind)
			}
			want := fmt.Sprintf("+%d/-%d lines", c.added, c.removed)
			if !strings.Contains(s.What, want) {
				t.Errorf("What = %q, want it to contain %q", s.What, want)
			}
		})
	}
}

func TestFallbackUsesEnclosingFunctionName(t *testing.T) {
	in := Input{
		Path:              "internal/core/diff/hunk.go",
		Language:          "go",
		Hunk:              "+\treturn x\n",
		EnclosingFunction: "func parseHunk(body string) (*Hunk, error) {\n\treturn nil, nil\n}",
	}
	s := Fallback("key", "hunk1", in)
	if !strings.Contains(s.What, "parseHunk") {
		t.Errorf("What = %q, want it to name the enclosing function parseHunk", s.What)
	}
}

func TestFallbackFallsBackToBaseName(t *testing.T) {
	in := Input{Path: "a/b/c.yaml", Language: "yaml", Hunk: "+x: 1\n"}
	s := Fallback("key", "hunk1", in)
	if !strings.Contains(s.What, "c.yaml") {
		t.Errorf("What = %q, want it to name the file c.yaml", s.What)
	}
}
