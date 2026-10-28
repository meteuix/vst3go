#ifndef VST3GO_CLAP_BRIDGE_H
#define VST3GO_CLAP_BRIDGE_H

// CLAP front end: the entry point, plugin factory, plugin object and
// extension tables. Every call goes to Go (wrapper_clap.go) with the
// instance ID; host extensions are cached here and called through the
// vst3go_clap_* helpers.

#include "../../include/clap/clap.h"
#include <stdbool.h>
#include <stdint.h>

#define VST3GO_CLAP_SYSEX_SCRATCH 65536

struct VST3GoClapGui;

typedef struct {
    clap_plugin_t plugin;
    const clap_host_t* host;
    const clap_host_params_t* hostParams;
    const clap_host_latency_t* hostLatency;
    const clap_host_audio_ports_t* hostAudioPorts;
    const clap_host_state_t* hostState;
    const clap_host_track_info_t* hostTrackInfo;
    const clap_host_note_name_t* hostNoteName;
    const clap_host_gui_t* hostGui;
    const clap_host_timer_support_t* hostTimer;
    const clap_host_preset_load_t* hostPresetLoad;
    struct VST3GoClapGui* gui; // open editor (clap_gui.c)
    uintptr_t id;
    uint32_t sysexUsed;
    uint8_t sysex[VST3GO_CLAP_SYSEX_SCRATCH];
} VST3GoClapPlugin;

// gui and timer-support extensions (clap_gui.c); NULL without a platform editor.
const void* vst3go_clap_gui_extension(const char* id);
void vst3go_clap_gui_destroy(VST3GoClapPlugin* p);

// Preset discovery factory and preset-load extension (clap_presets.c).
const void* vst3go_clap_preset_discovery_factory(void);
const void* vst3go_clap_preset_load_extension(void);
void vst3go_clap_preset_loaded(VST3GoClapPlugin* p, uint32_t kind, const char* location, const char* loadKey);
void vst3go_clap_preset_error(VST3GoClapPlugin* p, uint32_t kind, const char* location, const char* loadKey, const char* message);
bool vst3go_clap_declare_filetype(clap_preset_discovery_indexer_t* indexer, const char* name, const char* extension);
bool vst3go_clap_declare_location(clap_preset_discovery_indexer_t* indexer, uint32_t flags, const char* name, const char* location);
void vst3go_clap_metadata_error(clap_preset_discovery_metadata_receiver_t* r, const char* message);
bool vst3go_clap_metadata_begin(clap_preset_discovery_metadata_receiver_t* r, const char* name);
void vst3go_clap_metadata_plugin_id(clap_preset_discovery_metadata_receiver_t* r, const char* abi, const char* id);
void vst3go_clap_metadata_flags(clap_preset_discovery_metadata_receiver_t* r, uint32_t flags);
void vst3go_clap_metadata_timestamps(clap_preset_discovery_metadata_receiver_t* r, uint64_t modified);
void vst3go_clap_metadata_feature(clap_preset_discovery_metadata_receiver_t* r, const char* feature);
void vst3go_clap_metadata_extra(clap_preset_discovery_metadata_receiver_t* r, const char* key, const char* value);

// Host extension calls (no-ops when the host lacks the extension).
void vst3go_clap_request_flush(VST3GoClapPlugin* p);
void vst3go_clap_request_restart(VST3GoClapPlugin* p);
void vst3go_clap_request_callback(VST3GoClapPlugin* p);
void vst3go_clap_params_rescan(VST3GoClapPlugin* p, uint32_t flags);
void vst3go_clap_latency_changed(VST3GoClapPlugin* p);
void vst3go_clap_audio_ports_rescan(VST3GoClapPlugin* p, uint32_t flags);
void vst3go_clap_state_mark_dirty(VST3GoClapPlugin* p);
void vst3go_clap_note_names_changed(VST3GoClapPlugin* p);
bool vst3go_clap_track_info(VST3GoClapPlugin* p, clap_track_info_t* info);

// Streams and event lists.
int64_t vst3go_clap_ostream_write(const clap_ostream_t* stream, const void* buffer, uint64_t size);
int64_t vst3go_clap_istream_read(const clap_istream_t* stream, void* buffer, uint64_t size);
uint32_t vst3go_clap_events_size(const clap_input_events_t* events);
const clap_event_header_t* vst3go_clap_events_get(const clap_input_events_t* events, uint32_t index);

bool vst3go_clap_push_note(const clap_output_events_t* out, uint16_t type, uint32_t time, int16_t port, int16_t channel, int16_t key, int32_t noteId, double velocity);
bool vst3go_clap_push_expression(const clap_output_events_t* out, uint32_t time, int32_t expression, int16_t port, int16_t channel, int16_t key, int32_t noteId, double value);
bool vst3go_clap_push_midi(const clap_output_events_t* out, uint32_t time, uint16_t port, uint8_t b0, uint8_t b1, uint8_t b2);
bool vst3go_clap_push_sysex(VST3GoClapPlugin* p, const clap_output_events_t* out, uint32_t time, uint16_t port, const uint8_t* data, uint32_t size);
bool vst3go_clap_push_param(const clap_output_events_t* out, uint32_t time, clap_id id, double value);
bool vst3go_clap_push_gesture(const clap_output_events_t* out, uint16_t type, uint32_t time, clap_id id);

#endif
