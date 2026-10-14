//go:build darwin

package window

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

func init() {
	// Cocoa's run loop must own the OS thread it starts on for the life of
	// the process. Run (called from main) makes that thread the one this
	// goroutine is locked to; callers do their other work (e.g. an HTTP
	// server) in goroutines and call Run last, from main.
	runtime.LockOSThread()
}

var (
	mu            sync.Mutex
	onPreferences func()
	onQuit        func()
)

// Run creates NSApplication, one window with a WKWebView loading opts.URL,
// and runs the Cocoa event loop on the calling (main) OS thread. It blocks
// until the app quits, via ⌘Q or by closing the window (⌘W).
func Run(opts Options) error {
	if opts.URL == "" {
		return errors.New("window: Options.URL is required")
	}

	mu.Lock()
	onPreferences = opts.OnPreferences
	onQuit = opts.OnQuit
	mu.Unlock()

	title := opts.Title
	if title == "" {
		title = "flowreview"
	}

	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cURL := C.CString(opts.URL)
	defer C.free(unsafe.Pointer(cURL))

	var devTools C.int
	if os.Getenv("FLOWREVIEW_DEVTOOLS") == "1" {
		devTools = 1
	}

	C.windowRun(cTitle, cURL, C.int(opts.Width), C.int(opts.Height), devTools)
	return nil
}

// Focus brings the running window to front. Safe to call from any goroutine.
func Focus() {
	C.windowFocus()
}

// Navigate loads a new URL in the existing webview. Safe to call from any
// goroutine.
func Navigate(url string) {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.windowNavigate(cURL)
}

// SetTitle updates the window's title. Safe to call from any goroutine.
func SetTitle(title string) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	C.windowSetTitle(cTitle)
}

// Quit terminates the application. Safe to call from any goroutine.
func Quit() {
	C.windowQuit()
}

//export flowreviewPreferencesCallback
func flowreviewPreferencesCallback() {
	mu.Lock()
	cb := onPreferences
	mu.Unlock()
	if cb != nil {
		cb()
	}
}

//export flowreviewWillTerminateCallback
func flowreviewWillTerminateCallback() {
	mu.Lock()
	cb := onQuit
	mu.Unlock()
	if cb != nil {
		cb()
	}
}
