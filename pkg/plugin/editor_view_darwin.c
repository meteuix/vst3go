//go:build darwin

// The editor view on macOS (an NSView) and iOS (a UIView, for AUv3
// extensions): a WKWebView showing the editor page.

#include <TargetConditionals.h>
#if TARGET_OS_OSX
#import <Cocoa/Cocoa.h>
typedef NSView VST3GoNativeView;
#define VST3GO_AUTORESIZE (NSViewWidthSizable | NSViewHeightSizable)
#else
#import <UIKit/UIKit.h>
typedef UIView VST3GoNativeView;
#define VST3GO_AUTORESIZE (UIViewAutoresizingFlexibleWidth | UIViewAutoresizingFlexibleHeight)
#endif
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <WebKit/WebKit.h>

#include "editor_view.h"
#include <dlfcn.h>
#include <objc/runtime.h>
#include <stdlib.h>
#include <string.h>

typedef struct VST3GoEditorView VST3GoEditorView;

typedef struct VST3GoEditorScaleSupport {
    Steinberg_IPlugViewContentScaleSupportVtbl* lpVtbl;
    VST3GoEditorView* owner;
} VST3GoEditorScaleSupport;

struct VST3GoEditorView {
    Steinberg_IPlugView view;
    VST3GoEditorScaleSupport scaleSupport;
    Steinberg_uint32 refCount;
    uintptr_t component;
    VST3GoNativeView* parentView;
    VST3GoNativeView* containerView;
    WKWebView* webView;
    NSObject<WKScriptMessageHandler>* messageHandler;
    struct Steinberg_IPlugFrame* frame;
    Steinberg_ViewRect size;
    float scaleFactor;
    Steinberg_TBool attached;
};

static Steinberg_tresult editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);
static Steinberg_uint32 editorView_addRef(void* thisInterface);
static Steinberg_uint32 editorView_release(void* thisInterface);
static Steinberg_tresult editorView_isPlatformTypeSupported(void* thisInterface, Steinberg_FIDString type);
static Steinberg_tresult editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type);
static Steinberg_tresult editorView_removed(void* thisInterface);
static Steinberg_tresult editorView_onWheel(void* thisInterface, float distance);
static Steinberg_tresult editorView_onKeyDown(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers);
static Steinberg_tresult editorView_onKeyUp(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers);
static Steinberg_tresult editorView_getSize(void* thisInterface, Steinberg_ViewRect* size);
static Steinberg_tresult editorView_onSize(void* thisInterface, Steinberg_ViewRect* size);
static Steinberg_tresult editorView_onFocus(void* thisInterface, Steinberg_TBool state);
static Steinberg_tresult editorView_setFrame(void* thisInterface, struct Steinberg_IPlugFrame* frame);
static Steinberg_tresult editorView_canResize(void* thisInterface);
static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect);
static Steinberg_tresult editorScale_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);
static Steinberg_uint32 editorScale_addRef(void* thisInterface);
static Steinberg_uint32 editorScale_release(void* thisInterface);
static Steinberg_tresult editorScale_setContentScaleFactor(void* thisInterface, Steinberg_IPlugViewContentScaleSupport_ScaleFactor factor);

static Steinberg_IPlugViewContentScaleSupportVtbl editorScaleVtbl = {
    editorScale_queryInterface,
    editorScale_addRef,
    editorScale_release,
    editorScale_setContentScaleFactor
};

static Steinberg_IPlugViewVtbl editorViewVtbl = {
    editorView_queryInterface,
    editorView_addRef,
    editorView_release,
    editorView_isPlatformTypeSupported,
    editorView_attached,
    editorView_removed,
    editorView_onWheel,
    editorView_onKeyDown,
    editorView_onKeyUp,
    editorView_getSize,
    editorView_onSize,
    editorView_onFocus,
    editorView_setFrame,
    editorView_canResize,
    editorView_checkSizeConstraint
};

// The script message handler class is registered at run time under a name
// unique to this binary: several vst3go plugins in one host process would
// otherwise share one Objective-C class, whose methods call into the Go
// runtime of whichever binary registered it first.
static Ivar messageHandlerOwner;

