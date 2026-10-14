//go:build !darwin

package window

import "errors"

// ErrUnsupported is returned by Run on platforms other than darwin.
var ErrUnsupported = errors.New("window: native window is only supported on darwin")

// Run always fails on this platform; see ErrUnsupported.
func Run(opts Options) error {
	return ErrUnsupported
}

// Focus is a no-op on this platform.
func Focus() {}

// Navigate is a no-op on this platform.
func Navigate(url string) {}

// SetTitle is a no-op on this platform.
func SetTitle(title string) {}

// Quit is a no-op on this platform.
func Quit() {}
