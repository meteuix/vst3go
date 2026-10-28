//go:build linux && webkitgtk

// Linux editor: a WebKitGTK web view in an override-redirect GTK window that
// is reparented into the host's X11 window (kPlatformTypeX11EmbedWindowID).
// GTK events are pumped from the host's IRunLoop timer; hosts that run a
// GLib main loop on their UI thread dispatch them without it.

#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <webkit2/webkit2.h>
#include <X11/Xlib.h>

#include "editor_view.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct VST3GoEditorView VST3GoEditorView;

typedef struct {
    Steinberg_IPlugViewContentScaleSupportVtbl* lpVtbl;
    VST3GoEditorView* owner;
} VST3GoEditorScaleSupport;

typedef struct {
    struct Steinberg_Linux_ITimerHandlerVtbl* lpVtbl;
    VST3GoEditorView* owner;
} VST3GoEditorTimer;

struct VST3GoEditorView {
    Steinberg_IPlugView view;
    VST3GoEditorScaleSupport scaleSupport;
    VST3GoEditorTimer timer;
    Steinberg_uint32 refCount;
    uintptr_t component;
    struct Steinberg_IPlugFrame* frame;
    struct Steinberg_Linux_IRunLoop* runLoop; // one reference while attached
    GtkWidget* window;
    GtkWidget* webView;
    Steinberg_ViewRect size;
    float scaleFactor;
};

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorView_addRef(void* thisInterface);
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorView_release(void* thisInterface);
static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_removed(void* thisInterface);

static int uid_equal(const Steinberg_TUID a, const Steinberg_TUID b) {
    return memcmp(a, b, sizeof(Steinberg_TUID)) == 0;
}

// GTK main loop integration

