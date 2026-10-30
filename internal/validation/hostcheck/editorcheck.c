// editorcheck opens the editor of a plugin built with the Linux WebKitGTK
// editor (build tag webkitgtk) in an X11 window, the way a Linux VST3 host
// does: IPlugFrame with an IRunLoop whose timers the host drives. It checks
// that the view embeds into the host window, that keyboard input in the page
// changes a parameter through the JS -> Go bridge (XTest), that it survives
// host parameter updates and resizing, and that it detaches cleanly. With
// --clap it does the same through the CLAP gui and timer-support extensions
// of the plugin's .clap binary. With --custom it expects the synth's custom
// editor page, which asks for 720x420 through vst3go.resize at startup.
// Run it under an X server (e.g. xvfb-run).
//
// usage: editorcheck [--clap] [--custom] <plugin-binary>
#include "vst3/vst3_c_api.h"
#include "clap/clap.h"
#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>
#include <X11/keysym.h>
#include <dlfcn.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

static int uid_eq(const Steinberg_TUID a, const Steinberg_TUID b) { return memcmp(a, b, sizeof(Steinberg_TUID)) == 0; }
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE ref(void* t) { (void)t; return 1; }

// IRunLoop: one timer, driven by main().
typedef struct { struct Steinberg_Linux_IRunLoopVtbl* lpVtbl; struct Steinberg_Linux_ITimerHandler* timer; int registrations; } RunLoop;
static RunLoop runLoop;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE rl_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_Linux_IRunLoop_iid)) { *o = t; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE rl_regEvent(void* t, struct Steinberg_Linux_IEventHandler* h, Steinberg_Linux_FileDescriptor fd) { (void)t; (void)h; (void)fd; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE rl_unregEvent(void* t, struct Steinberg_Linux_IEventHandler* h) { (void)t; (void)h; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE rl_regTimer(void* t, struct Steinberg_Linux_ITimerHandler* h, Steinberg_Linux_TimerInterval ms) {
    (void)ms; RunLoop* r = t; r->timer = h; r->registrations++; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE rl_unregTimer(void* t, struct Steinberg_Linux_ITimerHandler* h) {
    RunLoop* r = t; if (r->timer == h) r->timer = NULL; return Steinberg_kResultOk;
}
static struct Steinberg_Linux_IRunLoopVtbl runLoopVtbl = { rl_qi, ref, ref, rl_regEvent, rl_unregEvent, rl_regTimer, rl_unregTimer };

// IPlugFrame exposing the run loop.
typedef struct { struct Steinberg_IPlugFrameVtbl* lpVtbl; } Frame;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_IPlugFrame_iid)) { *o = t; return Steinberg_kResultOk; }
    if (uid_eq(iid, Steinberg_Linux_IRunLoop_iid)) { *o = &runLoop; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
// resizeView accepts the size and resizes the view, like a host.
static int resizeRequests, resizeWidth, resizeHeight;
static bool customEditor;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_resize(void* t, struct Steinberg_IPlugView* v, struct Steinberg_ViewRect* r) {
    (void)t;
    resizeRequests++;
    resizeWidth = r->right - r->left;
    resizeHeight = r->bottom - r->top;
    return v->lpVtbl->onSize(v, r);
}

static void check_startup_resize(void) {
    if (customEditor) {
        CHECK(resizeRequests >= 1 && resizeWidth == 720 && resizeHeight == 420, "startup vst3go.resize request %dx%d (%d requests)", resizeWidth, resizeHeight, resizeRequests);
    } else {
        CHECK(resizeRequests == 0, "the built-in editor does not resize itself");
    }
}
static struct Steinberg_IPlugFrameVtbl frameVtbl = { f_qi, ref, ref, f_resize };

static void run_timers(Display* display, int ms) {
    struct timespec tick = { 0, 16 * 1000 * 1000 };
    for (int elapsed = 0; elapsed < ms; elapsed += 16) {
        if (runLoop.timer) runLoop.timer->lpVtbl->onTimer(runLoop.timer);
        while (XPending(display)) { XEvent e; XNextEvent(display, &e); }
        nanosleep(&tick, NULL);
    }
}

static void press(Display* display, KeySym sym) {
    KeyCode code = XKeysymToKeycode(display, sym);
    XTestFakeKeyEvent(display, code, True, 0);
    XTestFakeKeyEvent(display, code, False, 0);
    XFlush(display);
}

static Window first_child(Display* display, Window window) {
    Window root, parent, *children = NULL, child = 0;
    unsigned int count = 0;
    XQueryTree(display, window, &root, &parent, &children, &count);
    if (count > 0) child = children[0];
    if (children) XFree(children);
    return child;
}

static unsigned int child_count(Display* display, Window window) {
    Window root, parent, *children = NULL;
    unsigned int count = 0;
    XQueryTree(display, window, &root, &parent, &children, &count);
    if (children) XFree(children);
    return count;
}

static int check_vst3(Display* display, Window parent, const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen: %s", dlerror());
    typedef struct Steinberg_IPluginFactory* (*GetFactory)(void);
    struct Steinberg_IPluginFactory* f = ((GetFactory)dlsym(lib, "GetPluginFactory"))();
    struct Steinberg_PClassInfo ci;
    CHECK(f->lpVtbl->getClassInfo(f, 0, &ci) == Steinberg_kResultOk, "getClassInfo");
    struct Steinberg_Vst_IComponent* comp;
    struct Steinberg_Vst_IEditController* ctrl;
    CHECK(f->lpVtbl->createInstance(f, ci.cid, Steinberg_Vst_IComponent_iid, (void**)&comp) == Steinberg_kResultOk, "createInstance");
    CHECK(comp->lpVtbl->initialize(comp, NULL) == Steinberg_kResultOk, "initialize");
    CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IEditController_iid, (void**)&ctrl) == Steinberg_kResultOk, "QI controller");

    struct Steinberg_IPlugView* view = ctrl->lpVtbl->createView(ctrl, "editor");
    CHECK(view, "createView returned no editor");
    CHECK(view->lpVtbl->isPlatformTypeSupported(view, Steinberg_kPlatformTypeX11EmbedWindowID) == Steinberg_kResultOk, "X11 embedding supported");
    CHECK(view->lpVtbl->isPlatformTypeSupported(view, "HWND") == Steinberg_kResultFalse, "HWND unsupported");
    runLoop.lpVtbl = &runLoopVtbl;
    Frame frame = { &frameVtbl };
    CHECK(view->lpVtbl->setFrame(view, (struct Steinberg_IPlugFrame*)&frame) == Steinberg_kResultOk, "setFrame");
    CHECK(view->lpVtbl->attached(view, (void*)(uintptr_t)parent, Steinberg_kPlatformTypeX11EmbedWindowID) == Steinberg_kResultOk, "attached");
    CHECK(runLoop.registrations == 1 && runLoop.timer, "editor registers an IRunLoop timer");
    run_timers(display, 1500);
    CHECK(child_count(display, parent) >= 1, "editor window embedded into the host window");
    check_startup_resize();

    // Keyboard input in the page: tabbing to the controls and pressing End
    // moves a slider, whose change must arrive at the controller through the
    // vst3go script message handler.
    Steinberg_int32 count = ctrl->lpVtbl->getParameterCount(ctrl);
    CHECK(count > 0 && count <= 1024, "parameter count %d", (int)count);
    double before[1024];
    Steinberg_Vst_ParamID ids[1024];
    for (Steinberg_int32 i = 0; i < count; i++) {
        struct Steinberg_Vst_ParameterInfo info;
        ctrl->lpVtbl->getParameterInfo(ctrl, i, &info);
        ids[i] = info.id;
        before[i] = ctrl->lpVtbl->getParamNormalized(ctrl, info.id);
    }
    XSetInputFocus(display, first_child(display, parent), RevertToParent, CurrentTime);
    run_timers(display, 100);
    int changed = -1;
    for (int tab = 0; tab < 8 && changed < 0; tab++) {
        press(display, XK_Tab);
        run_timers(display, 50);
        press(display, XK_End);
        run_timers(display, 150);
        for (Steinberg_int32 i = 0; i < count && changed < 0; i++) {
            if (ctrl->lpVtbl->getParamNormalized(ctrl, ids[i]) != before[i]) changed = (int)i;
        }
    }
    CHECK(changed >= 0, "editor input reached no parameter (JS -> Go bridge)");
    printf("editor input changed param %u to %g\n", (unsigned)ids[changed], ctrl->lpVtbl->getParamNormalized(ctrl, ids[changed]));

    // Host-side parameter changes and resizing reach the live view.
    for (int i = 0; i < 10; i++) {
        ctrl->lpVtbl->setParamNormalized(ctrl, 0, i / 10.0);
        run_timers(display, 32);
    }
    struct Steinberg_ViewRect size = { 0, 0, 640, 480 };
    CHECK(view->lpVtbl->onSize(view, &size) == Steinberg_kResultOk, "onSize");
    run_timers(display, 200);

    CHECK(view->lpVtbl->removed(view) == Steinberg_kResultOk, "removed");
    CHECK(runLoop.timer == NULL, "timer unregistered on removed");
    XSync(display, False);
    CHECK(child_count(display, parent) == 0, "editor window destroyed on removed");
    view->lpVtbl->release(view);
    printf("editor ok\n");
    return 0;
}

// ---- CLAP host: gui, timer-support and params ----
#define MAX_TIMERS 8
static struct {
    struct { clap_id id; bool used; } timers[MAX_TIMERS];
    int timerRegistrations, resizeRequests, callbacks;
} clapHost;

static bool h_register_timer(const clap_host_t* h, uint32_t ms, clap_id* id) {
    (void)h; (void)ms;
    for (int i = 0; i < MAX_TIMERS; i++) {
        if (!clapHost.timers[i].used) {
            clapHost.timers[i].used = true;
            clapHost.timers[i].id = *id = (clap_id)(100 + clapHost.timerRegistrations++);
            return true;
        }
    }
    return false;
}
static bool h_unregister_timer(const clap_host_t* h, clap_id id) {
    (void)h;
    for (int i = 0; i < MAX_TIMERS; i++) {
        if (clapHost.timers[i].used && clapHost.timers[i].id == id) { clapHost.timers[i].used = false; return true; }
    }
    return false;
}
static int active_timers(void) {
    int n = 0;
    for (int i = 0; i < MAX_TIMERS; i++) n += clapHost.timers[i].used;
    return n;
}
static const clap_host_timer_support_t hostTimer = { h_register_timer, h_unregister_timer };
static void h_resize_hints_changed(const clap_host_t* h) { (void)h; }
static bool h_request_resize(const clap_host_t* h, uint32_t w, uint32_t hh) {
    (void)h;
    clapHost.resizeRequests++;
    resizeRequests++;
    resizeWidth = (int)w;
    resizeHeight = (int)hh;
    return true;
}
static bool h_request_show(const clap_host_t* h) { (void)h; return true; }
static bool h_request_hide(const clap_host_t* h) { (void)h; return true; }
static int guiClosed;
static void h_closed(const clap_host_t* h, bool destroyed) { (void)h; CHECK(!destroyed, "closed with destroyed"); guiClosed++; }
static const clap_host_gui_t hostGui = { h_resize_hints_changed, h_request_resize, h_request_show, h_request_hide, h_closed };
static void h_rescan(const clap_host_t* h, clap_param_rescan_flags f) { (void)h; (void)f; }
static void h_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags f) { (void)h; (void)id; (void)f; }
static void h_request_flush(const clap_host_t* h) { (void)h; }
static const clap_host_params_t hostParams = { h_rescan, h_clear, h_request_flush };
static const void* h_get_extension(const clap_host_t* h, const char* id) {
    (void)h;
    if (!strcmp(id, CLAP_EXT_GUI)) return &hostGui;
    if (!strcmp(id, CLAP_EXT_TIMER_SUPPORT)) return &hostTimer;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    return NULL;
}
static void h_request_restart(const clap_host_t* h) { (void)h; }
static void h_request_process(const clap_host_t* h) { (void)h; }
static void h_request_callback(const clap_host_t* h) { (void)h; clapHost.callbacks++; }
static const clap_host_t host = {
    CLAP_VERSION_INIT, NULL, "editorcheck", "vst3go", "", "1.0",
    h_get_extension, h_request_restart, h_request_process, h_request_callback,
};