static void messageHandlerReceive(id self, SEL cmd, WKUserContentController* controller, WKScriptMessage* message) {
    (void)cmd;
    (void)controller;
    VST3GoEditorView* owner = *(VST3GoEditorView**)((char*)self + ivar_getOffset(messageHandlerOwner));
    if (!owner || ![message.body isKindOfClass:[NSDictionary class]]) {
        return;
    }

    NSData *json = [NSJSONSerialization dataWithJSONObject:message.body options:0 error:nil];
    if (!json) {
        return;
    }
    GoEditorMessage(owner->component, (char *)json.bytes, (int)json.length);
}

static NSObject<WKScriptMessageHandler>* newMessageHandler(VST3GoEditorView* owner) {
    static Class cls;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        Dl_info info;
        char name[96];
        uintptr_t image = dladdr((const void*)&newMessageHandler, &info) ? (uintptr_t)info.dli_fbase : (uintptr_t)&newMessageHandler;
        snprintf(name, sizeof(name), "VST3GoEditorMessageHandler_%lx", (unsigned long)image);
        cls = objc_allocateClassPair([NSObject class], name, 0);
        class_addIvar(cls, "owner", sizeof(void*), sizeof(void*) == 8 ? 3 : 2, "^v");
        class_addMethod(cls, @selector(userContentController:didReceiveScriptMessage:), (IMP)messageHandlerReceive, "v@:@@");
        class_addProtocol(cls, @protocol(WKScriptMessageHandler));
        objc_registerClassPair(cls);
        messageHandlerOwner = class_getInstanceVariable(cls, "owner");
    });
    NSObject<WKScriptMessageHandler>* handler = [[cls alloc] init];
    *(VST3GoEditorView**)((char*)handler + ivar_getOffset(messageHandlerOwner)) = owner;
    return handler;
}

static void clearMessageHandler(NSObject<WKScriptMessageHandler>* handler) {
    *(VST3GoEditorView**)((char*)handler + ivar_getOffset(messageHandlerOwner)) = NULL;
}

static VST3GoEditorView* editorViewFromInterface(void* thisInterface) {
    return (VST3GoEditorView*)thisInterface;
}

static NSString* editorViewHTML(VST3GoEditorView* view) {
    char* html = GoEditControllerGetEditorHTML(view->component);
    if (!html) {
        return nil;
    }

    NSString* result = [[NSString alloc] initWithUTF8String:html];
    free(html);
    return [result autorelease];
}

static void editorViewLoadContent(VST3GoEditorView* view) {
    if (!view->webView) {
        return;
    }

    NSString* html = editorViewHTML(view);
    if (!html) {
        html = @"<!doctype html><html><body style=\"background:#0b1020;color:#e2e8f0;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;padding:24px;\">Editor model unavailable.</body></html>";
    }

    [view->webView loadHTMLString:html baseURL:nil];
}

static void editorViewSetFrameFromParent(VST3GoEditorView* view) {
    if (!view->containerView || !view->parentView) {
        return;
    }

    view->containerView.frame = view->parentView.bounds;
    view->webView.frame = view->containerView.bounds;
}

#if TARGET_OS_OSX
const char* const VST3GoEditorPlatformType = "NSView";
#else
const char* const VST3GoEditorPlatformType = "UIView";
#endif

void* VST3GoCreateEditorView(uintptr_t component) {
    @autoreleasepool {
        VST3GoEditorView* view = (VST3GoEditorView*)calloc(1, sizeof(VST3GoEditorView));
        if (!view) {
            return NULL;
        }

        view->view.lpVtbl = &editorViewVtbl;
        view->scaleSupport.lpVtbl = &editorScaleVtbl;
        view->scaleSupport.owner = view;
        view->refCount = 1;
        view->component = component;
        view->size.left = 0;
        view->size.top = 0;
        GoEditorSize(component, &view->size.right, &view->size.bottom);
        view->scaleFactor = 1.0f;
        return view;
    }
}

