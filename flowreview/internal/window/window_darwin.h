#ifndef FLOWREVIEW_WINDOW_DARWIN_H
#define FLOWREVIEW_WINDOW_DARWIN_H

// windowRun creates the NSApplication, builds the menu bar, opens one window
// containing a WKWebView that loads url, and runs the Cocoa event loop. It
// blocks until the app quits. width/height of 0 fall back to a default
// size; devTools nonzero enables the WebKit Web Inspector.
void windowRun(const char *title, const char *url, int width, int height, int devTools);

// windowFocus, windowNavigate, windowSetTitle and windowQuit act on the
// window windowRun created. Each is safe to call from any thread: it
// dispatches its actual work onto the main queue internally.
void windowFocus(void);
void windowNavigate(const char *url);
void windowSetTitle(const char *title);
void windowQuit(void);

#endif
