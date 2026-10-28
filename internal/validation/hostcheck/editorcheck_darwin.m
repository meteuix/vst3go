// editorcheck_darwin opens the editor of a plugin in an NSWindow, the way a
// macOS host does, as a VST3 (IPlugView attached to an NSView) or with
// --clap through the CLAP gui extension. It checks that the WKWebView embeds
// into the host view, that a slider edit in the page reaches the parameters
// through the JS -> Go bridge (and, under CLAP, the host's output events),
// that host parameter changes reach the page, resizing, and that closing
// the editor removes its view. Needs a window server session.
//
// usage: editorcheck_darwin [--clap] <plugin-binary>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "vst3/vst3_c_api.h"
#include "clap/clap.h"
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

static const clap_plugin_t* clapPlugin;
static int clapCallbacks;

// run spins the main run loop like a host event loop, serving CLAP
// main-thread callbacks.
static void run(double seconds) {
    NSDate* until = [NSDate dateWithTimeIntervalSinceNow:seconds];
    while ([until timeIntervalSinceNow] > 0) {
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.016]];
        if (clapPlugin && clapCallbacks > 0) {
            clapCallbacks = 0;
            clapPlugin->on_main_thread(clapPlugin);
        }
    }
}

static WKWebView* findWebView(NSView* view) {
    if ([view isKindOfClass:[WKWebView class]]) return (WKWebView*)view;
    for (NSView* child in view.subviews) {
        WKWebView* found = findWebView(child);
        if (found) return found;
    }
    return nil;
}

// eval runs a script in the page and waits for its string result.
static NSString* eval(WKWebView* web, NSString* script) {
    __block NSString* result = nil;
    __block BOOL done = NO;
    [web evaluateJavaScript:script completionHandler:^(id value, NSError* error) {
        result = error ? [NSString stringWithFormat:@"error: %@", error.localizedDescription] : [NSString stringWithFormat:@"%@", value];
        done = YES;
    }];
    for (int i = 0; i < 300 && !done; i++) run(0.01);
    return result;
}

// waitForControls waits until the page has rendered its parameter sliders.
static WKWebView* waitForControls(NSView* parent) {
    for (int i = 0; i < 100; i++) {
        run(0.05);
        WKWebView* web = findWebView(parent);
        if (web && [eval(web, @"String(document.querySelectorAll('input[type=range]:not(:disabled)').length)") intValue] > 0) return web;
    }
    CHECK(0, "editor page rendered no sliders");
    return nil;
}

// editFirstSlider moves the first slider to 1 with the page's gesture
// events, as a mouse drag would.
static void editFirstSlider(WKWebView* web) {
    NSString* result = eval(web, @"(() => {"
        "const el = document.querySelector('input[type=range]:not(:disabled)');"
        "el.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));"
        "el.value = '1';"
        "el.dispatchEvent(new Event('input', { bubbles: true }));"
        "el.dispatchEvent(new PointerEvent('pointerup', { bubbles: true }));"
        "el.dispatchEvent(new Event('change', { bubbles: true }));"
        "return 'ok'; })()");
    CHECK([result isEqualToString:@"ok"], "slider edit: %s", result.UTF8String);
}

static double firstSliderValue(WKWebView* web) {
    return [eval(web, @"document.querySelector('input[type=range]:not(:disabled)').value") doubleValue];
}

// ---- VST3 ----

