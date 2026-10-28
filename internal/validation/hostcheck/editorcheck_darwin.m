// editorcheck_darwin opens the editor of a plugin in an NSWindow, the way a
// macOS host does, as a VST3 (IPlugView attached to an NSView) or with
// --clap through the CLAP gui extension. It checks that the WKWebView embeds
// into the host view, that a slider edit in the page reaches the parameters
// through the JS -> Go bridge (and, under CLAP, the host's output events),
// that host parameter changes reach the page, the vst3go SDK in the page
// (edits with gestures, values and text from the host), resizing, and that
// closing the editor removes its view. With --custom it checks the synth's
// custom editor instead of the built-in one: its startup vst3go.resize
// request and a vst3go.send/on round trip with the processor. Needs a window
// server session.
//
// usage: editorcheck_darwin [--clap] [--custom] <plugin-binary>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "vst3/vst3_c_api.h"
#include "clap/clap.h"
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <string.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

static const clap_plugin_t* clapPlugin;
static int clapCallbacks;
static bool customEditor;
static int resizeRequests;
static int32_t resizeWidth, resizeHeight;

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

// waitFor runs the page until a script returns "true".
static bool waitFor(WKWebView* web, NSString* script) {
    for (int i = 0; i < 100; i++) {
        if ([eval(web, script) isEqualToString:@"true"]) return true;
        run(0.02);
    }
    return false;
}

typedef double (^GetNormalized)(uint32_t id);
typedef void (^HostSet)(uint32_t id, double normalized);

// checkSDK drives the page's vst3go SDK: an edit gesture on the first
// continuous parameter, then a host change that must reach the SDK's value,
// text and change listeners.
static void checkSDK(WKWebView* web, GetNormalized get, HostSet set) {
    CHECK([eval(web, @"String(typeof vst3go === 'object' && vst3go.version === 1 && vst3go.params.length > 0)") isEqualToString:@"true"], "vst3go SDK in the page");
    NSString* result = eval(web, @"(() => {"
        "const p = vst3go.params.find((p) => !p.readOnly && !p.hidden && p.stepCount === 0);"
        "window.__changes = 0;"
        "vst3go.onParamChange((q) => { if (q === p) window.__changes++; });"
        "p.beginEdit(); p.setNormalized(0.5); p.setNormalized(0.75); p.endEdit();"
        "return String(p.id); })()");
    uint32_t id = (uint32_t)[result longLongValue];
    run(0.2);
    CHECK(fabs(get(id) - 0.75) < 1e-9, "SDK edit of param %u reached the plugin: %g (%s)", (unsigned)id, get(id), result.UTF8String);
    set(id, 0.25);
    NSString* check = [NSString stringWithFormat:@"String(vst3go.param(%u).normalized === 0.25 && vst3go.param(%u).text !== '' && window.__changes >= 3)", (unsigned)id, (unsigned)id];
    CHECK(waitFor(web, check), "host change reached the SDK: %s", eval(web, [NSString stringWithFormat:@"JSON.stringify([vst3go.param(%u).normalized, vst3go.param(%u).text, window.__changes])", (unsigned)id, (unsigned)id]).UTF8String);
}

