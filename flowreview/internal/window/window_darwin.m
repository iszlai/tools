#import "window_darwin.h"
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Implemented in window_darwin.go (//export); called when the user picks
// Preferences… (⌘,) from the app menu.
extern void flowreviewPreferencesCallback(void);
// Implemented in window_darwin.go (//export); called just before exit().
extern void flowreviewWillTerminateCallback(void);

// The window and webview live for the process's whole life, so they are
// simple globals: this tool never has more than one of either, and nothing
// ever needs to release them before the process exits.
static NSWindow *gWindow;
static WKWebView *gWebView;

@interface FlowreviewAppDelegate : NSObject <NSApplicationDelegate>
@end

@implementation FlowreviewAppDelegate

- (void)showPreferences:(id)sender {
    flowreviewPreferencesCallback();
}

// A single-window tool: closing its only window (⌘W) should quit the app
// rather than leave a menu-bar-only process behind.
- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)sender {
    return YES;
}

- (void)applicationWillTerminate:(NSNotification *)notification {
    flowreviewWillTerminateCallback();
}

@end

static NSMenu *buildMainMenu(id target) {
    NSString *appName = [[NSProcessInfo processInfo] processName];
    NSMenu *mainMenu = [[NSMenu alloc] init];

    // App menu. AppKit always displays the running app's name here,
    // regardless of this item's own title.
    NSMenuItem *appMenuItem = [[NSMenuItem alloc] initWithTitle:@"" action:NULL keyEquivalent:@""];
    NSMenu *appMenu = [[NSMenu alloc] init];
    [appMenu addItemWithTitle:[@"About " stringByAppendingString:appName]
                       action:@selector(orderFrontStandardAboutPanel:)
                keyEquivalent:@""];
    [appMenu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *prefsItem = [appMenu addItemWithTitle:@"Preferences…"
                                                action:@selector(showPreferences:)
                                         keyEquivalent:@","];
    [prefsItem setTarget:target];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItemWithTitle:[@"Hide " stringByAppendingString:appName]
                        action:@selector(hide:)
                 keyEquivalent:@"h"];
    NSMenuItem *hideOthers = [appMenu addItemWithTitle:@"Hide Others"
                                                 action:@selector(hideOtherApplications:)
                                          keyEquivalent:@"h"];
    [hideOthers setKeyEquivalentModifierMask:(NSEventModifierFlagCommand | NSEventModifierFlagOption)];
    [appMenu addItemWithTitle:@"Show All" action:@selector(unhideAllApplications:) keyEquivalent:@""];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItemWithTitle:[@"Quit " stringByAppendingString:appName]
                        action:@selector(terminate:)
                 keyEquivalent:@"q"];
    [appMenuItem setSubmenu:appMenu];
    [mainMenu addItem:appMenuItem];

    // Edit menu. cut:/copy:/paste:/selectAll:/undo:/redo: need no explicit
    // target — WebKit answers them itself, via the standard responder
    // chain, for whichever text field in the web UI is first responder.
    NSMenuItem *editMenuItem = [[NSMenuItem alloc] initWithTitle:@"Edit" action:NULL keyEquivalent:@""];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"Edit"];
    [editMenu addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
    NSMenuItem *redoItem = [editMenu addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"z"];
    [redoItem setKeyEquivalentModifierMask:(NSEventModifierFlagCommand | NSEventModifierFlagShift)];
    [editMenu addItem:[NSMenuItem separatorItem]];
    [editMenu addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
    [editMenu addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
    [editMenu addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
    [editMenu addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
    [editMenuItem setSubmenu:editMenu];
    [mainMenu addItem:editMenuItem];

    // Window menu. Close quits, since this tool only ever has one window
    // (see applicationShouldTerminateAfterLastWindowClosed above).
    NSMenuItem *windowMenuItem = [[NSMenuItem alloc] initWithTitle:@"Window" action:NULL keyEquivalent:@""];
    NSMenu *windowMenu = [[NSMenu alloc] initWithTitle:@"Window"];
    [windowMenu addItemWithTitle:@"Close" action:@selector(performClose:) keyEquivalent:@"w"];
    [windowMenuItem setSubmenu:windowMenu];
    [mainMenu addItem:windowMenuItem];
    [NSApp setWindowsMenu:windowMenu];

    return mainMenu;
}

void windowRun(const char *title, const char *url, int width, int height, int devTools) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];

        FlowreviewAppDelegate *delegate = [[FlowreviewAppDelegate alloc] init];
        [app setDelegate:delegate];
        [app setMainMenu:buildMainMenu(delegate)];

        CGFloat w = width > 0 ? width : 1024;
        CGFloat h = height > 0 ? height : 720;
        NSRect frame = NSMakeRect(0, 0, w, h);

        NSWindow *window = [[NSWindow alloc]
            initWithContentRect:frame
                      styleMask:(NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                                 NSWindowStyleMaskResizable | NSWindowStyleMaskMiniaturizable)
                        backing:NSBackingStoreBuffered
                          defer:NO];
        [window setTitle:[NSString stringWithUTF8String:title]];
        [window center];
        // There is no "reopen" path for this tool, so keep the NSWindow
        // object alive even after the user closes it; closing just quits
        // the whole app (see applicationShouldTerminateAfterLastWindowClosed).
        [window setReleasedWhenClosed:NO];

        WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
        if (devTools) {
            // developerExtrasEnabled predates WKWebView's public
            // isInspectable (macOS 13.3+); set via KVC so this keeps
            // working on older macOS too.
            @try {
                [config.preferences setValue:@YES forKey:@"developerExtrasEnabled"];
            } @catch (NSException *exception) {
                // Best-effort only: ignore if a future WebKit drops the key.
            }
        }

        WKWebView *webView = [[WKWebView alloc] initWithFrame:frame configuration:config];
        [window setContentView:webView];

        gWindow = window;
        gWebView = webView;

        NSURL *nsurl = [NSURL URLWithString:[NSString stringWithUTF8String:url]];
        if (nsurl != nil) {
            [webView loadRequest:[NSURLRequest requestWithURL:nsurl]];
        }

        [window makeKeyAndOrderFront:nil];
        [app activateIgnoringOtherApps:YES];

        [app run];
    }
}

void windowFocus(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp activateIgnoringOtherApps:YES];
        [gWindow makeKeyAndOrderFront:nil];
    });
}

void windowNavigate(const char *url) {
    // Convert before dispatching: the C string is only valid for the
    // duration of this call, but the block runs later on the main queue.
    NSString *s = [NSString stringWithUTF8String:url];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSURL *nsurl = [NSURL URLWithString:s];
        if (nsurl != nil && gWebView != nil) {
            [gWebView loadRequest:[NSURLRequest requestWithURL:nsurl]];
        }
    });
}

void windowSetTitle(const char *title) {
    NSString *s = [NSString stringWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        [gWindow setTitle:s];
    });
}

void windowQuit(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp terminate:nil];
    });
}
