// editorcheck opens the editor of a plugin built with the Linux WebKitGTK
// editor (build tag webkitgtk) in an X11 window, the way a Linux VST3 host
// does: IPlugFrame with an IRunLoop whose timers the host drives. It checks
// that the view embeds into the host window, that keyboard input in the page
// changes a parameter through the JS -> Go bridge (XTest), that it survives
// host parameter updates and resizing, and that it detaches cleanly. Run it under an X server (e.g. xvfb-run).
//
// usage: editorcheck <plugin-binary>
#include "vst3/vst3_c_api.h"
#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>
#include <X11/keysym.h>
#include <dlfcn.h>
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
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_resize(void* t, struct Steinberg_IPlugView* v, struct Steinberg_ViewRect* r) { (void)t; (void)v; (void)r; return Steinberg_kResultOk; }
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

int main(int argc, char** argv) {
    CHECK(argc == 2, "usage: editorcheck <plugin-binary>");
    Display* display = XOpenDisplay(NULL);
    CHECK(display, "no X display (run under xvfb-run)");
    Window parent = XCreateSimpleWindow(display, DefaultRootWindow(display), 0, 0, 900, 640, 0, 0, 0);
    XMapWindow(display, parent);
    XSync(display, False);

    void* lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
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

    // Keyboard input in the page: tabbing to the controls and pressing End
    // moves a slider, whose change must arrive at the controller through the
    // vst3go script message handler.
    Steinberg_int32 count = ctrl->lpVtbl->getParameterCount(ctrl);
    CHECK(count > 0 && count <= 64, "parameter count %d", (int)count);
    double before[64];
    Steinberg_Vst_ParamID ids[64];
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
