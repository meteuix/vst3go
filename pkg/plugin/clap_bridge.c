#include "clap_bridge.h"
#include "clap_rt.h"
#include "rtguard.h"
#include "ara.h"
#include "../../include/ara/ARACLAP.h"
#include <stdlib.h>
#include <string.h>

// Go side (wrapper_clap.go).
extern uint32_t GoClapPluginCount(void);
extern clap_plugin_descriptor_t* GoClapDescriptor(uint32_t index);
extern uintptr_t GoClapCreate(uint32_t index, VST3GoClapPlugin* plugin);
extern bool GoClapInit(uintptr_t id);
extern void GoClapDestroy(uintptr_t id);
extern bool GoClapActivate(uintptr_t id, double sampleRate, uint32_t minFrames, uint32_t maxFrames);
extern void GoClapDeactivate(uintptr_t id);
extern void GoClapReset(uintptr_t id);
extern clap_process_status GoClapProcess(uintptr_t id, clap_process_t* process);
extern void GoClapOnMainThread(uintptr_t id);
extern uint32_t GoClapParamsCount(uintptr_t id);
extern bool GoClapParamsInfo(uintptr_t id, uint32_t index, clap_param_info_t* info);
extern bool GoClapParamsValue(uintptr_t id, clap_id param, double* value);
extern bool GoClapParamsValueToText(uintptr_t id, clap_id param, double value, char* out, uint32_t size);
extern bool GoClapParamsTextToValue(uintptr_t id, clap_id param, char* text, double* value);
extern void GoClapParamsFlush(uintptr_t id, clap_input_events_t* in, clap_output_events_t* out);
extern void GoClapRealtimeConfig(uintptr_t id, double* share, uint32_t* channels);
extern bool GoClapStateSave(uintptr_t id, clap_ostream_t* stream);
extern bool GoClapStateLoad(uintptr_t id, clap_istream_t* stream);
extern uint32_t GoClapAudioPortsCount(uintptr_t id, bool isInput);
extern bool GoClapAudioPortsGet(uintptr_t id, uint32_t index, bool isInput, clap_audio_port_info_t* info);
extern uint32_t GoClapNotePortsCount(uintptr_t id, bool isInput);
extern bool GoClapNotePortsGet(uintptr_t id, uint32_t index, bool isInput, clap_note_port_info_t* info);
extern uint32_t GoClapLatency(uintptr_t id);
extern uint32_t GoClapTail(uintptr_t id);
extern void GoClapTrackInfoChanged(uintptr_t id);
extern ARAFactory* GoClapARAFactory(uintptr_t id);
extern ARAPlugInExtensionInstance* GoClapARABind(uintptr_t id, void* controller, int32_t knownRoles, int32_t assignedRoles);
extern uint32_t GoARAFactoryCount(void);
extern int32_t GoARAPluginIndex(int32_t slot);
extern uint32_t GoClapNoteNameCount(uintptr_t id);
extern bool GoClapNoteNameGet(uintptr_t id, uint32_t index, clap_note_name_t* name);
extern bool GoClapConfigurePorts(uintptr_t id, clap_audio_port_configuration_request_t* requests, uint32_t count, bool apply);
extern bool GoClapSurroundMaskSupported(uintptr_t id, uint64_t mask);
extern uint32_t GoClapSurroundChannelMap(uintptr_t id, bool isInput, uint32_t index, uint8_t* map, uint32_t capacity);

static uintptr_t idOf(const clap_plugin_t* plugin) {
    return ((VST3GoClapPlugin*)plugin->plugin_data)->id;
}

// Extensions