typedef struct { clap_input_events_t list; clap_event_param_value_t events[1]; uint32_t count; } InEvents;
typedef struct { clap_output_events_t list; uint32_t values, gestures; } OutEvents;
static uint32_t in_size(const clap_input_events_t* l) { return ((const InEvents*)l->ctx)->count; }
static const clap_event_header_t* in_get(const clap_input_events_t* l, uint32_t i) { return &((const InEvents*)l->ctx)->events[i].header; }
static bool out_push(const clap_output_events_t* l, const clap_event_header_t* ev) {
    OutEvents* out = (OutEvents*)l->ctx;
    if (ev->type == CLAP_EVENT_PARAM_VALUE) out->values++;
    if (ev->type == CLAP_EVENT_PARAM_GESTURE_BEGIN || ev->type == CLAP_EVENT_PARAM_GESTURE_END) out->gestures++;
    return true;
}

// clap_run drives the plugin's timers and main-thread callbacks like a host
// event loop would.
static void clap_run(Display* display, const clap_plugin_t* plugin, const clap_plugin_timer_support_t* timer, int ms) {
    struct timespec tick = { 0, 16 * 1000 * 1000 };
    for (int elapsed = 0; elapsed < ms; elapsed += 16) {
        for (int i = 0; i < MAX_TIMERS; i++) {
            if (clapHost.timers[i].used) timer->on_timer(plugin, clapHost.timers[i].id);
        }
        if (clapHost.callbacks > 0) { clapHost.callbacks = 0; plugin->on_main_thread(plugin); }
        while (XPending(display)) { XEvent e; XNextEvent(display, &e); }
        nanosleep(&tick, NULL);
    }
}

