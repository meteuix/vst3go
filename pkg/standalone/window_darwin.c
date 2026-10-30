//go:build standalone && darwin

#import <Cocoa/Cocoa.h>

#include "_cgo_export.h"
#include "host.h"
#include "platform.h"

static NSWindow* window;
static NSView* editorView;   // the plugin editor's parent, below the toolbar
static NSView* toolbarView;  // the toolbar's parent, along the top
static uint32_t toolbarHeight;
static bool quitting;

@interface VST3GoStandalone : NSObject <NSApplicationDelegate, NSWindowDelegate>
@end

@implementation VST3GoStandalone
- (BOOL)windowShouldClose:(NSWindow*)sender {
    (void)sender;
    vst3go_sa_quit();
    return NO;
}

// User drags snap to the sizes the editor accepts.
- (NSSize)windowWillResize:(NSWindow*)sender toSize:(NSSize)frameSize {
    NSRect content = [sender contentRectForFrameRect:NSMakeRect(0, 0, frameSize.width, frameSize.height)];
    uint32_t width = (uint32_t)content.size.width;
    uint32_t height = content.size.height > toolbarHeight ? (uint32_t)content.size.height - toolbarHeight : 0;
    vst3go_sa_gui_adjust(&width, &height);
    return [sender frameRectForContentRect:NSMakeRect(0, 0, width, height + toolbarHeight)].size;
}

- (void)windowDidResize:(NSNotification*)notification {
    (void)notification;
    NSSize size = [editorView frame].size;
    vst3go_sa_gui_set_size((uint32_t)size.width, (uint32_t)size.height);
    goStandaloneToolbarSize((uint32_t)size.width, toolbarHeight);
}

- (void)openSettings:(id)sender {
    (void)sender;
    goStandaloneOpenSettings();
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication*)sender {
    (void)sender;
    vst3go_sa_quit();
    return NSTerminateCancel;
}

- (void)tick:(NSTimer*)timer {
    (void)timer;
    goStandaloneTick();
}
@end

static VST3GoStandalone* delegate;

static void setup_app(const char* title) {
    if (delegate) return;
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    delegate = [[VST3GoStandalone alloc] init];
    [NSApp setDelegate:delegate];

    NSString* name = [NSString stringWithUTF8String:title];
    NSMenu* bar = [[NSMenu alloc] init];
    NSMenuItem* appItem = [[NSMenuItem alloc] init];
    [bar addItem:appItem];
    NSMenu* appMenu = [[NSMenu alloc] init];
    NSMenuItem* settings = [appMenu addItemWithTitle:@"Settings…" action:@selector(openSettings:) keyEquivalent:@","];
    [settings setTarget:delegate];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItemWithTitle:[@"Hide " stringByAppendingString:name] action:@selector(hide:) keyEquivalent:@"h"];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItemWithTitle:[@"Quit " stringByAppendingString:name] action:@selector(terminate:) keyEquivalent:@"q"];
    [appItem setSubmenu:appMenu];
    [NSApp setMainMenu:bar];
}

const char* vst3go_sa_window_api(void) { return CLAP_WINDOW_API_COCOA; }

// Cocoa editors work in logical pixels.
double vst3go_sa_window_scale(void) { return 0; }

bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, uint32_t toolbar,
                           clap_window_t* out, void** toolbarParent) {
    @autoreleasepool {
        toolbarHeight = toolbar;
        setup_app(title);
        NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable;
        if (resizable) style |= NSWindowStyleMaskResizable;
        window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, width, height + toolbar)
                                             styleMask:style
                                               backing:NSBackingStoreBuffered
                                                 defer:NO];
        if (!window) return false;
        [window setReleasedWhenClosed:NO];
        [window setTitle:[NSString stringWithUTF8String:title]];
        // Cocoa's origin is bottom left: the editor fills the bottom, the
        // toolbar keeps its height along the top.
        editorView = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, width, height)];
        [editorView setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
        toolbarView = [[NSView alloc] initWithFrame:NSMakeRect(0, height, width, toolbar)];
        [toolbarView setAutoresizingMask:NSViewWidthSizable | NSViewMinYMargin];
        [[window contentView] addSubview:editorView];
        [[window contentView] addSubview:toolbarView];
        [window setDelegate:delegate];
        [window center];
        [window makeKeyAndOrderFront:nil];
        out->api = CLAP_WINDOW_API_COCOA;
        out->cocoa = (__bridge void*)editorView;
        *toolbarParent = (__bridge void*)toolbarView;
        return true;
    }
}

void vst3go_sa_window_resize(uint32_t width, uint32_t height) {
    if (window) [window setContentSize:NSMakeSize(width, height + toolbarHeight)];
}

void vst3go_sa_window_close(void) {
    if (!window) return;
    [window setDelegate:nil];
    [window orderOut:nil];
    [window close];
    window = nil;
    editorView = toolbarView = nil;
}

void vst3go_sa_run(uint32_t tick_ms) {
    @autoreleasepool {
        quitting = false;
        NSTimer* timer = [NSTimer timerWithTimeInterval:tick_ms / 1000.0 target:delegate selector:@selector(tick:) userInfo:nil repeats:YES];
        // Common modes keep the tick running during live resizes and menus.
        [[NSRunLoop currentRunLoop] addTimer:timer forMode:NSRunLoopCommonModes];
        [NSApp activateIgnoringOtherApps:YES];
        [NSApp run];
        [timer invalidate];
    }
}

void vst3go_sa_quit(void) {
    if (quitting || !NSApp) return;
    quitting = true;
    [NSApp stop:nil];
    // stop: takes effect after the next event.
    NSEvent* wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0
                                      timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
    [NSApp postEvent:wake atStart:YES];
}

bool vst3go_sa_timers_supported(void) { return false; }
bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id) { (void)period_ms; (void)id; return false; }
bool vst3go_sa_timer_unregister(clap_id id) { (void)id; return false; }
