package window

import "testing"

// Options is a plain value type; this just pins the zero-value contract
// callers rely on (no field defaults to something surprising).
func TestOptionsZeroValue(t *testing.T) {
	var opts Options
	if opts.Title != "" || opts.URL != "" {
		t.Fatalf("zero Options should have empty Title/URL, got %+v", opts)
	}
	if opts.Width != 0 || opts.Height != 0 {
		t.Fatalf("zero Options should have 0 Width/Height, got %+v", opts)
	}
	if opts.OnPreferences != nil {
		t.Fatalf("zero Options should have nil OnPreferences")
	}
}
