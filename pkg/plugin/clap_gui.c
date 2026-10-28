// CLAP gui and timer-support extensions on top of the platform editor views
// (editor_view_*.c), which are VST3 IPlugView objects: the gui calls drive
// the view through its vtable, and a small IPlugFrame/IRunLoop pair maps the
// view's resize requests and Linux timers to the CLAP host.
#include "clap_bridge.h"
#include "editor_view.h"
#include <stdlib.h>
#include <string.h>

extern void* GoClapGuiCreateView(uintptr_t id);

#define VST3GO_CLAP_MAX_TIMERS 8

typedef struct VST3GoClapGui VST3GoClapGui;

typedef struct {
    struct Steinberg_IPlugFrameVtbl* lpVtbl;
    VST3GoClapGui* gui;
} VST3GoClapFrame;

typedef struct {
    struct Steinberg_Linux_IRunLoopVtbl* lpVtbl;
    VST3GoClapGui* gui;
} VST3GoClapRunLoop;

struct VST3GoClapGui {
    VST3GoClapPlugin* plugin;
    struct Steinberg_IPlugView* view;
    VST3GoClapFrame frame;
    VST3GoClapRunLoop runLoop;
    struct {
        clap_id id;
        struct Steinberg_Linux_ITimerHandler* handler;
    } timers[VST3GO_CLAP_MAX_TIMERS];
    bool attached;
};

static bool iid_equal(const Steinberg_TUID a, const Steinberg_TUID b) {
    return memcmp(a, b, sizeof(Steinberg_TUID)) == 0;
}

// The frame and run loop live as long as the gui; reference counting is a no-op.
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE gui_addRef(void* self) { (void)self; return 1; }
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE gui_release(void* self) { (void)self; return 1; }