void VST3GoEditorViewPost(void* thisInterface, const char* json) {
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view || !view->webView || !json) {
            return;
        }

        // %s would read the JSON in the system encoding, not UTF-8.
        NSString* message = [NSString stringWithUTF8String:json];
        if (!message) {
            return;
        }
        NSString* script = [NSString stringWithFormat:@"if (window.__vst3goReceive) { window.__vst3goReceive(%@); }", message];
        [view->webView evaluateJavaScript:script completionHandler:nil];
    }
}

static Steinberg_tresult editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }

    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (memcmp(iid, Steinberg_FUnknown_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_IPlugView_iid, sizeof(Steinberg_TUID)) == 0) {
        *obj = &view->view;
        editorView_addRef(view);
        return Steinberg_kResultOk;
    }

    if (memcmp(iid, Steinberg_IPlugViewContentScaleSupport_iid, sizeof(Steinberg_TUID)) == 0) {
        *obj = &view->scaleSupport;
        editorView_addRef(view);
        return Steinberg_kResultOk;
    }

    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_tresult editorScale_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return editorView_queryInterface(((VST3GoEditorScaleSupport*)thisInterface)->owner, iid, obj);
}

static Steinberg_uint32 editorScale_addRef(void* thisInterface) {
    return editorView_addRef(((VST3GoEditorScaleSupport*)thisInterface)->owner);
}

static Steinberg_uint32 editorScale_release(void* thisInterface) {
    return editorView_release(((VST3GoEditorScaleSupport*)thisInterface)->owner);
}

static Steinberg_tresult editorScale_setContentScaleFactor(void* thisInterface, Steinberg_IPlugViewContentScaleSupport_ScaleFactor factor) {
    VST3GoEditorView* view = ((VST3GoEditorScaleSupport*)thisInterface)->owner;
    if (!view || factor <= 0) {
        return Steinberg_kResultFalse;
    }

    view->scaleFactor = factor;
    return Steinberg_kResultOk;
}

static Steinberg_uint32 editorView_addRef(void* thisInterface) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    return ++view->refCount;
}

static Steinberg_uint32 editorView_release(void* thisInterface) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return 0;
    }

    if (view->refCount > 0) {
        view->refCount--;
    }

    if (view->refCount == 0) {
        GoEditControllerClearEditorView(view->component, view);
        if (view->webView) {
            [view->webView removeFromSuperview];
            [view->webView setNavigationDelegate:nil];
            [view->webView setUIDelegate:nil];
            [view->webView release];
        }
        if (view->messageHandler) {
            clearMessageHandler(view->messageHandler);
            [view->messageHandler release];
        }
        if (view->containerView) {
            [view->containerView removeFromSuperview];
            [view->containerView release];
        }
        free(view);
        return 0;
    }

    return view->refCount;
}

static Steinberg_tresult editorView_isPlatformTypeSupported(void* thisInterface, Steinberg_FIDString type) {
    (void)thisInterface;
    if (!type) {
        return Steinberg_kResultFalse;
    }

    return (strcmp(type, VST3GoEditorPlatformType) == 0) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type) {
    (void)type;
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view || !parent) {
            return Steinberg_kResultFalse;
        }

        VST3GoNativeView* parentView = (VST3GoNativeView*)parent;
        if (view->attached) {
            [view->containerView removeFromSuperview];
            [view->containerView release];
            [view->messageHandler release];
            [view->webView release];
            view->containerView = nil;
            view->messageHandler = nil;
            view->webView = nil;
        }

        view->parentView = parentView;
        view->containerView = [[VST3GoNativeView alloc] initWithFrame:parentView.bounds];
        view->containerView.autoresizingMask = VST3GO_AUTORESIZE;

        WKWebViewConfiguration* configuration = [[WKWebViewConfiguration alloc] init];
        WKUserContentController* contentController = [[WKUserContentController alloc] init];
        view->messageHandler = newMessageHandler(view);
        [contentController addScriptMessageHandler:view->messageHandler name:@"vst3go"];
