//go:build !darwin

package window

import "testing"

// On non-darwin platforms Run must fail clearly instead of hanging or
// crashing, and the other entry points must be harmless no-ops.
func TestNonDarwinStub(t *testing.T) {
	if err := Run(Options{URL: "http://127.0.0.1:0"}); err != ErrUnsupported {
		t.Fatalf("Run() = %v, want ErrUnsupported", err)
	}

	// These must not panic.
	Focus()
	Navigate("http://127.0.0.1:0")
	SetTitle("title")
	Quit()
}
