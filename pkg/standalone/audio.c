//go:build standalone

#include "audio.h"
#include "host.h"

#include <stdatomic.h>
#include <string.h>

// The device layer only: no decoders, encoders, resource manager or engine.
#define MA_NO_DECODING
#define MA_NO_ENCODING
#define MA_NO_WAV
#define MA_NO_FLAC
#define MA_NO_MP3
#define MA_NO_RESOURCE_MANAGER
#define MA_NO_NODE_GRAPH
#define MA_NO_ENGINE
#define MA_NO_GENERATION
#define MINIAUDIO_IMPLEMENTATION
#include "miniaudio.h"

// MIDI messages per block, each way.
#define SA_MIDI_BLOCK 512
// Ring capacity in messages (a power of two).
#define SA_MIDI_RING 1024

// Ring is a single-producer, single-consumer queue of 3-byte messages.
typedef struct {
    atomic_uint head, tail; // tail: next write; head: next read
    uint8_t data[SA_MIDI_RING][3];
} Ring;

static bool ring_push(Ring* r, const uint8_t* msg) {
    unsigned tail = atomic_load_explicit(&r->tail, memory_order_relaxed);
    if (tail - atomic_load_explicit(&r->head, memory_order_acquire) == SA_MIDI_RING) return false;
    memcpy(r->data[tail % SA_MIDI_RING], msg, 3);
    atomic_store_explicit(&r->tail, tail + 1, memory_order_release);
    return true;
}

static bool ring_pop(Ring* r, uint8_t* msg) {
    unsigned head = atomic_load_explicit(&r->head, memory_order_relaxed);
    if (head == atomic_load_explicit(&r->tail, memory_order_acquire)) return false;
    memcpy(msg, r->data[head % SA_MIDI_RING], 3);
    atomic_store_explicit(&r->head, head + 1, memory_order_release);
    return true;
}

// One plugin per process, so one pair of rings.
static Ring midiIn, midiOut;

bool vst3go_sa_midi_in_push(const uint8_t* msg) { return ring_push(&midiIn, msg); }
bool vst3go_sa_midi_out_pop(uint8_t* msg) { return ring_pop(&midiOut, msg); }

struct VST3GoSAAudio {
    ma_context context;
    ma_device device;
    bool deviceOpen;
    ma_device_info* outputs;
    ma_device_info* inputs;
    ma_uint32 numOutputs, numInputs;
    uint32_t inChannels, outChannels;
};

static const struct {
    const char* name;
    ma_backend backend;
} backends[] = {
    { "coreaudio", ma_backend_coreaudio }, { "wasapi", ma_backend_wasapi }, { "dsound", ma_backend_dsound },
    { "winmm", ma_backend_winmm },         { "alsa", ma_backend_alsa },     { "pulseaudio", ma_backend_pulseaudio },
    { "jack", ma_backend_jack },           { "oss", ma_backend_oss },       { "sndio", ma_backend_sndio },
    { "null", ma_backend_null },
};

static bool name_equal(const char* a, const char* b) {
    for (; *a && *b; a++, b++) {
        char x = *a >= 'A' && *a <= 'Z' ? (char)(*a + 32) : *a;
        if (x != *b) return false;
    }
    return *a == *b;
}

// find_backend returns the index of a backend name in backends, or -1.
static int find_backend(const char* backend) {
    for (size_t i = 0; i < sizeof(backends) / sizeof(backends[0]); i++) {
        if (name_equal(backend, backends[i].name)) return (int)i;
    }
    return -1;
}

bool vst3go_sa_audio_backend_known(const char* backend) { return find_backend(backend) >= 0; }

const char* vst3go_sa_audio_backend_name(uint32_t index) {
    for (size_t i = 0; i < sizeof(backends) / sizeof(backends[0]); i++) {
        if (ma_is_backend_enabled(backends[i].backend) && index-- == 0) return backends[i].name;
    }
    return NULL;
}