#if !TARGET_OS_OSX
        // Lay the page out in points at the view's width, as on macOS,
        // instead of on a zoomed-out 980-pixel canvas.
        WKUserScript* viewport = [[WKUserScript alloc]
              initWithSource:@"var m = document.createElement('meta'); m.name = 'viewport';"
                             "m.content = 'width=device-width, initial-scale=1';"
                             "document.head.appendChild(m);"
               injectionTime:WKUserScriptInjectionTimeAtDocumentEnd
            forMainFrameOnly:YES];
        [contentController addUserScript:viewport];
        [viewport release];
#endif
        configuration.userContentController = contentController;

        view->webView = [[WKWebView alloc] initWithFrame:view->containerView.bounds configuration:configuration];
        view->webView.autoresizingMask = VST3GO_AUTORESIZE;
#if !TARGET_OS_OSX
        view->webView.scrollView.bounces = NO;
#endif
        [view->containerView addSubview:view->webView];
        [parentView addSubview:view->containerView];

        [contentController release];
        [configuration release];

        editorViewSetFrameFromParent(view);
        editorViewLoadContent(view);
        view->attached = 1;
        return Steinberg_kResultOk;
    }
}

static Steinberg_tresult editorView_removed(void* thisInterface) {
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view) {
            return Steinberg_kResultFalse;
        }

        if (view->webView) {
            [view->webView removeFromSuperview];
            [view->webView release];
            view->webView = nil;
        }

        if (view->messageHandler) {
            clearMessageHandler(view->messageHandler);
            [view->messageHandler release];
            view->messageHandler = nil;
        }

        if (view->containerView) {
            [view->containerView removeFromSuperview];
            [view->containerView release];
            view->containerView = nil;
        }

        view->attached = 0;
        view->parentView = nil;
        GoEditControllerClearEditorView(view->component, view);
        return Steinberg_kResultOk;
    }
}

static Steinberg_tresult editorView_onWheel(void* thisInterface, float distance) {
    (void)thisInterface;
    (void)distance;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_onKeyDown(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers) {
    (void)thisInterface;
    (void)key;
    (void)keyCode;
    (void)modifiers;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_onKeyUp(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers) {
    (void)thisInterface;
    (void)key;
    (void)keyCode;
    (void)modifiers;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_getSize(void* thisInterface, Steinberg_ViewRect* size) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !size) {
        return Steinberg_kResultFalse;
    }

    *size = view->size;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_onSize(void* thisInterface, Steinberg_ViewRect* size) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !size) {
        return Steinberg_kResultFalse;
    }

    view->size = *size;
    if (view->containerView) {
        CGFloat width = (CGFloat)(size->right - size->left);
        CGFloat height = (CGFloat)(size->bottom - size->top);
        view->containerView.frame = CGRectMake(0, 0, width, height);
        view->webView.frame = view->containerView.bounds;
    }
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_onFocus(void* thisInterface, Steinberg_TBool state) {
    (void)thisInterface;
    (void)state;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_setFrame(void* thisInterface, struct Steinberg_IPlugFrame* frame) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return Steinberg_kResultFalse;
    }

    view->frame = frame;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_canResize(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    return view && GoEditorCanResize(view->component) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !rect) {
        return Steinberg_kInvalidArgument;
    }
    Steinberg_int32 width = rect->right - rect->left, height = rect->bottom - rect->top;
    GoEditorConstrainSize(view->component, &width, &height);
    rect->right = rect->left + width;
    rect->bottom = rect->top + height;
    return Steinberg_kResultOk;
}

void VST3GoEditorViewRequestResize(void* thisInterface, int32_t width, int32_t height) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !view->frame) {
        return;
    }
    Steinberg_ViewRect rect = { 0, 0, width, height };
    view->frame->lpVtbl->resizeView(view->frame, (struct Steinberg_IPlugView*)&view->view, &rect);
}


// ---- open file dialog ----