static uint32_t params_count(const clap_plugin_t* plugin) { return GoClapParamsCount(idOf(plugin)); }
static bool params_get_info(const clap_plugin_t* plugin, uint32_t index, clap_param_info_t* info) {
    return info && GoClapParamsInfo(idOf(plugin), index, info);
}
static bool params_get_value(const clap_plugin_t* plugin, clap_id param, double* value) {
    return value && GoClapParamsValue(idOf(plugin), param, value);
}
static bool params_value_to_text(const clap_plugin_t* plugin, clap_id param, double value, char* out, uint32_t size) {
    return out && size > 0 && GoClapParamsValueToText(idOf(plugin), param, value, out, size);
}
static bool params_text_to_value(const clap_plugin_t* plugin, clap_id param, const char* text, double* value) {
    return text && value && GoClapParamsTextToValue(idOf(plugin), param, (char*)text, value);
}
// params_flush comes on the audio thread while active (through the
// deadline guard) and on the main thread otherwise.
static void params_flush(const clap_plugin_t* plugin, const clap_input_events_t* in, const clap_output_events_t* out) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    VST3GoClapRT* rt = __atomic_load_n(&p->rt, __ATOMIC_ACQUIRE);
    if (rt) {
        vst3go_clap_rt_flush(rt, in, out);
        return;
    }
    GoClapParamsFlush(p->id, (clap_input_events_t*)in, (clap_output_events_t*)out);
}
static const clap_plugin_params_t extParams = {
    params_count, params_get_info, params_get_value, params_value_to_text, params_text_to_value, params_flush
};

static bool state_save(const clap_plugin_t* plugin, const clap_ostream_t* stream) {
    return stream && GoClapStateSave(idOf(plugin), (clap_ostream_t*)stream);
}
static bool state_load(const clap_plugin_t* plugin, const clap_istream_t* stream) {
    return stream && GoClapStateLoad(idOf(plugin), (clap_istream_t*)stream);
}
static const clap_plugin_state_t extState = { state_save, state_load };

static uint32_t audio_ports_count(const clap_plugin_t* plugin, bool isInput) { return GoClapAudioPortsCount(idOf(plugin), isInput); }
static bool audio_ports_get(const clap_plugin_t* plugin, uint32_t index, bool isInput, clap_audio_port_info_t* info) {
    return info && GoClapAudioPortsGet(idOf(plugin), index, isInput, info);
}
static const clap_plugin_audio_ports_t extAudioPorts = { audio_ports_count, audio_ports_get };

static bool ports_can_apply(const clap_plugin_t* plugin, const clap_audio_port_configuration_request_t* requests, uint32_t count) {
    return GoClapConfigurePorts(idOf(plugin), (clap_audio_port_configuration_request_t*)requests, count, false);
}
static bool ports_apply(const clap_plugin_t* plugin, const clap_audio_port_configuration_request_t* requests, uint32_t count) {
    return GoClapConfigurePorts(idOf(plugin), (clap_audio_port_configuration_request_t*)requests, count, true);
}
static const clap_plugin_configurable_audio_ports_t extConfigurablePorts = { ports_can_apply, ports_apply };

static bool surround_mask_supported(const clap_plugin_t* plugin, uint64_t mask) { return GoClapSurroundMaskSupported(idOf(plugin), mask); }
static uint32_t surround_channel_map(const clap_plugin_t* plugin, bool isInput, uint32_t index, uint8_t* map, uint32_t capacity) {
    return map || !capacity ? GoClapSurroundChannelMap(idOf(plugin), isInput, index, map, capacity) : 0;
}
static const clap_plugin_surround_t extSurround = { surround_mask_supported, surround_channel_map };

static uint32_t note_ports_count(const clap_plugin_t* plugin, bool isInput) { return GoClapNotePortsCount(idOf(plugin), isInput); }
static bool note_ports_get(const clap_plugin_t* plugin, uint32_t index, bool isInput, clap_note_port_info_t* info) {
    return info && GoClapNotePortsGet(idOf(plugin), index, isInput, info);
}
static const clap_plugin_note_ports_t extNotePorts = { note_ports_count, note_ports_get };

static uint32_t latency_get(const clap_plugin_t* plugin) { return GoClapLatency(idOf(plugin)); }
static const clap_plugin_latency_t extLatency = { latency_get };

static uint32_t tail_get(const clap_plugin_t* plugin) { return GoClapTail(idOf(plugin)); }
static const clap_plugin_tail_t extTail = { tail_get };

