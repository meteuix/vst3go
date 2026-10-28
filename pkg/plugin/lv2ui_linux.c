// The X11 UI of the LV2 plugins: it opens the plugin's own CLAP editor
// (the WebKitGTK view, build tag webkitgtk) in the host's window. The UI
// reaches the plugin instance through instance-access, so the editor talks
// to the processor directly; edits it makes go to the host as control port
// writes (and ui:touch gestures) from the idle callback, which also runs
// the editor's timers and main-thread callbacks.

#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include <lv2/core/lv2.h>
#include <lv2/core/lv2_util.h>
#include <lv2/instance-access/instance-access.h>
#include <lv2/ui/ui.h>

#include "lv2_linux.h"

_Thread_local unsigned long vst3goLinuxEditorWindow;

enum { UI_MAX_TIMERS = 16 };

typedef struct {
    clap_id id;
    uint32_t period;  // ms
    int64_t due;      // ms, monotonic
} UITimer;

typedef struct {
    LV2Instance* in;
    const clap_plugin_t* plugin;
    const clap_plugin_gui_t* gui;
    LV2UI_Write_Function write;
    LV2UI_Controller controller;
    LV2UI_Resize* resize;
    LV2UI_Touch* touch;
    bool closed;
} UI;

// Timers belong to the plugin instance (the editor registers them while it
// is open; the host has one UI thread).
typedef struct {
    LV2Instance* in;
    UITimer timers[UI_MAX_TIMERS];
} TimerSet;

static TimerSet timerSets[64];
static clap_id nextTimerID = 1;

static int64_t now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static TimerSet* timer_set(LV2Instance* in, bool create) {
    TimerSet* free_set = NULL;
    for (size_t i = 0; i < sizeof(timerSets) / sizeof(timerSets[0]); i++) {
        if (timerSets[i].in == in) return &timerSets[i];
        if (!timerSets[i].in && !free_set) free_set = &timerSets[i];
    }
    if (!create || !free_set) return NULL;
    memset(free_set, 0, sizeof(*free_set));
    free_set->in = in;
    return free_set;
}

static void release_timer_set(LV2Instance* in) {
    TimerSet* set = timer_set(in, false);
    if (set) memset(set, 0, sizeof(*set));
}

// ---- CLAP host extensions for the editor ----

static LV2Instance* gui_instance(const clap_host_t* h) { return (LV2Instance*)h->host_data; }

static void gui_resize_hints_changed(const clap_host_t* h) { (void)h; }

static bool gui_request_resize(const clap_host_t* h, uint32_t width, uint32_t height) {
    UI* ui = (UI*)vst3go_lv2_ui(gui_instance(h));
    if (!ui || !width || !height) return false;
    if (ui->resize && ui->resize->ui_resize(ui->resize->handle, (int)width, (int)height) != 0) return false;
    ui->gui->set_size(ui->plugin, width, height);
    return true;
}

static bool gui_request_show(const clap_host_t* h) { (void)h; return false; }
static bool gui_request_hide(const clap_host_t* h) { (void)h; return false; }

static void gui_closed(const clap_host_t* h, bool destroyed) {
    (void)destroyed;
    UI* ui = (UI*)vst3go_lv2_ui(gui_instance(h));
    if (ui) ui->closed = true;
}

static const clap_host_gui_t hostGui = {
    gui_resize_hints_changed, gui_request_resize, gui_request_show, gui_request_hide, gui_closed,
};

static bool timer_register(const clap_host_t* h, uint32_t period, clap_id* id) {
    TimerSet* set = timer_set(gui_instance(h), true);
    if (!set || !id) return false;
    for (size_t i = 0; i < UI_MAX_TIMERS; i++) {
        if (set->timers[i].id) continue;
        if (period < 1) period = 1;
        set->timers[i] = (UITimer){ nextTimerID++, period, now_ms() + period };
        *id = set->timers[i].id;
        return true;
    }
    return false;
}

static bool timer_unregister(const clap_host_t* h, clap_id id) {
    TimerSet* set = timer_set(gui_instance(h), false);
    if (!set) return false;
    for (size_t i = 0; i < UI_MAX_TIMERS; i++) {
        if (set->timers[i].id == id) {
            set->timers[i].id = 0;
            return true;
        }
    }
    return false;
}