// checkCustom checks the synth editor: it asks for 720x420 at startup and
// shows the processor's replies to its "status" messages.
static void checkCustom(WKWebView* web) {
    run(0.3);
    CHECK(resizeRequests >= 1 && resizeWidth == 720 && resizeHeight == 420, "startup resize request %dx%d (%d requests)", resizeWidth, resizeHeight, resizeRequests);
    eval(web, @"vst3go.resize(5000, 100); 'ok'");
    run(0.1);
    CHECK(resizeWidth == 1200 && resizeHeight == 320, "resize request clamped to the editor limits: %dx%d", resizeWidth, resizeHeight);
    eval(web, @"window.__status = null; vst3go.on('status', (s) => { window.__status = s; }); vst3go.send('status'); 'ok'");
    CHECK(waitFor(web, @"String(window.__status !== null && window.__status.voices === 0 && window.__status.learnedCC === -1)"), "processor answered vst3go.send through Host.PostEditorMessage: %s", eval(web, @"JSON.stringify(window.__status)").UTF8String);
    CHECK(waitFor(web, @"String(document.getElementById('status').textContent === '0 voices')"), "page shows the status");
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
// resizeView accepts the size and resizes the view, like a host.
static Steinberg_tresult SMTG_STDMETHODCALLTYPE f_resize(void* t, struct Steinberg_IPlugView* v, struct Steinberg_ViewRect* r) {
    (void)t;
    resizeRequests++;
    resizeWidth = r->right - r->left;
    resizeHeight = r->bottom - r->top;
    return v->lpVtbl->onSize(v, r);
}
static struct Steinberg_IPlugFrameVtbl frameVtbl = { f_qi, ref, ref, f_resize };

// IComponentHandler: accepts the plugin's edits, so the processor gets its Host.
typedef struct { struct Steinberg_Vst_IComponentHandlerVtbl* lpVtbl; } Handler;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE ch_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_Vst_IComponentHandler_iid)) { *o = t; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE ch_edit(void* t, Steinberg_Vst_ParamID id) { (void)t; (void)id; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE ch_perform(void* t, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v) { (void)t; (void)id; (void)v; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE ch_restart(void* t, Steinberg_int32 flags) { (void)t; (void)flags; return Steinberg_kResultOk; }
static struct Steinberg_Vst_IComponentHandlerVtbl handlerVtbl = { ch_qi, ref, ref, ch_edit, ch_perform, ch_edit, ch_restart };
static Handler handler = { &handlerVtbl };

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
    CHECK(ctrl->lpVtbl->setComponentHandler(ctrl, (struct Steinberg_Vst_IComponentHandler*)&handler) == Steinberg_kResultOk, "setComponentHandler");

    struct Steinberg_IPlugView* view = ctrl->lpVtbl->createView(ctrl, "editor");
    CHECK(view, "createView");
    CHECK(view->lpVtbl->isPlatformTypeSupported(view, Steinberg_kPlatformTypeNSView) == Steinberg_kResultOk, "NSView supported");
    Frame frame = { &frameVtbl };
    CHECK(view->lpVtbl->setFrame(view, (struct Steinberg_IPlugFrame*)&frame) == Steinberg_kResultOk, "setFrame");
    CHECK(view->lpVtbl->attached(view, (__bridge void*)parent, Steinberg_kPlatformTypeNSView) == Steinberg_kResultOk, "attached");
    WKWebView* web = waitForControls(parent);

    if (customEditor) {
        checkCustom(web);
    } else {
        struct Steinberg_Vst_ParameterInfo info;
        CHECK(ctrl->lpVtbl->getParameterInfo(ctrl, 0, &info) == Steinberg_kResultOk, "parameter info");
        editFirstSlider(web);
        run(0.2);
        CHECK(ctrl->lpVtbl->getParamNormalized(ctrl, info.id) == 1, "slider edit reached param %u (JS -> Go bridge)", (unsigned)info.id);
        ctrl->lpVtbl->setParamNormalized(ctrl, info.id, 0.25);
        run(0.2);
        CHECK(firstSliderValue(web) == 0.25, "host change reached the page (Go -> JS)");
    }
    checkSDK(web, ^double(uint32_t id) { return ctrl->lpVtbl->getParamNormalized(ctrl, id); },
        ^(uint32_t id, double v) { ctrl->lpVtbl->setParamNormalized(ctrl, id, v); });

    struct Steinberg_ViewRect size = { 0, 0, 640, 480 };
    CHECK(view->lpVtbl->onSize(view, &size) == Steinberg_kResultOk, "onSize");
    run(0.1);
    CHECK(view->lpVtbl->removed(view) == Steinberg_kResultOk, "removed");
    CHECK(parent.subviews.count == 0, "editor view removed");
    view->lpVtbl->release(view);
    printf("editor ok\n");
}

// ---- CLAP ----

static bool h_request_resize(const clap_host_t* h, uint32_t w, uint32_t hh) {
    (void)h;
    resizeRequests++;
    resizeWidth = (int32_t)w;
    resizeHeight = (int32_t)hh;
    return true;
}
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

    __block InEvents in = { .count = 0 };
    OutEvents out;
    // hostSet automates a parameter (plain domain) and lets the plugin
    // refresh the page from the main thread.
    void (^hostSet)(clap_id, double) = ^(clap_id id, double plain) {
        memset(&in.event, 0, sizeof(in.event));
        in.event.header = (clap_event_header_t){ sizeof(clap_event_param_value_t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 };
        in.event.param_id = id;
        in.event.note_id = -1; in.event.port_index = -1; in.event.channel = -1; in.event.key = -1;
        in.event.value = plain;
        in.count = 1;
        OutEvents ignored;
        flush(params, &in, &ignored);
        in.count = 0;
        CHECK(clapCallbacks == 1, "host automation requests a main-thread editor refresh");
    };
    clap_param_info_t (^infoOf)(clap_id) = ^clap_param_info_t(clap_id id) {
        clap_param_info_t info;
        for (uint32_t i = 0; i < params->count(clapPlugin); i++) {
            if (params->get_info(clapPlugin, i, &info) && info.id == id) return info;
        }
        CHECK(0, "no param %u", (unsigned)id);
        return info;
    };

    if (customEditor) {
        checkCustom(web);
    } else {
        clap_param_info_t info;
        CHECK(params->get_info(clapPlugin, 0, &info), "param info");
        editFirstSlider(web);
        run(0.2);
        double value;
        CHECK(params->get_value(clapPlugin, info.id, &value) && value == info.max_value, "slider edit reached param %u: %g (JS -> Go bridge)", (unsigned)info.id, value);
        flush(params, &in, &out);
        CHECK(out.values >= 1 && out.gestures == 2, "editor edit sent to the host (%u values, %u gestures)", out.values, out.gestures);
        hostSet(info.id, info.min_value);
        run(0.2);
        CHECK(firstSliderValue(web) == 0, "host automation reached the page (Go -> JS): %s", eval(web, @"JSON.stringify(vst3go.params.map((p) => [p.id, p.normalized, p.text]))").UTF8String);
    }
    checkSDK(web, ^double(uint32_t id) {
        clap_param_info_t info = infoOf(id);
        double plain;
        CHECK(params->get_value(clapPlugin, id, &plain), "get_value");
        return (plain - info.min_value) / (info.max_value - info.min_value);
    }, ^(uint32_t id, double normalized) {
        clap_param_info_t info = infoOf(id);
        hostSet(id, info.min_value + normalized * (info.max_value - info.min_value));
    });
    flush(params, &in, &out);

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
        bool clap = false;
        int arg = 1;
        for (; arg < argc - 1; arg++) {
            if (!strcmp(argv[arg], "--clap")) clap = true;
            else if (!strcmp(argv[arg], "--custom")) customEditor = true;
            else break;
        }
        CHECK(arg == argc - 1, "usage: editorcheck_darwin [--clap] [--custom] <plugin-binary>");
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
            checkCLAP(parent, argv[arg]);
        } else {
            checkVST3(parent, argv[arg]);
        }
    }
    return 0;
}