static bool render_has_hard_realtime_requirement(const clap_plugin_t* plugin) { (void)plugin; return false; }
// render_set does not enter Go: LV2 switches the mode from run when the
// host starts or stops freewheeling.
static bool render_set(const clap_plugin_t* plugin, clap_plugin_render_mode mode) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    if (mode != CLAP_RENDER_REALTIME && mode != CLAP_RENDER_OFFLINE) return false;
    __atomic_store_n(&p->offline, mode == CLAP_RENDER_OFFLINE, __ATOMIC_RELEASE);
    return true;
}

double vst3go_clap_deadline(const clap_plugin_t* plugin) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    double share = 0;
    uint32_t channels = 0;
    GoClapRealtimeConfig(p->id, &share, &channels);
    return share;
}

bool vst3go_clap_offline(VST3GoClapPlugin* p) { return __atomic_load_n(&p->offline, __ATOMIC_ACQUIRE); }
static const clap_plugin_render_t extRender = { render_has_hard_realtime_requirement, render_set };

static void track_info_changed(const clap_plugin_t* plugin) { GoClapTrackInfoChanged(idOf(plugin)); }
static const clap_plugin_track_info_t extTrackInfo = { track_info_changed };

static uint32_t note_name_count(const clap_plugin_t* plugin) { return GoClapNoteNameCount(idOf(plugin)); }
static bool note_name_get(const clap_plugin_t* plugin, uint32_t index, clap_note_name_t* name) {
    return name && GoClapNoteNameGet(idOf(plugin), index, name);
}
static const clap_plugin_note_name_t extNoteName = { note_name_count, note_name_get };

// Plugin

static bool plugin_init(const clap_plugin_t* plugin) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    const clap_host_t* host = p->host;
    if (host && host->get_extension) {
        p->hostParams = (const clap_host_params_t*)host->get_extension(host, CLAP_EXT_PARAMS);
        p->hostLatency = (const clap_host_latency_t*)host->get_extension(host, CLAP_EXT_LATENCY);
        p->hostAudioPorts = (const clap_host_audio_ports_t*)host->get_extension(host, CLAP_EXT_AUDIO_PORTS);
        p->hostState = (const clap_host_state_t*)host->get_extension(host, CLAP_EXT_STATE);
        p->hostTrackInfo = (const clap_host_track_info_t*)host->get_extension(host, CLAP_EXT_TRACK_INFO);
        p->hostNoteName = (const clap_host_note_name_t*)host->get_extension(host, CLAP_EXT_NOTE_NAME);
        p->hostGui = (const clap_host_gui_t*)host->get_extension(host, CLAP_EXT_GUI);
        p->hostTimer = (const clap_host_timer_support_t*)host->get_extension(host, CLAP_EXT_TIMER_SUPPORT);
        p->hostPresetLoad = (const clap_host_preset_load_t*)host->get_extension(host, CLAP_EXT_PRESET_LOAD);
        if (!p->hostPresetLoad) {
            p->hostPresetLoad = (const clap_host_preset_load_t*)host->get_extension(host, CLAP_EXT_PRESET_LOAD_COMPAT);
        }
    }
    return GoClapInit(p->id);
}

static void plugin_destroy(const clap_plugin_t* plugin) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    vst3go_clap_rt_free(p->rt);
    p->rt = NULL;
    vst3go_clap_gui_destroy(p);
    GoClapDestroy(p->id);
    free(p);
}

// activate starts the deadline guard (DeadlineProcessor); deactivate stops
// it after its last block. The guard runs offline blocks on the host's
// thread, so it does not depend on the render mode.
static bool plugin_activate(const clap_plugin_t* plugin, double sampleRate, uint32_t minFrames, uint32_t maxFrames) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    if (!GoClapActivate(p->id, sampleRate, minFrames, maxFrames)) return false;
    double share = 0;
    uint32_t channels = 0;
    GoClapRealtimeConfig(p->id, &share, &channels);
    if (share > 0) {
        __atomic_store_n(&p->rt, vst3go_clap_rt_new(p, sampleRate, maxFrames, channels, share), __ATOMIC_RELEASE);
    }
    return true;
}
static void plugin_deactivate(const clap_plugin_t* plugin) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    vst3go_clap_rt_free(__atomic_exchange_n(&p->rt, NULL, __ATOMIC_ACQ_REL));
    GoClapDeactivate(p->id);
}