static int uid_eq(const Steinberg_TUID a, const Steinberg_TUID b) { return memcmp(a, b, sizeof(Steinberg_TUID)) == 0; }
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE ref(void* t) { (void)t; return 1; }
typedef struct { struct Steinberg_IPlugFrameVtbl* lpVtbl; } Frame;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_IPlugFrame_iid)) { *o = t; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_resize(void* t, struct Steinberg_IPlugView* v, struct Steinberg_ViewRect* r) { (void)t; (void)v; (void)r; return Steinberg_kResultOk; }
static struct Steinberg_IPlugFrameVtbl frameVtbl = { f_qi, ref, ref, f_resize };

static void checkVST3(NSView* parent, const char* path) {
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
    CHECK(view, "createView");
    CHECK(view->lpVtbl->isPlatformTypeSupported(view, Steinberg_kPlatformTypeNSView) == Steinberg_kResultOk, "NSView supported");
    Frame frame = { &frameVtbl };
    CHECK(view->lpVtbl->setFrame(view, (struct Steinberg_IPlugFrame*)&frame) == Steinberg_kResultOk, "setFrame");
    CHECK(view->lpVtbl->attached(view, (__bridge void*)parent, Steinberg_kPlatformTypeNSView) == Steinberg_kResultOk, "attached");
    WKWebView* web = waitForControls(parent);

    struct Steinberg_Vst_ParameterInfo info;
    CHECK(ctrl->lpVtbl->getParameterInfo(ctrl, 0, &info) == Steinberg_kResultOk, "parameter info");
    editFirstSlider(web);
    run(0.2);
    CHECK(ctrl->lpVtbl->getParamNormalized(ctrl, info.id) == 1, "slider edit reached param %u (JS -> Go bridge)", (unsigned)info.id);

    ctrl->lpVtbl->setParamNormalized(ctrl, info.id, 0.25);
    run(0.2);
    CHECK(firstSliderValue(web) == 0.25, "host change reached the page (Go -> JS)");

    struct Steinberg_ViewRect size = { 0, 0, 640, 480 };
    CHECK(view->lpVtbl->onSize(view, &size) == Steinberg_kResultOk, "onSize");
    run(0.1);
    CHECK(view->lpVtbl->removed(view) == Steinberg_kResultOk, "removed");
    CHECK(parent.subviews.count == 0, "editor view removed");
    view->lpVtbl->release(view);
    printf("editor ok\n");
}

// ---- CLAP ----

static bool h_request_resize(const clap_host_t* h, uint32_t w, uint32_t hh) { (void)h; (void)w; (void)hh; return true; }
static bool h_true(const clap_host_t* h) { (void)h; return true; }
static void h_hints(const clap_host_t* h) { (void)h; }
static void h_closed(const clap_host_t* h, bool destroyed) { (void)h; (void)destroyed; }
static const clap_host_gui_t hostGui = { h_hints, h_request_resize, h_true, h_true, h_closed };
static void h_rescan(const clap_host_t* h, clap_param_rescan_flags f) { (void)h; (void)f; }
static void h_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags f) { (void)h; (void)id; (void)f; }
static void h_request_flush(const clap_host_t* h) { (void)h; }
static const clap_host_params_t hostParams = { h_rescan, h_clear, h_request_flush };
static const void* h_get_extension(const clap_host_t* h, const char* id) {
    (void)h;
    if (!strcmp(id, CLAP_EXT_GUI)) return &hostGui;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    return NULL;
}
static void h_noop(const clap_host_t* h) { (void)h; }
static void h_request_callback(const clap_host_t* h) { (void)h; clapCallbacks++; }
static const clap_host_t host = {
    CLAP_VERSION_INIT, NULL, "editorcheck", "vst3go", "", "1.0",
    h_get_extension, h_noop, h_noop, h_request_callback,
};

typedef struct { clap_input_events_t list; clap_event_param_value_t event; uint32_t count; } InEvents;
typedef struct { clap_output_events_t list; uint32_t values, gestures; } OutEvents;
static uint32_t in_size(const clap_input_events_t* l) { return ((const InEvents*)l->ctx)->count; }
static const clap_event_header_t* in_get(const clap_input_events_t* l, uint32_t i) { (void)i; return &((const InEvents*)l->ctx)->event.header; }
static bool out_push(const clap_output_events_t* l, const clap_event_header_t* ev) {
    OutEvents* out = (OutEvents*)l->ctx;
    if (ev->type == CLAP_EVENT_PARAM_VALUE) out->values++;
    if (ev->type == CLAP_EVENT_PARAM_GESTURE_BEGIN || ev->type == CLAP_EVENT_PARAM_GESTURE_END) out->gestures++;
    return true;
}
static void flush(const clap_plugin_params_t* params, InEvents* in, OutEvents* out) {
    in->list.ctx = in; in->list.size = in_size; in->list.get = in_get;
    memset(out, 0, sizeof(*out));
    out->list.ctx = out; out->list.try_push = out_push;
    params->flush(clapPlugin, &in->list, &out->list);
}

