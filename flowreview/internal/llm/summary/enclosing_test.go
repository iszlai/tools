package summary

import (
	"context"
	"strings"
	"testing"
)

func TestEnclosingFunctionGo(t *testing.T) {
	src := []byte("package p\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n")
	// line 4 ("return a + b") is inside Add.
	got := EnclosingFunction(context.Background(), "p.go", src, 4)
	if !strings.Contains(got, "func Add(a, b int) int {") {
		t.Errorf("got %q, want it to contain Add's signature", got)
	}
	if strings.Contains(got, "func Sub") {
		t.Errorf("got %q, want it to not spill into Sub", got)
	}
}

func TestEnclosingFunctionNoMatch(t *testing.T) {
	src := []byte("package p\n\nvar x = 1\n")
	got := EnclosingFunction(context.Background(), "p.go", src, 3)
	if got != "" {
		t.Errorf("got %q, want \"\" for a line outside any function", got)
	}
}

func TestEnclosingFunctionUnsupportedLanguage(t *testing.T) {
	got := EnclosingFunction(context.Background(), "data.csv", []byte("a,b,c\n1,2,3\n"), 2)
	if got != "" {
		t.Errorf("got %q, want \"\" for a language with no provider", got)
	}
}

func TestEnclosingName(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"func parseHunk(body string) (*Hunk, error) {", "parseHunk"},
		{"func (r *Repo) Open(ctx context.Context) error {", "Open"},
		{"function computeTotal(items) {", "computeTotal"},
		{"def compute_total(items):", "compute_total"},
		{"class Widget:", "Widget"},
		{"\tv0 := 0", ""},
	}
	for _, c := range cases {
		if got := enclosingName(c.text); got != c.want {
			t.Errorf("enclosingName(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