// start_processing, stop_processing and reset come on the audio thread.
// Starting and stopping need nothing from the processor; a reset under the
// deadline guard runs at the start of the next job.
static bool plugin_start_processing(const clap_plugin_t* plugin) {
    (void)plugin;
    return true;
}
static void plugin_stop_processing(const clap_plugin_t* plugin) { (void)plugin; }
static void plugin_reset(const clap_plugin_t* plugin) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    if (p->rt) {
        vst3go_clap_rt_reset(p->rt);
        return;
    }
    GoClapReset(p->id);
}

static clap_process_status plugin_process(const clap_plugin_t* plugin, const clap_process_t* process) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    if (p->rt) {
        return vst3go_clap_rt_process(p->rt, process);
    }
    p->sysexUsed = 0;
    rtguard_fp fp = rtguard_flush_denormals();
    clap_process_status status = GoClapProcess(p->id, (clap_process_t*)process);
    rtguard_restore_fp(fp);
    return status;
}

// ARA plug-in extension (ARACLAP.h)

static const ARAFactory* ara_get_factory(const clap_plugin_t* plugin) { return GoClapARAFactory(idOf(plugin)); }

static const ARAPlugInExtensionInstance* ara_bind(const clap_plugin_t* plugin, ARADocumentControllerRef controller,
                                                  ARAPlugInInstanceRoleFlags known, ARAPlugInInstanceRoleFlags assigned) {
    return GoClapARABind(idOf(plugin), controller, known, assigned);
}

static const clap_ara_plugin_extension_t extARA = { ara_get_factory, ara_bind };

static const void* plugin_get_extension(const clap_plugin_t* plugin, const char* id) {
    if (!id) return NULL;
    if (!strcmp(id, CLAP_EXT_ARA_PLUGINEXTENSION) || !strcmp(id, CLAP_EXT_ARA_PLUGINEXTENSION_COMPAT)) {
        return GoClapARAFactory(idOf(plugin)) ? &extARA : NULL;
    }
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &extParams;
    if (!strcmp(id, CLAP_EXT_STATE)) return &extState;
    if (!strcmp(id, CLAP_EXT_AUDIO_PORTS)) return &extAudioPorts;
    if (!strcmp(id, CLAP_EXT_CONFIGURABLE_AUDIO_PORTS) || !strcmp(id, CLAP_EXT_CONFIGURABLE_AUDIO_PORTS_COMPAT)) return &extConfigurablePorts;
    if (!strcmp(id, CLAP_EXT_SURROUND) || !strcmp(id, CLAP_EXT_SURROUND_COMPAT)) return &extSurround;
    if (!strcmp(id, CLAP_EXT_NOTE_PORTS)) return &extNotePorts;
    if (!strcmp(id, CLAP_EXT_LATENCY)) return &extLatency;
    if (!strcmp(id, CLAP_EXT_TAIL)) return &extTail;
    if (!strcmp(id, CLAP_EXT_RENDER)) return &extRender;
    if (!strcmp(id, CLAP_EXT_TRACK_INFO)) return &extTrackInfo;
    if (!strcmp(id, CLAP_EXT_NOTE_NAME)) return &extNoteName;
    if (!strcmp(id, CLAP_EXT_PRESET_LOAD) || !strcmp(id, CLAP_EXT_PRESET_LOAD_COMPAT)) return vst3go_clap_preset_load_extension();
    return vst3go_clap_gui_extension(id);
}

static void plugin_on_main_thread(const clap_plugin_t* plugin) { GoClapOnMainThread(idOf(plugin)); }

// Factory

static uint32_t factory_get_plugin_count(const clap_plugin_factory_t* factory) {
    (void)factory;
    return GoClapPluginCount();
}