static void checkCLAP(NSView* parent, const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen: %s", dlerror());
    const clap_plugin_entry_t* entry = (const clap_plugin_entry_t*)dlsym(lib, "clap_entry");
    CHECK(entry && entry->init(path), "clap_entry");
    const clap_plugin_factory_t* factory = (const clap_plugin_factory_t*)entry->get_factory(CLAP_PLUGIN_FACTORY_ID);
    const clap_plugin_descriptor_t* desc = factory->get_plugin_descriptor(factory, 0);
    clapPlugin = factory->create_plugin(factory, &host, desc->id);
    CHECK(clapPlugin && clapPlugin->init(clapPlugin), "create and init");
    const clap_plugin_gui_t* gui = (const clap_plugin_gui_t*)clapPlugin->get_extension(clapPlugin, CLAP_EXT_GUI);
    const clap_plugin_params_t* params = (const clap_plugin_params_t*)clapPlugin->get_extension(clapPlugin, CLAP_EXT_PARAMS);
    CHECK(gui && params, "gui and params extensions");

    CHECK(gui->is_api_supported(clapPlugin, CLAP_WINDOW_API_COCOA, false), "cocoa supported");
    CHECK(gui->create(clapPlugin, CLAP_WINDOW_API_COCOA, false), "gui create");
    uint32_t width = 0, height = 0;
    CHECK(gui->get_size(clapPlugin, &width, &height) && width > 0 && height > 0, "get_size");
    CHECK(gui->set_scale(clapPlugin, 2.0) || true, "set_scale");
    clap_window_t window = { CLAP_WINDOW_API_COCOA, { .cocoa = (__bridge void*)parent } };
    CHECK(gui->set_parent(clapPlugin, &window), "set_parent");
    CHECK(gui->show(clapPlugin), "show");
    WKWebView* web = waitForControls(parent);

    clap_param_info_t info;
    CHECK(params->get_info(clapPlugin, 0, &info), "param info");
    editFirstSlider(web);
    run(0.2);
    double value;
    CHECK(params->get_value(clapPlugin, info.id, &value) && value == info.max_value, "slider edit reached param %u: %g (JS -> Go bridge)", (unsigned)info.id, value);
    InEvents in = { .count = 0 };
    OutEvents out;
    flush(params, &in, &out);
    CHECK(out.values >= 1 && out.gestures == 2, "editor edit sent to the host (%u values, %u gestures)", out.values, out.gestures);

    // Host automation reaches the page through a main-thread callback.
    memset(&in.event, 0, sizeof(in.event));
    in.event.header = (clap_event_header_t){ sizeof(clap_event_param_value_t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 };
    in.event.param_id = info.id;
    in.event.note_id = -1; in.event.port_index = -1; in.event.channel = -1; in.event.key = -1;
    in.event.value = info.min_value;
    in.count = 1;
    flush(params, &in, &out);
    CHECK(clapCallbacks == 1, "host automation requests a main-thread editor refresh");
    run(0.2);
    CHECK(firstSliderValue(web) == 0, "host automation reached the page (Go -> JS)");

    CHECK(gui->set_size(clapPlugin, 640, 480), "set_size");
    run(0.1);
    CHECK(gui->hide(clapPlugin), "hide");
    gui->destroy(clapPlugin);
    CHECK(parent.subviews.count == 0, "editor view removed on destroy");
    clapPlugin->destroy(clapPlugin);
    clapPlugin = NULL;
    entry->deinit();
    printf("clap editor ok\n");
}

int main(int argc, char** argv) {
    @autoreleasepool {
        bool clap = argc == 3 && !strcmp(argv[1], "--clap");
        CHECK(argc == 2 || clap, "usage: editorcheck_darwin [--clap] <plugin-binary>");
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        NSWindow* window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 900, 640)
                                                       styleMask:NSWindowStyleMaskTitled
                                                         backing:NSBackingStoreBuffered
                                                           defer:NO];
        NSView* parent = [[NSView alloc] initWithFrame:window.contentView.bounds];
        [window.contentView addSubview:parent];
        [window orderFrontRegardless];
        if (clap) {
            checkCLAP(parent, argv[2]);
        } else {
            checkVST3(parent, argv[1]);
        }
    }
    return 0;
}