static void pumpEvents(void) {
    for (int i = 0; i < 64 && g_main_context_iteration(NULL, FALSE); i++) {
    }
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE timer_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    VST3GoEditorTimer* timer = (VST3GoEditorTimer*)thisInterface;
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    if (uid_equal(iid, Steinberg_FUnknown_iid) || uid_equal(iid, Steinberg_Linux_ITimerHandler_iid)) {
        *obj = timer;
        editorView_addRef(timer->owner);
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE timer_addRef(void* thisInterface) {
    return editorView_addRef(((VST3GoEditorTimer*)thisInterface)->owner);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE timer_release(void* thisInterface) {
    return editorView_release(((VST3GoEditorTimer*)thisInterface)->owner);
}

static void SMTG_STDMETHODCALLTYPE timer_onTimer(void* thisInterface) {
    (void)thisInterface;
    pumpEvents();
}

static struct Steinberg_Linux_ITimerHandlerVtbl timerVtbl = {
    timer_queryInterface,
    timer_addRef,
    timer_release,
    timer_onTimer
};

// Web content

static void onScriptMessage(WebKitUserContentManager* manager, WebKitJavascriptResult* result, gpointer userData) {
    (void)manager;
    VST3GoEditorView* view = (VST3GoEditorView*)userData;
    char* json = jsc_value_to_json(webkit_javascript_result_get_js_value(result), 0);
    if (json) {
        GoEditorMessage(view->component, json, (int)strlen(json));
        g_free(json);
    }
}

static void loadContent(VST3GoEditorView* view) {
    char* html = GoEditControllerGetEditorHTML(view->component);
    if (html) {
        webkit_web_view_load_html(WEBKIT_WEB_VIEW(view->webView), html, NULL);
        free(html);
        return;
    }
    webkit_web_view_load_html(WEBKIT_WEB_VIEW(view->webView),
                              "<!doctype html><html><body style=\"background:#0b1020;color:#e2e8f0;font-family:sans-serif;padding:24px;\">Editor model unavailable.</body></html>",
                              NULL);
}

static void runScript(VST3GoEditorView* view, const char* script) {
#if WEBKIT_CHECK_VERSION(2, 40, 0)
    webkit_web_view_evaluate_javascript(WEBKIT_WEB_VIEW(view->webView), script, -1, NULL, NULL, NULL, NULL, NULL);
#else
    webkit_web_view_run_javascript(WEBKIT_WEB_VIEW(view->webView), script, NULL, NULL, NULL);
#endif
}

void* VST3GoCreateEditorView(uintptr_t component);

void VST3GoEditorViewPost(void* thisInterface, const char* json) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !view->webView || !json) {
        return;
    }
    char* script = g_strdup_printf("if (window.__vst3goReceive) { window.__vst3goReceive(%s); }", json);
    runScript(view, script);
    g_free(script);
}

// IPlugView

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_isPlatformTypeSupported(void* thisInterface, Steinberg_FIDString type) {
    (void)thisInterface;
    return (type && strcmp(type, Steinberg_kPlatformTypeX11EmbedWindowID) == 0) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static int width(const Steinberg_ViewRect* r) { return (int)(r->right - r->left); }
static int height(const Steinberg_ViewRect* r) { return (int)(r->bottom - r->top); }

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !parent || editorView_isPlatformTypeSupported(view, type) != Steinberg_kResultOk) {
        return Steinberg_kResultFalse;
    }
    if (view->window) {
        editorView_removed(view);
    }
    // GTK must use X11 to embed into the host window.
    gdk_set_allowed_backends("x11");
    if (!gtk_init_check(NULL, NULL)) {
        return Steinberg_kResultFalse;
    }
    GdkDisplay* display = gdk_display_get_default();
    if (!display || !GDK_IS_X11_DISPLAY(display)) {
        return Steinberg_kResultFalse;
    }

    view->window = gtk_window_new(GTK_WINDOW_POPUP);
    gtk_window_set_default_size(GTK_WINDOW(view->window), width(&view->size), height(&view->size));
    gtk_window_set_decorated(GTK_WINDOW(view->window), FALSE);

    WebKitUserContentManager* manager = webkit_user_content_manager_new();
    g_signal_connect(manager, "script-message-received::vst3go", G_CALLBACK(onScriptMessage), view);
    webkit_user_content_manager_register_script_message_handler(manager, "vst3go");
    view->webView = webkit_web_view_new_with_user_content_manager(manager);
    g_object_unref(manager);
    gtk_container_add(GTK_CONTAINER(view->window), view->webView);
    gtk_widget_realize(view->window);

    Display* xdisplay = gdk_x11_display_get_xdisplay(display);
    Window child = gdk_x11_window_get_xid(gtk_widget_get_window(view->window));
    XReparentWindow(xdisplay, child, (Window)(uintptr_t)parent, 0, 0);
    gtk_widget_show_all(view->window);
    XMapWindow(xdisplay, child);
    XFlush(xdisplay);
    loadContent(view);

    if (view->frame && view->frame->lpVtbl->queryInterface(view->frame, Steinberg_Linux_IRunLoop_iid, (void**)&view->runLoop) == Steinberg_kResultOk && view->runLoop) {
        view->runLoop->lpVtbl->registerTimer(view->runLoop, (struct Steinberg_Linux_ITimerHandler*)&view->timer, 16);
    } else {
        view->runLoop = NULL;
    }
    pumpEvents();
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_removed(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view) {
        return Steinberg_kResultFalse;
    }
    if (view->runLoop) {
        view->runLoop->lpVtbl->unregisterTimer(view->runLoop, (struct Steinberg_Linux_ITimerHandler*)&view->timer);
        view->runLoop->lpVtbl->release(view->runLoop);
        view->runLoop = NULL;
    }
    if (view->window) {
        gtk_widget_destroy(view->window);
        view->window = NULL;
        view->webView = NULL;
        pumpEvents();
    }
    GoEditControllerClearEditorView(view->component, view);
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_onWheel(void* thisInterface, float distance) {
    (void)thisInterface;
    (void)distance;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_onKey(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers) {
    (void)thisInterface;
    (void)key;
    (void)keyCode;
    (void)modifiers;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_getSize(void* thisInterface, Steinberg_ViewRect* size) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !size) {
        return Steinberg_kResultFalse;
    }
    *size = view->size;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_onSize(void* thisInterface, Steinberg_ViewRect* size) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !size) {
        return Steinberg_kResultFalse;
    }
    view->size = *size;
    if (view->window && width(size) > 0 && height(size) > 0) {
        gtk_window_resize(GTK_WINDOW(view->window), width(size), height(size));
        pumpEvents();
    }
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_onFocus(void* thisInterface, Steinberg_TBool state) {
    (void)thisInterface;
    (void)state;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_setFrame(void* thisInterface, struct Steinberg_IPlugFrame* frame) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view) {
        return Steinberg_kResultFalse;
    }
    view->frame = frame;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_canResize(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    return view && GoEditorCanResize(view->component) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect) {
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

static Steinberg_IPlugViewVtbl editorViewVtbl = {
    editorView_queryInterface,
    editorView_addRef,
    editorView_release,
    editorView_isPlatformTypeSupported,
    editorView_attached,
    editorView_removed,
    editorView_onWheel,
    editorView_onKey,
    editorView_onKey,
    editorView_getSize,
    editorView_onSize,
    editorView_onFocus,
    editorView_setFrame,
    editorView_canResize,
    editorView_checkSizeConstraint
};

// IPlugViewContentScaleSupport

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorScale_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return editorView_queryInterface(((VST3GoEditorScaleSupport*)thisInterface)->owner, iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorScale_addRef(void* thisInterface) {
    return editorView_addRef(((VST3GoEditorScaleSupport*)thisInterface)->owner);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorScale_release(void* thisInterface) {
    return editorView_release(((VST3GoEditorScaleSupport*)thisInterface)->owner);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorScale_setContentScaleFactor(void* thisInterface, Steinberg_IPlugViewContentScaleSupport_ScaleFactor factor) {
    VST3GoEditorView* view = ((VST3GoEditorScaleSupport*)thisInterface)->owner;
    if (!view || factor <= 0) {
        return Steinberg_kResultFalse;
    }
    view->scaleFactor = factor;
    if (view->webView) {
        webkit_web_view_set_zoom_level(WEBKIT_WEB_VIEW(view->webView), factor);
    }
    return Steinberg_kResultOk;
}

static Steinberg_IPlugViewContentScaleSupportVtbl editorScaleVtbl = {
    editorScale_queryInterface,
    editorScale_addRef,
    editorScale_release,
    editorScale_setContentScaleFactor
};

// FUnknown

static Steinberg_tresult SMTG_STDMETHODCALLTYPE editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    if (uid_equal(iid, Steinberg_FUnknown_iid) || uid_equal(iid, Steinberg_IPlugView_iid)) {
        *obj = &view->view;
    } else if (uid_equal(iid, Steinberg_IPlugViewContentScaleSupport_iid)) {
        *obj = &view->scaleSupport;
    } else {
        *obj = NULL;
        return Steinberg_kNoInterface;
    }
    editorView_addRef(view);
    return Steinberg_kResultOk;
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorView_addRef(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    return ++view->refCount;
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE editorView_release(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || view->refCount == 0) {
        return 0;
    }
    if (--view->refCount > 0) {
        return view->refCount;
    }
    editorView_removed(view);
    free(view);
    return 0;
}

const char* const VST3GoEditorPlatformType = "X11EmbedWindowID";

void* VST3GoCreateEditorView(uintptr_t component) {
    VST3GoEditorView* view = (VST3GoEditorView*)calloc(1, sizeof(VST3GoEditorView));
    if (!view) {
        return NULL;
    }
    view->view.lpVtbl = &editorViewVtbl;
    view->scaleSupport.lpVtbl = &editorScaleVtbl;
    view->scaleSupport.owner = view;
    view->timer.lpVtbl = &timerVtbl;
    view->timer.owner = view;
    view->refCount = 1;
    view->component = component;
    GoEditorSize(component, &view->size.right, &view->size.bottom);
    view->scaleFactor = 1.0f;
    return view;
}