static const clap_plugin_descriptor_t* factory_get_plugin_descriptor(const clap_plugin_factory_t* factory, uint32_t index) {
    (void)factory;
    return index < GoClapPluginCount() ? GoClapDescriptor(index) : NULL;
}

static const clap_plugin_t* factory_create_plugin(const clap_plugin_factory_t* factory, const clap_host_t* host, const char* pluginID) {
    (void)factory;
    if (!host || !pluginID || !clap_version_is_compatible(host->clap_version)) {
        return NULL;
    }
    uint32_t count = GoClapPluginCount();
    for (uint32_t i = 0; i < count; i++) {
        const clap_plugin_descriptor_t* desc = GoClapDescriptor(i);
        if (!desc || strcmp(desc->id, pluginID) != 0) {
            continue;
        }
        VST3GoClapPlugin* p = (VST3GoClapPlugin*)calloc(1, sizeof(VST3GoClapPlugin));
        if (!p) {
            return NULL;
        }
        p->host = host;
        p->plugin.desc = desc;
        p->plugin.plugin_data = p;
        p->plugin.init = plugin_init;
        p->plugin.destroy = plugin_destroy;
        p->plugin.activate = plugin_activate;
        p->plugin.deactivate = plugin_deactivate;
        p->plugin.start_processing = plugin_start_processing;
        p->plugin.stop_processing = plugin_stop_processing;
        p->plugin.reset = plugin_reset;
        p->plugin.process = plugin_process;
        p->plugin.get_extension = plugin_get_extension;
        p->plugin.on_main_thread = plugin_on_main_thread;
        p->id = GoClapCreate(i, p);
        if (!p->id) {
            free(p);
            return NULL;
        }
        return &p->plugin;
    }
    return NULL;
}

static const clap_plugin_factory_t pluginFactory = {
    factory_get_plugin_count, factory_get_plugin_descriptor, factory_create_plugin
};

// ARA factory (ARACLAP.h): the ARA plugins of the binary.

static uint32_t ara_factory_count(const clap_ara_factory_t* factory) {
    (void)factory;
    return GoARAFactoryCount();
}

static const ARAFactory* ara_factory_get(const clap_ara_factory_t* factory, uint32_t index) {
    (void)factory;
    return index < GoARAFactoryCount() ? vst3go_ara_factory((int32_t)index) : NULL;
}

static const char* ara_factory_plugin_id(const clap_ara_factory_t* factory, uint32_t index) {
    (void)factory;
    if (index >= GoARAFactoryCount()) return NULL;
    const clap_plugin_descriptor_t* desc = GoClapDescriptor((uint32_t)GoARAPluginIndex((int32_t)index));
    return desc ? desc->id : NULL;
}

static const clap_ara_factory_t araFactory = { ara_factory_count, ara_factory_get, ara_factory_plugin_id };

// Entry

static bool entry_init(const char* path) {
    (void)path;
    return true;
}

static void entry_deinit(void) {}

static const void* entry_get_factory(const char* factoryID) {
    if (!factoryID) return NULL;
    if (!strcmp(factoryID, CLAP_PLUGIN_FACTORY_ID)) return &pluginFactory;
    if (!strcmp(factoryID, CLAP_EXT_ARA_FACTORY) || !strcmp(factoryID, CLAP_EXT_ARA_FACTORY_COMPAT)) {
        return GoARAFactoryCount() ? &araFactory : NULL;
    }
    if (!strcmp(factoryID, CLAP_PRESET_DISCOVERY_FACTORY_ID) || !strcmp(factoryID, CLAP_PRESET_DISCOVERY_FACTORY_ID_COMPAT)) {
        return vst3go_clap_preset_discovery_factory();
    }
    return NULL;
}

CLAP_EXPORT const clap_plugin_entry_t clap_entry = {
    CLAP_VERSION_INIT, entry_init, entry_deinit, entry_get_factory
};

// Host helpers

