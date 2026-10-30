//go:build standalone

// The standalone application hosts its plugin through the plugin's own CLAP
// entry (clap_entry in pkg/plugin), so it runs the same thread-safe runtime
// a CLAP host does. One plugin instance per process.
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clap/clap.h"

uint32_t vst3go_sa_plugin_count(void);
const clap_plugin_descriptor_t* vst3go_sa_plugin_descriptor(uint32_t index);

// vst3go_sa_create creates and initializes the plugin of a descriptor index
// on the main thread. It returns NULL or an error message.
const char* vst3go_sa_create(uint32_t index);
void vst3go_sa_destroy(void);

// Channels of the main audio input or output port (0 without one), and
// whether the plugin takes note input.
uint32_t vst3go_sa_main_channels(bool input);
bool vst3go_sa_note_input(void);
uint32_t vst3go_sa_latency(void);

bool vst3go_sa_activate(double sample_rate, uint32_t max_frames);
void vst3go_sa_deactivate(void);

// Stop handshake: the audio thread calls stop_processing on its next
// callback after vst3go_sa_request_stop, then reports vst3go_sa_stopped.
void vst3go_sa_request_stop(void);
bool vst3go_sa_stopped(void);
// vst3go_sa_force_stop calls stop_processing from the main thread when the
// device delivered no callback after the request.
void vst3go_sa_force_stop(void);

// vst3go_sa_process renders frames of interleaved float audio on the audio
// thread. midi holds midi_count 3-byte messages for the start of the block;
// MIDI the plugin outputs is written to midi_out (3 bytes per message, up to
// midi_out_capacity messages) and the count returned.
uint32_t vst3go_sa_process(const float* in, uint32_t in_channels, float* out, uint32_t out_channels, uint32_t frames,
                           const uint8_t* midi, uint32_t midi_count, uint8_t* midi_out, uint32_t midi_out_capacity);

// Transport of the application, played to the plugin (clap_process
// transport) from the next block on. The position advances while playing;
// vst3go_sa_transport_position reports it in quarter notes.
enum { VST3GO_SA_MIN_TEMPO = 20, VST3GO_SA_MAX_TEMPO = 999, VST3GO_SA_DEFAULT_TEMPO = 120 };
void vst3go_sa_transport_set(bool playing, double tempo, uint32_t numerator, uint32_t denominator);
void vst3go_sa_transport_rewind(void);
double vst3go_sa_transport_position(void);

// State as one malloc'd buffer (free it), and loading it back.
bool vst3go_sa_state_save(uint8_t** data, size_t* size);
bool vst3go_sa_state_load(const uint8_t* data, size_t size);

// vst3go_sa_service runs the plugin's main-thread requests (on_main_thread,
// editor resizes) and returns the VST3GO_SA_* flags the application handles.
enum {
    VST3GO_SA_RESTART = 1 << 0, // the plugin asked to be reactivated
};
uint32_t vst3go_sa_service(void);

// Editor through the CLAP gui extension on the main thread.
bool vst3go_sa_gui_create(const char* api, double scale, uint32_t* width, uint32_t* height, bool* resizable);
bool vst3go_sa_gui_attach(const clap_window_t* window);
// vst3go_sa_gui_adjust turns a size the user drags the window to into the
// nearest size the editor accepts; vst3go_sa_gui_set_size applies the new
// size of the window.
void vst3go_sa_gui_adjust(uint32_t* width, uint32_t* height);
void vst3go_sa_gui_set_size(uint32_t width, uint32_t height);
void vst3go_sa_gui_destroy(void);

// Timers of the CLAP timer-support extension, which the platform's run loop
// fires (vst3go_sa_timer_fire); only X11 editors need them.
void vst3go_sa_timer_fire(clap_id id);
