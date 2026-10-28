//go:build darwin

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

#include "editor_view.h"
#include <stdlib.h>
#include <string.h>

@interface VST3GoEditorMessageHandler : NSObject <WKScriptMessageHandler>
@property(nonatomic, assign) struct VST3GoEditorView *owner;
@end

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
    NSView* parentView;
    NSView* containerView;
    WKWebView* webView;
    VST3GoEditorMessageHandler* messageHandler;
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

@implementation VST3GoEditorMessageHandler
- (void)userContentController:(WKUserContentController *)userContentController didReceiveScriptMessage:(WKScriptMessage *)message {
    (void)userContentController;
    if (!self.owner || ![message.body isKindOfClass:[NSDictionary class]]) {
        return;
    }

    NSDictionary *payload = (NSDictionary *)message.body;
    if (![payload[@"type"] isEqualToString:@"param-change"]) {
        return;
    }

    NSNumber *identifier = payload[@"id"];
    NSNumber *value = payload[@"value"];
    if (!identifier || !value) {
        return;
    }

    GoEditControllerSetParamNormalizedWithNotification(self.owner->component, (Steinberg_Vst_ParamID)[identifier unsignedIntValue], (Steinberg_Vst_ParamValue)[value doubleValue]);
}
@end

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
        view->size.right = 900;
        view->size.bottom = 640;
        view->scaleFactor = 1.0f;
        return view;
    }
}

void VST3GoEditorViewUpdateParameter(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue normalized, Steinberg_Vst_ParamValue plain) {
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view || !view->webView) {
            return;
        }

        NSString* script = [NSString stringWithFormat:@"if (window.__vst3goUpdateParameter) { window.__vst3goUpdateParameter(%u, %.17g, %.17g); }", (unsigned int)id, normalized, plain];
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
            view->messageHandler.owner = nil;
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

    return (strcmp(type, "NSView") == 0) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type) {
    (void)type;
    @autoreleasepool {
        VST3GoEditorView* view = editorViewFromInterface(thisInterface);
        if (!view || !parent) {
            return Steinberg_kResultFalse;
        }

        NSView* parentView = (NSView*)parent;
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
        view->containerView = [[NSView alloc] initWithFrame:parentView.bounds];
        view->containerView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

        WKWebViewConfiguration* configuration = [[WKWebViewConfiguration alloc] init];
        WKUserContentController* contentController = [[WKUserContentController alloc] init];
        view->messageHandler = [[VST3GoEditorMessageHandler alloc] init];
        view->messageHandler.owner = view;
        [contentController addScriptMessageHandler:view->messageHandler name:@"vst3go"];
        configuration.userContentController = contentController;

        view->webView = [[WKWebView alloc] initWithFrame:view->containerView.bounds configuration:configuration];
        view->webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
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
            view->messageHandler.owner = nil;
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
        view->containerView.frame = NSMakeRect(0, 0, width, height);
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
    (void)thisInterface;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect) {
    (void)thisInterface;
    (void)rect;
    return Steinberg_kResultOk;
}