void vst3go_clap_request_flush(VST3GoClapPlugin* p) {
    if (p->hostParams) p->hostParams->request_flush(p->host);
}
void vst3go_clap_request_restart(VST3GoClapPlugin* p) { p->host->request_restart(p->host); }
void vst3go_clap_request_callback(VST3GoClapPlugin* p) { p->host->request_callback(p->host); }
void vst3go_clap_params_rescan(VST3GoClapPlugin* p, uint32_t flags) {
    if (p->hostParams) p->hostParams->rescan(p->host, flags);
}
void vst3go_clap_latency_changed(VST3GoClapPlugin* p) {
    if (p->hostLatency) p->hostLatency->changed(p->host);
}
void vst3go_clap_audio_ports_rescan(VST3GoClapPlugin* p, uint32_t flags) {
    if (p->hostAudioPorts && p->hostAudioPorts->is_rescan_flag_supported(p->host, flags)) {
        p->hostAudioPorts->rescan(p->host, flags);
    } else {
        p->host->request_restart(p->host);
    }
}
void vst3go_clap_state_mark_dirty(VST3GoClapPlugin* p) {
    if (p->hostState) p->hostState->mark_dirty(p->host);
}
void vst3go_clap_note_names_changed(VST3GoClapPlugin* p) {
    if (p->hostNoteName) p->hostNoteName->changed(p->host);
}
bool vst3go_clap_track_info(VST3GoClapPlugin* p, clap_track_info_t* info) {
    return p->hostTrackInfo && p->hostTrackInfo->get(p->host, info);
}

int64_t vst3go_clap_ostream_write(const clap_ostream_t* stream, const void* buffer, uint64_t size) {
    return stream->write(stream, buffer, size);
}
int64_t vst3go_clap_istream_read(const clap_istream_t* stream, void* buffer, uint64_t size) {
    return stream->read(stream, buffer, size);
}
uint32_t vst3go_clap_events_size(const clap_input_events_t* events) { return events ? events->size(events) : 0; }
const clap_event_header_t* vst3go_clap_events_get(const clap_input_events_t* events, uint32_t index) {
    return events->get(events, index);
}

bool vst3go_clap_push_note(const clap_output_events_t* out, uint16_t type, uint32_t time, int16_t port, int16_t channel, int16_t key, int32_t noteId, double velocity) {
    clap_event_note_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, type, 0 }, noteId, port, channel, key, velocity };
    return out->try_push(out, &ev.header);
}
bool vst3go_clap_push_expression(const clap_output_events_t* out, uint32_t time, int32_t expression, int16_t port, int16_t channel, int16_t key, int32_t noteId, double value) {
    clap_event_note_expression_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_NOTE_EXPRESSION, 0 }, expression, noteId, port, channel, key, value };
    return out->try_push(out, &ev.header);
}
bool vst3go_clap_push_midi(const clap_output_events_t* out, uint32_t time, uint16_t port, uint8_t b0, uint8_t b1, uint8_t b2) {
    clap_event_midi_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0 }, port, { b0, b1, b2 } };
    return out->try_push(out, &ev.header);
}
bool vst3go_clap_push_sysex(VST3GoClapPlugin* p, const clap_output_events_t* out, uint32_t time, uint16_t port, const uint8_t* data, uint32_t size) {
    // The host reads the payload after process returns: keep it in the
    // instance scratch memory until the next block.
    if (size == 0 || size > VST3GO_CLAP_SYSEX_SCRATCH - p->sysexUsed) return false;
    uint8_t* buffer = p->sysex + p->sysexUsed;
    memcpy(buffer, data, size);
    p->sysexUsed += size;
    clap_event_midi_sysex_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI_SYSEX, 0 }, port, buffer, size };
    return out->try_push(out, &ev.header);
}
bool vst3go_clap_push_param(const clap_output_events_t* out, uint32_t time, clap_id id, double value) {
    clap_event_param_value_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 }, id, NULL, -1, -1, -1, -1, value };
    return out->try_push(out, &ev.header);
}
bool vst3go_clap_push_gesture(const clap_output_events_t* out, uint16_t type, uint32_t time, clap_id id) {
    clap_event_param_gesture_t ev = { { sizeof(ev), time, CLAP_CORE_EVENT_SPACE_ID, type, 0 }, id };
    return out->try_push(out, &ev.header);
}
