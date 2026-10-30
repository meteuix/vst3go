//go:build standalone

// The audio device of the application, through miniaudio (miniaudio.h,
// public domain or MIT-0). Its callback runs on the device's thread in C:
// it takes the MIDI input from a ring the MIDI ports fill, renders the
// block with vst3go_sa_process (host.h) and puts the plugin's MIDI output
// in a ring the application drains, so the device thread never enters Go.
#pragma once

#include <stdbool.h>
#include <stdint.h>

typedef struct VST3GoSAAudio VST3GoSAAudio;

// vst3go_sa_audio_backend_known reports whether backend names an audio
// API: coreaudio, wasapi, dsound, winmm, alsa, pulseaudio, jack, oss,
// sndio or null, in any case.
bool vst3go_sa_audio_backend_known(const char* backend);
// vst3go_sa_audio_backend_name names the index-th audio API built for this
// platform, NULL past the last.
const char* vst3go_sa_audio_backend_name(uint32_t index);

// vst3go_sa_audio_new opens a known audio API by name (NULL or "" for the
// platform's first that works) and lists its devices. On failure it
// returns NULL and sets *error.
VST3GoSAAudio* vst3go_sa_audio_new(const char* backend, const char** error);

// vst3go_sa_audio_free closes the device (stopping it) and the API.
void vst3go_sa_audio_free(VST3GoSAAudio* a);

// The output (capture false) or input devices of the API.
uint32_t vst3go_sa_audio_device_count(VST3GoSAAudio* a, bool capture);
const char* vst3go_sa_audio_device_name(VST3GoSAAudio* a, bool capture, uint32_t index);
bool vst3go_sa_audio_device_default(VST3GoSAAudio* a, bool capture, uint32_t index);

// vst3go_sa_audio_open opens the output device of index output and, with
// in_channels > 0, the input device of index input (-1: the default ones)
// for interleaved float frames, periods of frames frames at sample_rate
// (0: the device's). It returns NULL or an error message.
const char* vst3go_sa_audio_open(VST3GoSAAudio* a, int32_t output, int32_t input, uint32_t sample_rate, uint32_t frames,
                                 uint32_t in_channels, uint32_t out_channels);
uint32_t vst3go_sa_audio_sample_rate(VST3GoSAAudio* a);

// vst3go_sa_audio_render renders one period of interleaved float frames,
// as the device's callback does: the queued MIDI input, then the block,
// then the plugin's MIDI output into its ring.
void vst3go_sa_audio_render(const float* in, uint32_t in_channels, float* out, uint32_t out_channels, uint32_t frames);

// vst3go_sa_audio_start returns NULL or an error message.
const char* vst3go_sa_audio_start(VST3GoSAAudio* a);
bool vst3go_sa_audio_running(VST3GoSAAudio* a);
void vst3go_sa_audio_stop(VST3GoSAAudio* a);

// vst3go_sa_midi_in_push queues a 3-byte message for the next block; one
// thread at a time. It returns false when the ring is full.
bool vst3go_sa_midi_in_push(const uint8_t* msg);

// vst3go_sa_midi_out_pop takes a 3-byte message the plugin output; one
// thread at a time.
bool vst3go_sa_midi_out_pop(uint8_t* msg);
