// Package window shows flowreview's UI in a single native window: a
// WKWebView pointed at the URL the HTTP server (built separately) serves.
// It is a thin shell, not a UI toolkit — the only state it owns is the one
// window and webview a running flowreview process ever has.
//
// Run owns the process's main thread for as long as the app runs, so it
// must be called from main, after any server setup has started in a
// goroutine:
//
//	go runServer()
//	if err := window.Run(window.Options{URL: serverURL}); err != nil {
//	    log.Fatal(err)
//	}
//
// Focus, Navigate, SetTitle and Quit act on the window Run created and are
// safe to call from any goroutine while Run is blocked running the event
// loop.
package window

// Options configures the window Run creates.
type Options struct {
	// Title is the window's title bar text. Empty falls back to a default.
	Title string
	// URL is the page the webview loads on launch.
	URL string
	// Width and Height are the window's content size in points. Zero (the
	// default) falls back to a reasonable size.
	Width, Height int
	// OnPreferences is called when the user picks Preferences… (⌘,) from
	// the app menu. Nil leaves the menu item with no effect.
	OnPreferences func()
	// OnQuit is called on the main thread just before the app exits.
	// AppKit's terminate: ends the process with exit(), so deferred calls
	// in main never run: cleanup that must happen (stopping child
	// processes) goes here.
	OnQuit func()
}
