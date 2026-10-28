// Shared between the LV2 plugin (lv2_linux.c) and its X11 UI (lv2ui_linux.c).
#ifndef VST3GO_LV2_LINUX_H
#define VST3GO_LV2_LINUX_H

#include <stdbool.h>
#include <stdint.h>

#include "clap_bridge.h"

typedef struct LV2Instance LV2Instance;

// Edits the plugin's editor made, for the UI to forward to the host.
enum { VST3GO_LV2_UI_EDITS = 1024 };
enum { VST3GO_LV2_UI_VALUE, VST3GO_LV2_UI_BEGIN, VST3GO_LV2_UI_END };
typedef struct {
    uint32_t kind;
    uint32_t param;  // parameter index
    double value;
} VST3GoLV2UIEdit;

// lv2_linux.c
bool vst3go_lv2_is_instance(const void* handle);
void vst3go_lv2_main_thread(LV2Instance* in);
const clap_plugin_t* vst3go_lv2_plugin(LV2Instance* in);
const clap_plugin_gui_t* vst3go_lv2_gui(LV2Instance* in);
const clap_plugin_timer_support_t* vst3go_lv2_timer(LV2Instance* in);
void* vst3go_lv2_ui(LV2Instance* in);
void vst3go_lv2_set_ui(LV2Instance* in, void* ui);
bool vst3go_lv2_ui_pop(LV2Instance* in, VST3GoLV2UIEdit* edit);
int32_t vst3go_lv2_param_port(LV2Instance* in, uint32_t param);
const clap_plugin_params_t* vst3go_lv2_params(LV2Instance* in);
const clap_param_info_t* vst3go_lv2_param_info(LV2Instance* in, uint32_t param);
uint32_t vst3go_lv2_param_count(LV2Instance* in);
bool vst3go_lv2_take_rescan(LV2Instance* in);  // the plugin changed its values
const char* vst3go_lv2_uri(uint32_t index);

// lv2ui_linux.c
bool vst3go_lv2_ui_supported(LV2Instance* in);
const void* vst3go_lv2_ui_host_extension(LV2Instance* in, const char* id);

// The X11 window the Linux editor reparented into the host window last, on
// this thread (set by editor_view_linux.c; 0 without the editor).
extern _Thread_local unsigned long vst3goLinuxEditorWindow;

#endif