// fileTypes is the content types of ';'-separated file name extensions,
// empty for any file.
static NSArray<UTType*>* fileTypes(const char* extensions) {
    NSMutableArray<UTType*>* types = [NSMutableArray array];
    NSString* list = extensions ? [NSString stringWithUTF8String:extensions] : nil;
    for (NSString* ext in [list componentsSeparatedByString:@";"]) {
        UTType* type = ext.length ? [UTType typeWithFilenameExtension:ext] : nil;
        if (type) [types addObject:type];
    }
    if (!types.count) [types addObject:UTTypeItem];
    return types;
}

// The sandbox of an AUv3 extension lets it read a file or folder the user
// picked until the process ends (a folder with everything in it). To read
// it again later, when a saved state names it, the extension keeps a
// security-scoped bookmark of each pick (the newest VST3GO_MAX_BOOKMARKS)
// and VST3GoRestoreFileAccess resolves them when it starts. Outside a
// sandbox (a macOS host process) there is nothing to do; iOS processes
// are always sandboxed.
#define VST3GO_MAX_BOOKMARKS 256
static NSString* const bookmarksKey = @"VST3GoFileBookmarks";

#if TARGET_OS_OSX
#define BOOKMARK_CREATION (NSURLBookmarkCreationWithSecurityScope | NSURLBookmarkCreationSecurityScopeAllowOnlyReadAccess)
#define BOOKMARK_RESOLUTION (NSURLBookmarkResolutionWithSecurityScope | NSURLBookmarkResolutionWithoutUI)

static bool sandboxed(void) {
    return getenv("APP_SANDBOX_CONTAINER_ID") != NULL;
}
#else
// iOS bookmarks of picked URLs carry their security scope.
#define BOOKMARK_CREATION 0
#define BOOKMARK_RESOLUTION NSURLBookmarkResolutionWithoutUI

static bool sandboxed(void) {
    return true;
}
#endif

void VST3GoKeepFileAccess(NSURL* url) {
    if (!sandboxed()) {
        return;
    }
    NSData* bookmark = [url bookmarkDataWithOptions:BOOKMARK_CREATION
                     includingResourceValuesForKeys:nil
                                      relativeToURL:nil
                                              error:nil];
    if (!bookmark) {
        return;
    }
    NSUserDefaults* defaults = [NSUserDefaults standardUserDefaults];
    NSMutableArray* list = [NSMutableArray arrayWithArray:[defaults arrayForKey:bookmarksKey] ?: @[]];
    [list addObject:bookmark];
    if (list.count > VST3GO_MAX_BOOKMARKS) {
        [list removeObjectsInRange:NSMakeRange(0, list.count - VST3GO_MAX_BOOKMARKS)];
    }
    [defaults setObject:list forKey:bookmarksKey];
}

void VST3GoRestoreFileAccess(void) {
    if (!sandboxed()) {
        return;
    }
    @autoreleasepool {
        NSUserDefaults* defaults = [NSUserDefaults standardUserDefaults];
        NSMutableArray* kept = [NSMutableArray array];
        for (id bookmark in [defaults arrayForKey:bookmarksKey]) {
            if (![bookmark isKindOfClass:[NSData class]]) {
                continue;
            }
            BOOL stale = NO;
            NSURL* url = [NSURL URLByResolvingBookmarkData:bookmark
                                                   options:BOOKMARK_RESOLUTION
                                             relativeToURL:nil
                                       bookmarkDataIsStale:&stale
                                                     error:nil];
            // Access lasts until the process ends.
            if (url && [url startAccessingSecurityScopedResource]) {
                [kept addObject:bookmark];
            }
        }
        [defaults setObject:kept forKey:bookmarksKey];
    }
}