static const clap_host_timer_support_t hostTimer = { timer_register, timer_unregister };

const void* vst3go_lv2_ui_host_extension(LV2Instance* in, const char* id) {
    (void)in;
    if (!strcmp(id, CLAP_EXT_GUI)) return &hostGui;
    if (!strcmp(id, CLAP_EXT_TIMER_SUPPORT)) return &hostTimer;
    return NULL;
}

bool vst3go_lv2_ui_supported(LV2Instance* in) {
    const clap_plugin_gui_t* gui = vst3go_lv2_gui(in);
    return gui && vst3go_lv2_timer(in) && gui->is_api_supported(vst3go_lv2_plugin(in), CLAP_WINDOW_API_X11, false);
}

// ---- LV2 UI ----

static LV2UI_Descriptor* uiDescriptors;
static uint32_t uiDescriptorCount;

static LV2UI_Handle ui_instantiate(const LV2UI_Descriptor* d, const char* pluginURI, const char* bundle, LV2UI_Write_Function write,
                                   LV2UI_Controller controller, LV2UI_Widget* widget, const LV2_Feature* const* features);
static void ui_cleanup(LV2UI_Handle handle);
static void ui_port_event(LV2UI_Handle handle, uint32_t port, uint32_t size, uint32_t format, const void* buffer);
static const void* ui_extension_data(const char* uri);

static void ui_init_descriptors(void) {
    uint32_t count = 0;
    while (vst3go_lv2_uri(count)) count++;
    uiDescriptors = (LV2UI_Descriptor*)calloc(count ? count : 1, sizeof(LV2UI_Descriptor));
    if (!uiDescriptors) return;
    for (uint32_t i = 0; i < count; i++) {
        const char* uri = vst3go_lv2_uri(i);
        size_t n = strlen(uri) + 4;
        char* ui = (char*)malloc(n);
        if (!ui) break;
        memcpy(ui, uri, n - 4);
        memcpy(ui + n - 4, ":ui", 4);
        uiDescriptors[i] = (LV2UI_Descriptor){ ui, ui_instantiate, ui_cleanup, ui_port_event, ui_extension_data };
        uiDescriptorCount = i + 1;
    }
}

static pthread_once_t uiOnce = PTHREAD_ONCE_INIT;

LV2_SYMBOL_EXPORT const LV2UI_Descriptor* lv2ui_descriptor(uint32_t index) {
    pthread_once(&uiOnce, ui_init_descriptors);
    return index < uiDescriptorCount ? &uiDescriptors[index] : NULL;
}

static LV2UI_Handle ui_instantiate(const LV2UI_Descriptor* d, const char* pluginURI, const char* bundle, LV2UI_Write_Function write,
                                   LV2UI_Controller controller, LV2UI_Widget* widget, const LV2_Feature* const* features) {
    (void)d;
    (void)pluginURI;
    (void)bundle;
    LV2Instance* in = NULL;
    void* parent = NULL;
    LV2UI_Resize* resize = NULL;
    LV2UI_Touch* touch = NULL;
    const char* missing = lv2_features_query(features, LV2_INSTANCE_ACCESS_URI, &in, true, LV2_UI__parent, &parent, true,
                                             LV2_UI__resize, &resize, false, LV2_UI__touch, &touch, false, NULL);
    if (missing || !vst3go_lv2_is_instance(in) || vst3go_lv2_ui(in) || !vst3go_lv2_ui_supported(in)) return NULL;
    UI* ui = (UI*)calloc(1, sizeof(UI));
    if (!ui) return NULL;
    *ui = (UI){ in, vst3go_lv2_plugin(in), vst3go_lv2_gui(in), write, controller, resize, touch, false };
    vst3go_lv2_set_ui(in, ui);
    uint32_t width = 0, height = 0;
    vst3goLinuxEditorWindow = 0;
    clap_window_t window = { CLAP_WINDOW_API_X11, { .x11 = (clap_xwnd)(uintptr_t)parent } };
    if (!ui->gui->create(ui->plugin, CLAP_WINDOW_API_X11, false)) goto fail;
    if (!ui->gui->get_size(ui->plugin, &width, &height) || !ui->gui->set_parent(ui->plugin, &window) || !ui->gui->show(ui->plugin)) {
        ui->gui->destroy(ui->plugin);
        goto fail;
    }
    if (resize) resize->ui_resize(resize->handle, (int)width, (int)height);
    *widget = (LV2UI_Widget)(uintptr_t)(vst3goLinuxEditorWindow ? vst3goLinuxEditorWindow : (unsigned long)(uintptr_t)parent);
    return (LV2UI_Handle)ui;
fail:
    vst3go_lv2_set_ui(in, NULL);
    release_timer_set(in);
    free(ui);
    return NULL;
}

