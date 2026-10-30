#include "panel.h"

#include "editor_view.h"
#include <stdlib.h>
#include <string.h>

extern void GoPanelClosed(uintptr_t id);

typedef struct {
    struct Steinberg_IPlugFrameVtbl* lpVtbl;
    VST3GoPanel* panel;
} PanelFrame;

typedef struct {
    struct Steinberg_Linux_IRunLoopVtbl* lpVtbl;
    VST3GoPanel* panel;
} PanelRunLoop;

struct VST3GoPanel {
    uintptr_t id;
    struct Steinberg_IPlugView* view;
    PanelFrame frame;
    PanelRunLoop runLoop;
    struct Steinberg_Linux_ITimerHandler* timer;
    void* window;  // floating window
    bool attached;
};

static bool iid_equal(const Steinberg_TUID a, const Steinberg_TUID b) { return memcmp(a, b, sizeof(Steinberg_TUID)) == 0; }

// The frame and run loop live as long as the panel.
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE panel_ref(void* self) { (void)self; return 1; }

static Steinberg_tresult SMTG_STDMETHODCALLTYPE frame_query(void* self, const Steinberg_TUID iid, void** obj) {
    PanelFrame* frame = (PanelFrame*)self;
    if (iid_equal(iid, Steinberg_FUnknown_iid) || iid_equal(iid, Steinberg_IPlugFrame_iid)) {
        *obj = frame;
        return Steinberg_kResultOk;
    }
    if (iid_equal(iid, Steinberg_Linux_IRunLoop_iid)) {
        *obj = &frame->panel->runLoop;
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

// A panel's page resizing itself resizes its floating window.
static Steinberg_tresult SMTG_STDMETHODCALLTYPE frame_resize(void* self, struct Steinberg_IPlugView* view, struct Steinberg_ViewRect* rect) {
    VST3GoPanel* p = ((PanelFrame*)self)->panel;
    if (!view || !rect || !p->window) return Steinberg_kResultFalse;
    VST3GoFloatingWindowSetSize(p->window, rect->right - rect->left, rect->bottom - rect->top);
    return view->lpVtbl->onSize(view, rect);
}

static struct Steinberg_IPlugFrameVtbl frameVtbl = { frame_query, panel_ref, panel_ref, frame_resize };

static Steinberg_tresult SMTG_STDMETHODCALLTYPE loop_query(void* self, const Steinberg_TUID iid, void** obj) {
    if (iid_equal(iid, Steinberg_FUnknown_iid) || iid_equal(iid, Steinberg_Linux_IRunLoop_iid)) {
        *obj = self;
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE loop_register_handler(void* self, struct Steinberg_Linux_IEventHandler* h, Steinberg_Linux_FileDescriptor fd) {
    (void)self; (void)h; (void)fd;
    return Steinberg_kNotImplemented;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE loop_unregister_handler(void* self, struct Steinberg_Linux_IEventHandler* h) {
    (void)self; (void)h;
    return Steinberg_kNotImplemented;
}
// One timer: the X11 view's GTK pump, fired by vst3go_panel_idle.
static Steinberg_tresult SMTG_STDMETHODCALLTYPE loop_register_timer(void* self, struct Steinberg_Linux_ITimerHandler* h, Steinberg_Linux_TimerInterval ms) {
    (void)ms;
    VST3GoPanel* p = ((PanelRunLoop*)self)->panel;
    if (!h || p->timer) return Steinberg_kResultFalse;
    p->timer = h;
    return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE loop_unregister_timer(void* self, struct Steinberg_Linux_ITimerHandler* h) {
    VST3GoPanel* p = ((PanelRunLoop*)self)->panel;
    if (p->timer != h) return Steinberg_kResultFalse;
    p->timer = NULL;
    return Steinberg_kResultOk;
}
static struct Steinberg_Linux_IRunLoopVtbl loopVtbl = {
    loop_query, panel_ref, panel_ref, loop_register_handler, loop_unregister_handler, loop_register_timer, loop_unregister_timer
};

VST3GoPanel* vst3go_panel_new(uintptr_t id) {
    VST3GoPanel* p = (VST3GoPanel*)calloc(1, sizeof(VST3GoPanel));
    if (!p) return NULL;
    p->id = id;
    p->view = (struct Steinberg_IPlugView*)VST3GoCreateEditorView(id);
    if (!p->view) {
        free(p);
        return NULL;
    }
    p->frame = (PanelFrame){ &frameVtbl, p };
    p->runLoop = (PanelRunLoop){ &loopVtbl, p };
    p->view->lpVtbl->setFrame(p->view, (struct Steinberg_IPlugFrame*)&p->frame);
    return p;
}

void* vst3go_panel_view(VST3GoPanel* p) { return p->view; }

bool vst3go_panel_attach(VST3GoPanel* p, void* parent) {
    if (p->attached) return false;
    p->attached = p->view->lpVtbl->attached(p->view, parent, VST3GoEditorPlatformType) == Steinberg_kResultOk;
    return p->attached;
}

static void window_closed(void* ctx) { GoPanelClosed(((VST3GoPanel*)ctx)->id); }

static void window_resized(void* ctx, int32_t width, int32_t height) {
    VST3GoPanel* p = (VST3GoPanel*)ctx;
    struct Steinberg_ViewRect rect = { 0, 0, width, height };
    if (p->attached && width > 0 && height > 0) p->view->lpVtbl->onSize(p->view, &rect);
}

bool vst3go_panel_open_window(VST3GoPanel* p, const char* title, int32_t width, int32_t height, bool resizable) {
    if (p->attached || p->window) return false;
    VST3GoFloatingCallbacks callbacks = { window_closed, window_resized, p };
    p->window = VST3GoFloatingWindowCreate(width, height, resizable, callbacks);
    if (!p->window) return false;
    VST3GoFloatingWindowSetTitle(p->window, title);
    if (!vst3go_panel_attach(p, VST3GoFloatingWindowParent(p->window))) {
        VST3GoFloatingWindowDestroy(p->window);
        p->window = NULL;
        return false;
    }
    VST3GoFloatingWindowShow(p->window, 1);
    return true;
}

void vst3go_panel_show_window(VST3GoPanel* p, bool visible) {
    if (p->window) VST3GoFloatingWindowShow(p->window, visible);
}

void vst3go_panel_set_size(VST3GoPanel* p, int32_t width, int32_t height) {
    struct Steinberg_ViewRect rect = { 0, 0, width, height };
    if (p->window) VST3GoFloatingWindowSetSize(p->window, width, height);
    p->view->lpVtbl->onSize(p->view, &rect);
}

void vst3go_panel_idle(VST3GoPanel* p) {
    if (p->timer) p->timer->lpVtbl->onTimer(p->timer);
}

void vst3go_panel_free(VST3GoPanel* p) {
    if (p->attached) p->view->lpVtbl->removed(p->view);
    p->view->lpVtbl->setFrame(p->view, NULL);
    p->view->lpVtbl->release(p->view);
    if (p->window) VST3GoFloatingWindowDestroy(p->window);
    free(p);
}