#if TARGET_OS_OSX
void VST3GoEditorViewChooseFile(void* thisInterface, uint32_t request, const char* title, const char* extensions, int folder) {
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view) {
            return;
        }
        uintptr_t component = view->component;
        NSOpenPanel* panel = [NSOpenPanel openPanel];
        panel.canChooseFiles = !folder;
        panel.canChooseDirectories = folder != 0;
        panel.allowsMultipleSelection = NO;
        if (!folder) {
            panel.allowedContentTypes = fileTypes(extensions);
        }
        if (title && *title) {
            panel.message = [NSString stringWithUTF8String:title];
        }
        [panel beginWithCompletionHandler:^(NSModalResponse result) {
            NSURL* url = result == NSModalResponseOK ? panel.URL : nil;
            if (url) VST3GoKeepFileAccess(url);
            GoEditorFileChosen(component, request, url ? (char*)url.fileSystemRepresentation : NULL, NULL);
        }];
    }
}
#else
// The document picker's delegate, a class unique to this binary like the
// message handler's.
static Ivar pickerComponent, pickerRequest, pickerFolder;

static void pickerAnswer(id self, NSURL* url) {
    uintptr_t component = *(uintptr_t*)((char*)self + ivar_getOffset(pickerComponent));
    uint32_t request = *(uint32_t*)((char*)self + ivar_getOffset(pickerRequest));
    GoEditorFileChosen(component, request, url ? (char*)url.fileSystemRepresentation : NULL, NULL);
}

// A picked file's copy is temporary: it moves to its own folder under the
// extension's Application Support, where a saved state can find it again.
// A picked folder is not copied: its security scope opens it until the
// process ends, and its bookmark in later runs.
static void pickerPicked(id self, SEL cmd, UIDocumentPickerViewController* picker, NSArray<NSURL*>* urls) {
    (void)cmd;
    (void)picker;
    NSURL* copy = urls.firstObject;
    if (!copy) {
        pickerAnswer(self, nil);
        return;
    }
    if (*(BOOL*)((char*)self + ivar_getOffset(pickerFolder))) {
        [copy startAccessingSecurityScopedResource];
        VST3GoKeepFileAccess(copy);
        pickerAnswer(self, copy);
        return;
    }
    NSFileManager* files = [NSFileManager defaultManager];
    NSURL* support = [files URLForDirectory:NSApplicationSupportDirectory inDomain:NSUserDomainMask appropriateForURL:nil create:YES error:nil];
    NSURL* dir = [[support URLByAppendingPathComponent:@"vst3go-files" isDirectory:YES] URLByAppendingPathComponent:[NSUUID UUID].UUIDString isDirectory:YES];
    NSURL* kept = [dir URLByAppendingPathComponent:copy.lastPathComponent];
    NSError* error = nil;
    if (!support || ![files createDirectoryAtURL:dir withIntermediateDirectories:YES attributes:nil error:&error] ||
        ![files moveItemAtURL:copy toURL:kept error:&error]) {
        uintptr_t component = *(uintptr_t*)((char*)self + ivar_getOffset(pickerComponent));
        uint32_t request = *(uint32_t*)((char*)self + ivar_getOffset(pickerRequest));
        NSString* text = error ? error.localizedDescription : @"no Application Support folder";
        GoEditorFileChosen(component, request, NULL, (char*)text.UTF8String);
        return;
    }
    pickerAnswer(self, kept);
}

static void pickerCancelled(id self, SEL cmd, UIDocumentPickerViewController* picker) {
    (void)cmd;
    (void)picker;
    pickerAnswer(self, nil);
}