static void ui_cleanup(LV2UI_Handle handle) {
    UI* ui = (UI*)handle;
    ui->gui->hide(ui->plugin);
    ui->gui->destroy(ui->plugin);
    vst3go_lv2_set_ui(ui->in, NULL);
    release_timer_set(ui->in);
    free(ui);
}

// Host changes reach the editor through the processor.
static void ui_port_event(LV2UI_Handle handle, uint32_t port, uint32_t size, uint32_t format, const void* buffer) {
    (void)handle;
    (void)port;
    (void)size;
    (void)format;
    (void)buffer;
}

static int ui_idle(LV2UI_Handle handle) {
    UI* ui = (UI*)handle;
    VST3GoLV2UIEdit edit;
    while (vst3go_lv2_ui_pop(ui->in, &edit)) {
        int32_t port = vst3go_lv2_param_port(ui->in, edit.param);
        if (port < 0) continue;
        if (edit.kind == VST3GO_LV2_UI_VALUE) {
            float value = (float)edit.value;
            ui->write(ui->controller, (uint32_t)port, sizeof(float), 0, &value);
        } else if (ui->touch) {
            ui->touch->touch(ui->touch->handle, (uint32_t)port, edit.kind == VST3GO_LV2_UI_BEGIN);
        }
    }
    vst3go_lv2_main_thread(ui->in);
    if (vst3go_lv2_take_rescan(ui->in)) {
        const clap_plugin_params_t* params = vst3go_lv2_params(ui->in);
        for (uint32_t i = 0; i < vst3go_lv2_param_count(ui->in); i++) {
            int32_t port = vst3go_lv2_param_port(ui->in, i);
            const clap_param_info_t* info = vst3go_lv2_param_info(ui->in, i);
            double v;
            if (port < 0 || (info->flags & CLAP_PARAM_IS_READONLY) || !params->get_value(ui->plugin, info->id, &v)) continue;
            float value = (float)v;
            ui->write(ui->controller, (uint32_t)port, sizeof(float), 0, &value);
        }
    }
    TimerSet* set = timer_set(ui->in, false);
    const clap_plugin_timer_support_t* timer = vst3go_lv2_timer(ui->in);
    if (set && timer) {
        int64_t now = now_ms();
        for (size_t i = 0; i < UI_MAX_TIMERS; i++) {
            UITimer* t = &set->timers[i];
            if (!t->id || now < t->due) continue;
            t->due = now + t->period;
            timer->on_timer(ui->plugin, t->id);
        }
    }
    return ui->closed ? 1 : 0;
}

static int ui_resize(LV2UI_Feature_Handle handle, int width, int height) {
    UI* ui = (UI*)handle;
    if (width <= 0 || height <= 0 || !ui->gui->can_resize(ui->plugin)) return 1;
    uint32_t w = (uint32_t)width, h = (uint32_t)height;
    if (!ui->gui->adjust_size(ui->plugin, &w, &h) || !ui->gui->set_size(ui->plugin, w, h)) return 1;
    return 0;
}

static const LV2UI_Idle_Interface idleInterface = { ui_idle };
static const LV2UI_Resize resizeInterface = { NULL, ui_resize };

static const void* ui_extension_data(const char* uri) {
    if (!strcmp(uri, LV2_UI__idleInterface)) return &idleInterface;
    if (!strcmp(uri, LV2_UI__resize)) return &resizeInterface;
    return NULL;
}