static Steinberg_tresult SMTG_STDMETHODCALLTYPE frame_queryInterface(void* self, const Steinberg_TUID iid, void** obj) {
    VST3GoClapFrame* frame = (VST3GoClapFrame*)self;
    if (iid_equal(iid, Steinberg_FUnknown_iid) || iid_equal(iid, Steinberg_IPlugFrame_iid)) {
        *obj = frame;
        return Steinberg_kResultOk;
    }
    if (iid_equal(iid, Steinberg_Linux_IRunLoop_iid)) {
        *obj = &frame->gui->runLoop;
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE frame_resizeView(void* self, struct Steinberg_IPlugView* view, struct Steinberg_ViewRect* rect) {
    (void)view;
    VST3GoClapPlugin* p = ((VST3GoClapFrame*)self)->gui->plugin;
    if (!rect || !p->hostGui) {
        return Steinberg_kResultFalse;
    }
    uint32_t width = (uint32_t)(rect->right - rect->left), height = (uint32_t)(rect->bottom - rect->top);
    return p->hostGui->request_resize(p->host, width, height) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static struct Steinberg_IPlugFrameVtbl frameVtbl = {
    frame_queryInterface, gui_addRef, gui_release, frame_resizeView
};

static Steinberg_tresult SMTG_STDMETHODCALLTYPE runLoop_queryInterface(void* self, const Steinberg_TUID iid, void** obj) {
    VST3GoClapRunLoop* loop = (VST3GoClapRunLoop*)self;
    if (iid_equal(iid, Steinberg_FUnknown_iid) || iid_equal(iid, Steinberg_Linux_IRunLoop_iid)) {
        *obj = loop;
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE runLoop_registerEventHandler(void* self, struct Steinberg_Linux_IEventHandler* handler, Steinberg_Linux_FileDescriptor fd) {
    (void)self; (void)handler; (void)fd;
    return Steinberg_kNotImplemented;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE runLoop_unregisterEventHandler(void* self, struct Steinberg_Linux_IEventHandler* handler) {
    (void)self; (void)handler;
    return Steinberg_kNotImplemented;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE runLoop_registerTimer(void* self, struct Steinberg_Linux_ITimerHandler* handler, Steinberg_Linux_TimerInterval ms) {
    VST3GoClapGui* gui = ((VST3GoClapRunLoop*)self)->gui;
    VST3GoClapPlugin* p = gui->plugin;
    if (!handler || !p->hostTimer) {
        return Steinberg_kResultFalse;
    }
    for (int i = 0; i < VST3GO_CLAP_MAX_TIMERS; i++) {
        if (gui->timers[i].handler) {
            continue;
        }
        clap_id id;
        if (!p->hostTimer->register_timer(p->host, (uint32_t)ms, &id)) {
            return Steinberg_kResultFalse;
        }
        gui->timers[i].id = id;
        gui->timers[i].handler = handler;
        return Steinberg_kResultOk;
    }
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE runLoop_unregisterTimer(void* self, struct Steinberg_Linux_ITimerHandler* handler) {
    VST3GoClapGui* gui = ((VST3GoClapRunLoop*)self)->gui;
    for (int i = 0; i < VST3GO_CLAP_MAX_TIMERS; i++) {
        if (gui->timers[i].handler == handler) {
            gui->plugin->hostTimer->unregister_timer(gui->plugin->host, gui->timers[i].id);
            gui->timers[i].handler = NULL;
            return Steinberg_kResultOk;
        }
    }
    return Steinberg_kResultFalse;
}

static struct Steinberg_Linux_IRunLoopVtbl runLoopVtbl = {
    runLoop_queryInterface, gui_addRef, gui_release,
    runLoop_registerEventHandler, runLoop_unregisterEventHandler,
    runLoop_registerTimer, runLoop_unregisterTimer
};

// timer-support

static void timer_on_timer(const clap_plugin_t* plugin, clap_id id) {
    VST3GoClapGui* gui = ((VST3GoClapPlugin*)plugin->plugin_data)->gui;
    if (!gui) {
        return;
    }
    for (int i = 0; i < VST3GO_CLAP_MAX_TIMERS; i++) {
        if (gui->timers[i].handler && gui->timers[i].id == id) {
            gui->timers[i].handler->lpVtbl->onTimer(gui->timers[i].handler);
            return;
        }
    }
}

static const clap_plugin_timer_support_t extTimer = { timer_on_timer };

// gui

// clapAPI is the CLAP window API of the platform editor view, NULL without one.
static const char* clapAPI(void) {
    if (!VST3GoEditorPlatformType) return NULL;
    if (!strcmp(VST3GoEditorPlatformType, Steinberg_kPlatformTypeNSView)) return CLAP_WINDOW_API_COCOA;
    if (!strcmp(VST3GoEditorPlatformType, Steinberg_kPlatformTypeHWND)) return CLAP_WINDOW_API_WIN32;
    if (!strcmp(VST3GoEditorPlatformType, Steinberg_kPlatformTypeX11EmbedWindowID)) return CLAP_WINDOW_API_X11;
    return NULL;
}

static VST3GoClapGui* guiOf(const clap_plugin_t* plugin) {
    return ((VST3GoClapPlugin*)plugin->plugin_data)->gui;
}

static bool gui_is_api_supported(const clap_plugin_t* plugin, const char* api, bool isFloating) {
    (void)plugin;
    return !isFloating && api && clapAPI() && !strcmp(api, clapAPI());
}

static bool gui_get_preferred_api(const clap_plugin_t* plugin, const char** api, bool* isFloating) {
    (void)plugin;
    *api = clapAPI();
    *isFloating = false;
    return *api != NULL;
}

static bool gui_create(const clap_plugin_t* plugin, const char* api, bool isFloating) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    if (p->gui || !gui_is_api_supported(plugin, api, isFloating)) {
        return false;
    }
    VST3GoClapGui* gui = (VST3GoClapGui*)calloc(1, sizeof(VST3GoClapGui));
    if (!gui) {
        return false;
    }
    gui->plugin = p;
    gui->frame.lpVtbl = &frameVtbl;
    gui->frame.gui = gui;
    gui->runLoop.lpVtbl = &runLoopVtbl;
    gui->runLoop.gui = gui;
    gui->view = (struct Steinberg_IPlugView*)GoClapGuiCreateView(p->id);
    if (!gui->view) {
        free(gui);
        return false;
    }
    gui->view->lpVtbl->setFrame(gui->view, (struct Steinberg_IPlugFrame*)&gui->frame);
    p->gui = gui;
    return true;
}

void vst3go_clap_gui_destroy(VST3GoClapPlugin* p) {
    VST3GoClapGui* gui = p->gui;
    if (!gui) {
        return;
    }
    if (gui->attached) {
        gui->view->lpVtbl->removed(gui->view);
    }
    gui->view->lpVtbl->setFrame(gui->view, NULL);
    gui->view->lpVtbl->release(gui->view);
    for (int i = 0; i < VST3GO_CLAP_MAX_TIMERS; i++) {
        if (gui->timers[i].handler) {
            p->hostTimer->unregister_timer(p->host, gui->timers[i].id);
        }
    }
    p->gui = NULL;
    free(gui);
}

static void gui_destroy(const clap_plugin_t* plugin) {
    vst3go_clap_gui_destroy((VST3GoClapPlugin*)plugin->plugin_data);
}

static bool gui_set_scale(const clap_plugin_t* plugin, double scale) {
    VST3GoClapGui* gui = guiOf(plugin);
    struct Steinberg_IPlugViewContentScaleSupport* support = NULL;
    if (!gui || gui->view->lpVtbl->queryInterface(gui->view, Steinberg_IPlugViewContentScaleSupport_iid, (void**)&support) != Steinberg_kResultOk || !support) {
        return false;
    }
    bool ok = support->lpVtbl->setContentScaleFactor(support, (float)scale) == Steinberg_kResultOk;
    support->lpVtbl->release(support);
    return ok;
}

static bool gui_get_size(const clap_plugin_t* plugin, uint32_t* width, uint32_t* height) {
    VST3GoClapGui* gui = guiOf(plugin);
    struct Steinberg_ViewRect rect;
    if (!gui || gui->view->lpVtbl->getSize(gui->view, &rect) != Steinberg_kResultOk) {
        return false;
    }
    *width = (uint32_t)(rect.right - rect.left);
    *height = (uint32_t)(rect.bottom - rect.top);
    return true;
}

static bool gui_can_resize(const clap_plugin_t* plugin) {
    VST3GoClapGui* gui = guiOf(plugin);
    return gui && gui->view->lpVtbl->canResize(gui->view) == Steinberg_kResultOk;
}

static bool gui_get_resize_hints(const clap_plugin_t* plugin, clap_gui_resize_hints_t* hints) {
    bool resizable = gui_can_resize(plugin);
    hints->can_resize_horizontally = resizable;
    hints->can_resize_vertically = resizable;
    hints->preserve_aspect_ratio = false;
    hints->aspect_ratio_width = 0;
    hints->aspect_ratio_height = 0;
    return resizable;
}

static bool gui_adjust_size(const clap_plugin_t* plugin, uint32_t* width, uint32_t* height) {
    VST3GoClapGui* gui = guiOf(plugin);
    struct Steinberg_ViewRect rect = { 0, 0, (Steinberg_int32)*width, (Steinberg_int32)*height };
    if (!gui || gui->view->lpVtbl->checkSizeConstraint(gui->view, &rect) != Steinberg_kResultOk) {
        return false;
    }
    *width = (uint32_t)(rect.right - rect.left);
    *height = (uint32_t)(rect.bottom - rect.top);
    return true;
}

static bool gui_set_size(const clap_plugin_t* plugin, uint32_t width, uint32_t height) {
    VST3GoClapGui* gui = guiOf(plugin);
    struct Steinberg_ViewRect rect = { 0, 0, (Steinberg_int32)width, (Steinberg_int32)height };
    return gui && gui->view->lpVtbl->onSize(gui->view, &rect) == Steinberg_kResultOk;
}

static bool gui_set_parent(const clap_plugin_t* plugin, const clap_window_t* window) {
    VST3GoClapGui* gui = guiOf(plugin);
    if (!gui || !window || !window->api || !clapAPI() || strcmp(window->api, clapAPI()) != 0) {
        return false;
    }
    void* parent;
    if (!strcmp(window->api, CLAP_WINDOW_API_X11)) {
        parent = (void*)(uintptr_t)window->x11;
    } else {
        parent = window->ptr;
    }
    if (gui->view->lpVtbl->attached(gui->view, parent, VST3GoEditorPlatformType) != Steinberg_kResultOk) {
        return false;
    }
    gui->attached = true;
    return true;
}

static bool gui_set_transient(const clap_plugin_t* plugin, const clap_window_t* window) {
    (void)plugin; (void)window;
    return false;
}

static void gui_suggest_title(const clap_plugin_t* plugin, const char* title) {
    (void)plugin; (void)title;
}

// The embedded view is shown and hidden with its parent window.
static bool gui_show(const clap_plugin_t* plugin) { return guiOf(plugin) != NULL; }
static bool gui_hide(const clap_plugin_t* plugin) { return guiOf(plugin) != NULL; }

static const clap_plugin_gui_t extGui = {
    gui_is_api_supported, gui_get_preferred_api, gui_create, gui_destroy, gui_set_scale,
    gui_get_size, gui_can_resize, gui_get_resize_hints, gui_adjust_size, gui_set_size,
    gui_set_parent, gui_set_transient, gui_suggest_title, gui_show, gui_hide
};

const void* vst3go_clap_gui_extension(const char* id) {
    if (!clapAPI()) {
        return NULL;
    }
    if (!strcmp(id, CLAP_EXT_GUI)) return &extGui;
    if (!strcmp(id, CLAP_EXT_TIMER_SUPPORT)) return &extTimer;
    return NULL;
}