static void clap_flush(const clap_plugin_t* plugin, const clap_plugin_params_t* params, InEvents* in, OutEvents* out) {
    in->list.ctx = in; in->list.size = in_size; in->list.get = in_get;
    memset(out, 0, sizeof(*out));
    out->list.ctx = out; out->list.try_push = out_push;
    params->flush(plugin, &in->list, &out->list);
}

// find_titled returns the top-level window with a title, 0 when none.
static Window find_titled(Display* display, const char* title) {
    Window root, parent, *children = NULL, found = 0;
    unsigned int count = 0;
    XQueryTree(display, DefaultRootWindow(display), &root, &parent, &children, &count);
    for (unsigned int i = 0; i < count && !found; i++) {
        char* name = NULL;
        if (XFetchName(display, children[i], &name) && name) {
            if (!strcmp(name, title)) found = children[i];
            XFree(name);
        }
    }
    if (children) XFree(children);
    return found;
}

static bool viewable(Display* display, Window window) {
    XWindowAttributes attributes;
    return XGetWindowAttributes(display, window, &attributes) && attributes.map_state == IsViewable;
}

// check_floating opens the editor in its own window, which the user can
// close; closing hides it and tells the host.
static void check_floating(Display* display, Window parent, const clap_plugin_t* plugin, const clap_plugin_gui_t* gui, const clap_plugin_timer_support_t* timer) {
    CHECK(gui->create(plugin, CLAP_WINDOW_API_X11, true), "floating create");
    clap_window_t window = { CLAP_WINDOW_API_X11, { .x11 = parent } };
    CHECK(!gui->set_parent(plugin, &window), "no parent for a floating gui");
    CHECK(gui->set_transient(plugin, &window), "set_transient");
    gui->suggest_title(plugin, "editorcheck floating");
    CHECK(gui->show(plugin), "floating show");
    clap_run(display, plugin, timer, 1500);
    Window floating = find_titled(display, "editorcheck floating");
    CHECK(floating && viewable(display, floating), "floating window shown");
    CHECK(child_count(display, floating) >= 1, "editor embedded into the floating window");
    Window owner = 0;
    CHECK(XGetTransientForHint(display, floating, &owner) && owner == parent, "transient for the host window");
    Atom protocols = XInternAtom(display, "WM_PROTOCOLS", False), del = XInternAtom(display, "WM_DELETE_WINDOW", False);
    XEvent close = { .xclient = { .type = ClientMessage, .window = floating, .message_type = protocols, .format = 32 } };
    close.xclient.data.l[0] = (long)del;
    close.xclient.data.l[1] = CurrentTime;
    XSendEvent(display, floating, False, NoEventMask, &close);
    XFlush(display);
    clap_run(display, plugin, timer, 300);
    CHECK(guiClosed == 1 && !viewable(display, floating), "closing hides the window and tells the host (%d)", guiClosed);
    CHECK(gui->show(plugin), "shown again");
    clap_run(display, plugin, timer, 200);
    CHECK(viewable(display, floating), "visible again");
    gui->destroy(plugin);
    clap_run(display, plugin, timer, 100);
    CHECK(active_timers() == 0 && !find_titled(display, "editorcheck floating"), "floating window destroyed");
    printf("clap floating editor ok\n");
}