static id newPickerDelegate(uintptr_t component, uint32_t request, BOOL folder) {
    static Class cls;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        Dl_info info;
        char name[96];
        uintptr_t image = dladdr((const void*)&newPickerDelegate, &info) ? (uintptr_t)info.dli_fbase : (uintptr_t)&newPickerDelegate;
        snprintf(name, sizeof(name), "VST3GoDocumentPickerDelegate_%lx", (unsigned long)image);
        cls = objc_allocateClassPair([NSObject class], name, 0);
        class_addIvar(cls, "component", sizeof(uintptr_t), sizeof(uintptr_t) == 8 ? 3 : 2, "Q");
        class_addIvar(cls, "request", sizeof(uint32_t), 2, "I");
        class_addIvar(cls, "folder", sizeof(BOOL), 0, @encode(BOOL));
        class_addMethod(cls, @selector(documentPicker:didPickDocumentsAtURLs:), (IMP)pickerPicked, "v@:@@");
        class_addMethod(cls, @selector(documentPickerWasCancelled:), (IMP)pickerCancelled, "v@:@");
        class_addProtocol(cls, @protocol(UIDocumentPickerDelegate));
        objc_registerClassPair(cls);
        pickerComponent = class_getInstanceVariable(cls, "component");
        pickerRequest = class_getInstanceVariable(cls, "request");
        pickerFolder = class_getInstanceVariable(cls, "folder");
    });
    id delegate = [[cls alloc] init];
    *(uintptr_t*)((char*)delegate + ivar_getOffset(pickerComponent)) = component;
    *(uint32_t*)((char*)delegate + ivar_getOffset(pickerRequest)) = request;
    *(BOOL*)((char*)delegate + ivar_getOffset(pickerFolder)) = folder;
    return delegate;
}

// The picker's delegate is weak: each picker keeps its own alive.
static char pickerDelegateKey;

void VST3GoEditorViewChooseFile(void* thisInterface, uint32_t request, const char* title, const char* extensions, int folder) {
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view || !view->webView) {
            return;
        }
        UIResponder* responder = view->webView;
        while (responder && ![responder isKindOfClass:[UIViewController class]]) {
            responder = responder.nextResponder;
        }
        if (!responder) {
            GoEditorFileChosen(view->component, request, NULL, (char*)"the editor is not shown by a view controller");
            return;
        }
        // A file comes as a copy in the extension's container, which the
        // sandbox lets the plugin read; a folder as itself, security-scoped.
        UIDocumentPickerViewController* picker =
            folder ? [[[UIDocumentPickerViewController alloc] initForOpeningContentTypes:@[ UTTypeFolder ] asCopy:NO] autorelease]
                   : [[[UIDocumentPickerViewController alloc] initForOpeningContentTypes:fileTypes(extensions) asCopy:YES] autorelease];
        id delegate = newPickerDelegate(view->component, request, folder != 0);
        picker.delegate = delegate;
        objc_setAssociatedObject(picker, &pickerDelegateKey, delegate, OBJC_ASSOCIATION_RETAIN);
        [delegate release];
        if (title && *title) {
            picker.title = [NSString stringWithUTF8String:title];
        }
        [(UIViewController*)responder presentViewController:picker animated:YES completion:nil];
    }
}
#endif

// ---- floating windows ----

#if TARGET_OS_OSX

typedef struct {
    NSWindow* window;
    id delegate;
    VST3GoFloatingCallbacks callbacks;
} VST3GoFloatingWindow;

static Ivar floatingOwner;

// The user closing the window hides it and tells the owner.
static BOOL floatingShouldClose(id self, SEL cmd, NSWindow* sender) {
    (void)cmd;
    VST3GoFloatingWindow* w = *(VST3GoFloatingWindow**)((char*)self + ivar_getOffset(floatingOwner));
    [sender orderOut:nil];
    if (w) w->callbacks.closed(w->callbacks.ctx);
    return NO;
}

static void floatingDidResize(id self, SEL cmd, NSNotification* note) {
    (void)cmd;
    VST3GoFloatingWindow* w = *(VST3GoFloatingWindow**)((char*)self + ivar_getOffset(floatingOwner));
    if (!w) return;
    NSSize size = [[(NSWindow*)note.object contentView] frame].size;
    w->callbacks.resized(w->callbacks.ctx, (int32_t)size.width, (int32_t)size.height);
}