VST3GoSAAudio* vst3go_sa_audio_new(const char* backend, const char** error) {
    ma_backend chosen = ma_backend_null;
    bool one = backend && *backend;
    if (one) {
        int i = find_backend(backend);
        if (i < 0) {
            *error = "unknown audio backend";
            return NULL;
        }
        chosen = backends[i].backend;
    }
    VST3GoSAAudio* a = (VST3GoSAAudio*)ma_malloc(sizeof(VST3GoSAAudio), NULL);
    if (!a) {
        *error = "out of memory";
        return NULL;
    }
    memset(a, 0, sizeof(*a));
    ma_context_config config = ma_context_config_init();
    ma_result result = ma_context_init(one ? &chosen : NULL, one ? 1 : 0, &config, &a->context);
    if (result != MA_SUCCESS) {
        ma_free(a, NULL);
        *error = ma_result_description(result);
        return NULL;
    }
    result = ma_context_get_devices(&a->context, &a->outputs, &a->numOutputs, &a->inputs, &a->numInputs);
    if (result != MA_SUCCESS) {
        vst3go_sa_audio_free(a);
        *error = ma_result_description(result);
        return NULL;
    }
    return a;
}

void vst3go_sa_audio_free(VST3GoSAAudio* a) {
    if (!a) return;
    if (a->deviceOpen) ma_device_uninit(&a->device);
    ma_context_uninit(&a->context);
    ma_free(a, NULL);
}

uint32_t vst3go_sa_audio_device_count(VST3GoSAAudio* a, bool capture) { return capture ? a->numInputs : a->numOutputs; }

const char* vst3go_sa_audio_device_name(VST3GoSAAudio* a, bool capture, uint32_t index) {
    return capture ? a->inputs[index].name : a->outputs[index].name;
}

bool vst3go_sa_audio_device_default(VST3GoSAAudio* a, bool capture, uint32_t index) {
    return capture ? a->inputs[index].isDefault : a->outputs[index].isDefault;
}

void vst3go_sa_audio_render(const float* in, uint32_t in_channels, float* out, uint32_t out_channels, uint32_t frames) {
    // The device thread's.
    static uint8_t midi[SA_MIDI_BLOCK * 3], midiOutBlock[SA_MIDI_BLOCK * 3];
    uint32_t n = 0;
    while (n < SA_MIDI_BLOCK && ring_pop(&midiIn, midi + 3 * n)) n++;
    uint32_t count = vst3go_sa_process(in, in_channels, out, out_channels, frames, midi, n, midiOutBlock, SA_MIDI_BLOCK);
    // Without a MIDI output port nothing drains the ring; then it drops.
    for (uint32_t i = 0; i < count; i++) ring_push(&midiOut, midiOutBlock + 3 * i);
}

// data renders a period on the device's thread.
static void data(ma_device* device, void* out, const void* in, ma_uint32 frames) {
    VST3GoSAAudio* a = (VST3GoSAAudio*)device->pUserData;
    vst3go_sa_audio_render((const float*)in, a->inChannels, (float*)out, a->outChannels, frames);
}

const char* vst3go_sa_audio_open(VST3GoSAAudio* a, int32_t output, int32_t input, uint32_t sample_rate, uint32_t frames,
                                 uint32_t in_channels, uint32_t out_channels) {
    ma_device_config config = ma_device_config_init(in_channels > 0 ? ma_device_type_duplex : ma_device_type_playback);
    config.sampleRate = sample_rate;
    config.periodSizeInFrames = frames;
    config.playback.format = ma_format_f32;
    config.playback.channels = out_channels;
    if (output >= 0 && (ma_uint32)output < a->numOutputs) config.playback.pDeviceID = &a->outputs[output].id;
    config.capture.format = ma_format_f32;
    config.capture.channels = in_channels;
    if (in_channels > 0 && input >= 0 && (ma_uint32)input < a->numInputs) config.capture.pDeviceID = &a->inputs[input].id;
    config.dataCallback = data;
    config.pUserData = a;
    a->inChannels = in_channels;
    a->outChannels = out_channels;
    ma_result result = ma_device_init(&a->context, &config, &a->device);
    if (result != MA_SUCCESS) return ma_result_description(result);
    a->deviceOpen = true;
    return NULL;
}

uint32_t vst3go_sa_audio_sample_rate(VST3GoSAAudio* a) { return a->device.sampleRate; }

const char* vst3go_sa_audio_start(VST3GoSAAudio* a) {
    ma_result result = ma_device_start(&a->device);
    return result == MA_SUCCESS ? NULL : ma_result_description(result);
}

bool vst3go_sa_audio_running(VST3GoSAAudio* a) { return a->deviceOpen && ma_device_is_started(&a->device); }

void vst3go_sa_audio_stop(VST3GoSAAudio* a) {
    if (vst3go_sa_audio_running(a)) ma_device_stop(&a->device);
}