static int check_clap(Display* display, Window parent, const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen: %s", dlerror());
    const clap_plugin_entry_t* entry = (const clap_plugin_entry_t*)dlsym(lib, "clap_entry");
    CHECK(entry && entry->init(path), "clap_entry");
    const clap_plugin_factory_t* factory = (const clap_plugin_factory_t*)entry->get_factory(CLAP_PLUGIN_FACTORY_ID);
    const clap_plugin_descriptor_t* desc = factory->get_plugin_descriptor(factory, 0);
    const clap_plugin_t* plugin = factory->create_plugin(factory, &host, desc->id);
    CHECK(plugin && plugin->init(plugin), "create and init");
    const clap_plugin_gui_t* gui = (const clap_plugin_gui_t*)plugin->get_extension(plugin, CLAP_EXT_GUI);
    const clap_plugin_timer_support_t* timer = (const clap_plugin_timer_support_t*)plugin->get_extension(plugin, CLAP_EXT_TIMER_SUPPORT);
    const clap_plugin_params_t* params = (const clap_plugin_params_t*)plugin->get_extension(plugin, CLAP_EXT_PARAMS);
    CHECK(gui && timer && params, "gui, timer-support and params extensions");

    const char* api = NULL;
    bool floating = true;
    CHECK(gui->get_preferred_api(plugin, &api, &floating) && api && !strcmp(api, CLAP_WINDOW_API_X11) && !floating, "preferred api x11 embedded");
    CHECK(gui->is_api_supported(plugin, CLAP_WINDOW_API_X11, false), "x11 supported");
    CHECK(gui->is_api_supported(plugin, CLAP_WINDOW_API_X11, true), "floating supported");
    CHECK(!gui->is_api_supported(plugin, CLAP_WINDOW_API_WIN32, false), "win32 unsupported");
    CHECK(gui->create(plugin, CLAP_WINDOW_API_X11, false), "gui create");
    CHECK(!gui->create(plugin, CLAP_WINDOW_API_X11, false), "second gui create refused");
    uint32_t width = 0, height = 0;
    CHECK(gui->get_size(plugin, &width, &height) && width > 0 && height > 0, "get_size %ux%u", width, height);
    gui->set_scale(plugin, 1.0);
    clap_window_t window = { CLAP_WINDOW_API_X11, { .x11 = parent } };
    CHECK(gui->set_parent(plugin, &window), "set_parent");
    CHECK(gui->show(plugin), "show");
    CHECK(active_timers() == 1, "editor registers a host timer (%d)", active_timers());
    clap_run(display, plugin, timer, 1500);
    CHECK(child_count(display, parent) >= 1, "editor window embedded into the host window");
    check_startup_resize();

    // Keyboard input in the page reaches the parameters, and the edit goes
    // out to the host with its gestures on the next flush.
    uint32_t count = params->count(plugin);
    CHECK(count > 0 && count <= 1024, "parameter count %u", count);
    double before[1024];
    clap_id ids[1024];
    for (uint32_t i = 0; i < count; i++) {
        clap_param_info_t info;
        CHECK(params->get_info(plugin, i, &info), "param info %u", i);
        ids[i] = info.id;
        CHECK(params->get_value(plugin, info.id, &before[i]), "param value %u", i);
    }
    XSetInputFocus(display, first_child(display, parent), RevertToParent, CurrentTime);
    clap_run(display, plugin, timer, 100);
    int changed = -1;
    for (int tab = 0; tab < 8 && changed < 0; tab++) {
        press(display, XK_Tab);
        clap_run(display, plugin, timer, 50);
        press(display, XK_End);
        clap_run(display, plugin, timer, 150);
        for (uint32_t i = 0; i < count && changed < 0; i++) {
            double v;
            if (params->get_value(plugin, ids[i], &v) && v != before[i]) changed = (int)i;
        }
    }
    CHECK(changed >= 0, "editor input reached no parameter (JS -> Go bridge)");
    InEvents in = { .count = 0 };
    OutEvents out;
    clap_flush(plugin, params, &in, &out);
    CHECK(out.values >= 1 && out.gestures >= 2, "editor edit sent to the host (%u values, %u gestures)", out.values, out.gestures);
    printf("clap editor input changed param %u\n", (unsigned)ids[changed]);

    // Host automation is shown from the main thread, then resizing.
    for (int i = 0; i < 10; i++) {
        memset(&in.events[0], 0, sizeof(in.events[0]));
        in.events[0].header = (clap_event_header_t){ sizeof(clap_event_param_value_t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 };
        in.events[0].param_id = ids[0];
        in.events[0].note_id = -1; in.events[0].port_index = -1; in.events[0].channel = -1; in.events[0].key = -1;
        in.events[0].value = before[0];
        in.count = 1;
        clap_flush(plugin, params, &in, &out);
        CHECK(clapHost.callbacks >= 1, "host automation requests an editor refresh");
        clap_run(display, plugin, timer, 32);
    }
    CHECK(gui->set_size(plugin, 640, 480), "set_size");
    clap_run(display, plugin, timer, 200);
    CHECK(gui->hide(plugin), "hide");

    gui->destroy(plugin);
    CHECK(active_timers() == 0, "timer unregistered on destroy");
    XSync(display, False);
    CHECK(child_count(display, parent) == 0, "editor window destroyed on destroy");
    check_floating(display, parent, plugin, gui, timer);
    plugin->destroy(plugin);
    entry->deinit();
    printf("clap editor ok\n");
    return 0;
}

int main(int argc, char** argv) {
    bool clap = false;
    int arg = 1;
    for (; arg < argc - 1; arg++) {
        if (!strcmp(argv[arg], "--clap")) clap = true;
        else if (!strcmp(argv[arg], "--custom")) customEditor = true;
        else break;
    }
    CHECK(arg == argc - 1, "usage: editorcheck [--clap] [--custom] <plugin-binary>");
    Display* display = XOpenDisplay(NULL);
    CHECK(display, "no X display (run under xvfb-run)");
    Window parent = XCreateSimpleWindow(display, DefaultRootWindow(display), 0, 0, 900, 640, 0, 0, 0);
    XMapWindow(display, parent);
    XSync(display, False);
    return clap ? check_clap(display, parent, argv[arg]) : check_vst3(display, parent, argv[arg]);
}