static id newFloatingDelegate(VST3GoFloatingWindow* owner) {
    static Class cls;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        Dl_info info;
        char name[96];
        uintptr_t image = dladdr((const void*)&newFloatingDelegate, &info) ? (uintptr_t)info.dli_fbase : (uintptr_t)&newFloatingDelegate;
        snprintf(name, sizeof(name), "VST3GoFloatingWindowDelegate_%lx", (unsigned long)image);
        cls = objc_allocateClassPair([NSObject class], name, 0);
        class_addIvar(cls, "owner", sizeof(void*), sizeof(void*) == 8 ? 3 : 2, "^v");
        class_addMethod(cls, @selector(windowShouldClose:), (IMP)floatingShouldClose, "c@:@");
        class_addMethod(cls, @selector(windowDidResize:), (IMP)floatingDidResize, "v@:@");
        class_addProtocol(cls, @protocol(NSWindowDelegate));
        objc_registerClassPair(cls);
        floatingOwner = class_getInstanceVariable(cls, "owner");
    });
    id delegate = [[cls alloc] init];
    *(VST3GoFloatingWindow**)((char*)delegate + ivar_getOffset(floatingOwner)) = owner;
    return delegate;
}

void* VST3GoFloatingWindowCreate(int32_t width, int32_t height, int resizable, VST3GoFloatingCallbacks callbacks) {
    VST3GoFloatingWindow* w = (VST3GoFloatingWindow*)calloc(1, sizeof(VST3GoFloatingWindow));
    if (!w) return NULL;
    w->callbacks = callbacks;
    NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable;
    if (resizable) style |= NSWindowStyleMaskResizable;
    w->window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, width, height) styleMask:style backing:NSBackingStoreBuffered defer:NO];
    [w->window setReleasedWhenClosed:NO];
    w->delegate = newFloatingDelegate(w);
    [w->window setDelegate:w->delegate];
    [w->window center];
    return w;
}

void* VST3GoFloatingWindowParent(void* window) {
    return window ? (void*)[((VST3GoFloatingWindow*)window)->window contentView] : NULL;
}

void VST3GoFloatingWindowSetTitle(void* window, const char* title) {
    if (!window || !title) return;
    [((VST3GoFloatingWindow*)window)->window setTitle:[NSString stringWithUTF8String:title]];
}

void VST3GoFloatingWindowSetTransient(void* window, void* owner) {
    if (!window || !owner) return;
    NSWindow* parent = [(NSView*)owner window];
    NSWindow* child = ((VST3GoFloatingWindow*)window)->window;
    if (parent && [child parentWindow] != parent) [parent addChildWindow:child ordered:NSWindowAbove];
}

void VST3GoFloatingWindowSetSize(void* window, int32_t width, int32_t height) {
    if (!window) return;
    [((VST3GoFloatingWindow*)window)->window setContentSize:NSMakeSize(width, height)];
}

void VST3GoFloatingWindowShow(void* window, int visible) {
    if (!window) return;
    NSWindow* w = ((VST3GoFloatingWindow*)window)->window;
    if (visible) {
        [w makeKeyAndOrderFront:nil];
    } else {
        [w orderOut:nil];
    }
}

void VST3GoFloatingWindowDestroy(void* window) {
    if (!window) return;
    VST3GoFloatingWindow* w = (VST3GoFloatingWindow*)window;
    *(VST3GoFloatingWindow**)((char*)w->delegate + ivar_getOffset(floatingOwner)) = NULL;
    [w->window setDelegate:nil];
    if ([w->window parentWindow]) [[w->window parentWindow] removeChildWindow:w->window];
    [w->window orderOut:nil];
    [w->window release];
    [w->delegate release];
    free(w);
}

#else

// iOS has no floating windows.
void* VST3GoFloatingWindowCreate(int32_t width, int32_t height, int resizable, VST3GoFloatingCallbacks callbacks) {
    (void)width; (void)height; (void)resizable; (void)callbacks;
    return NULL;
}
void* VST3GoFloatingWindowParent(void* window) { return window; }
void VST3GoFloatingWindowSetTitle(void* window, const char* title) { (void)window; (void)title; }
void VST3GoFloatingWindowSetTransient(void* window, void* owner) { (void)window; (void)owner; }
void VST3GoFloatingWindowSetSize(void* window, int32_t width, int32_t height) { (void)window; (void)width; (void)height; }
void VST3GoFloatingWindowShow(void* window, int visible) { (void)window; (void)visible; }
void VST3GoFloatingWindowDestroy(void* window) { (void)window; }

#endif
