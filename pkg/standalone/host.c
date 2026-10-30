//go:build standalone

#include "host.h"

#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

#include "platform.h"

// The plugin's own CLAP entry, linked from pkg/plugin.
extern const clap_plugin_entry_t clap_entry;

typedef struct {
    uint32_t channels;
    float** data;  // channels pointers into storage
    float* storage;
} Port;

enum { MAX_MIDI_IN = 512 };

static struct {
    const clap_plugin_factory_t* factory;
    const clap_plugin_t* plugin;
    const clap_plugin_audio_ports_t* audioPorts;
    const clap_plugin_note_ports_t* notePorts;
    const clap_plugin_state_t* state;
    const clap_plugin_gui_t* gui;
    const clap_plugin_latency_t* latency;
    const clap_plugin_timer_support_t* timer;

    Port* ins;
    Port* outs;
    clap_audio_buffer_t* inBuffers;
    clap_audio_buffer_t* outBuffers;
    uint32_t inCount, outCount;
    uint32_t maxFrames;
    bool active;
    bool noteMIDI, noteCLAP;  // dialects of note input port 0
    bool guiCreated;

    bool processing;  // audio thread
    atomic_bool stopRequest, stopped;
    atomic_bool callbackRequested, restartRequested;
    atomic_uint_least64_t pendingSize;  // width << 32 | height, 0: none
    int64_t steadyTime;
    double sampleRate;

    // Transport: the main thread sets it, the audio thread plays it.
    atomic_bool playing, rewindRequest;
    atomic_uint_least64_t tempoBits;      // double
    atomic_uint_least32_t timeSignature;  // numerator << 16 | denominator
    atomic_uint_least64_t positionBits;   // double beats, published by the audio thread
    double beats;                         // audio thread
    clap_event_transport_t transport;

    clap_event_midi_t midiIn[MAX_MIDI_IN];
    clap_event_note_t notesIn[MAX_MIDI_IN];
    const clap_event_header_t* events[MAX_MIDI_IN];
    uint32_t eventCount;
    uint8_t* midiOut;
    uint32_t midiOutCount, midiOutCapacity;

#ifdef _WIN32
    DWORD mainThread;
#else
    pthread_t mainThread;
#endif
} sa;

static _Thread_local bool inAudioThread;

// ---- host extensions ----

static void host_log(const clap_host_t* h, clap_log_severity severity, const char* msg) {
    (void)h;
    static const char* names[] = { "debug", "info", "warning", "error", "fatal", "host misbehaving", "plugin misbehaving" };
    const char* name = severity >= 0 && severity < (int)(sizeof(names) / sizeof(names[0])) ? names[severity] : "log";
    if (severity >= CLAP_LOG_WARNING) fprintf(stderr, "plugin %s: %s\n", name, msg ? msg : "");
}
static const clap_host_log_t hostLog = { host_log };

static bool is_main_thread(const clap_host_t* h) {
    (void)h;
#ifdef _WIN32
    return GetCurrentThreadId() == sa.mainThread;
#else
    return pthread_equal(pthread_self(), sa.mainThread);
#endif
}
static bool is_audio_thread(const clap_host_t* h) { (void)h; return inAudioThread; }
static const clap_host_thread_check_t hostThreadCheck = { is_main_thread, is_audio_thread };

// The audio thread processes continuously, so edits reach the plugin on the
// next block and no flush is needed.
static void params_rescan(const clap_host_t* h, clap_param_rescan_flags flags) { (void)h; (void)flags; }
static void params_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags flags) { (void)h; (void)id; (void)flags; }
static void params_request_flush(const clap_host_t* h) { (void)h; }
static const clap_host_params_t hostParams = { params_rescan, params_clear, params_request_flush };

static void latency_changed(const clap_host_t* h) { (void)h; }
static const clap_host_latency_t hostLatency = { latency_changed };

static bool ports_rescan_supported(const clap_host_t* h, uint32_t flag) { (void)h; (void)flag; return false; }
static void ports_rescan(const clap_host_t* h, uint32_t flags) { (void)h; (void)flags; }
static const clap_host_audio_ports_t hostAudioPorts = { ports_rescan_supported, ports_rescan };

static uint32_t note_dialects(const clap_host_t* h) { (void)h; return CLAP_NOTE_DIALECT_CLAP | CLAP_NOTE_DIALECT_MIDI; }
static void note_rescan(const clap_host_t* h, uint32_t flags) { (void)h; (void)flags; }
static const clap_host_note_ports_t hostNotePorts = { note_dialects, note_rescan };

static void state_mark_dirty(const clap_host_t* h) { (void)h; }
static const clap_host_state_t hostState = { state_mark_dirty };

static void gui_resize_hints_changed(const clap_host_t* h) { (void)h; }
static bool gui_request_resize(const clap_host_t* h, uint32_t width, uint32_t height) {
    if (!width || !height) return false;
    if (is_main_thread(h)) {
        vst3go_sa_window_resize(width, height);
    } else {
        atomic_store(&sa.pendingSize, (uint64_t)width << 32 | height);
    }
    return true;
}
static bool gui_request_show(const clap_host_t* h) { (void)h; return false; }
static bool gui_request_hide(const clap_host_t* h) { (void)h; return false; }
static void gui_closed(const clap_host_t* h, bool destroyed) { (void)h; (void)destroyed; vst3go_sa_quit(); }
static const clap_host_gui_t hostGui = {
    gui_resize_hints_changed, gui_request_resize, gui_request_show, gui_request_hide, gui_closed
};

static bool timer_register(const clap_host_t* h, uint32_t period, clap_id* id) { (void)h; return vst3go_sa_timer_register(period, id); }
static bool timer_unregister(const clap_host_t* h, clap_id id) { (void)h; return vst3go_sa_timer_unregister(id); }
static const clap_host_timer_support_t hostTimer = { timer_register, timer_unregister };

static const void* host_get_extension(const clap_host_t* h, const char* id) {
    (void)h;
    if (!id) return NULL;
    if (!strcmp(id, CLAP_EXT_LOG)) return &hostLog;
    if (!strcmp(id, CLAP_EXT_THREAD_CHECK)) return &hostThreadCheck;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    if (!strcmp(id, CLAP_EXT_LATENCY)) return &hostLatency;
    if (!strcmp(id, CLAP_EXT_AUDIO_PORTS)) return &hostAudioPorts;
    if (!strcmp(id, CLAP_EXT_NOTE_PORTS)) return &hostNotePorts;
    if (!strcmp(id, CLAP_EXT_STATE)) return &hostState;
    if (!strcmp(id, CLAP_EXT_GUI) && vst3go_sa_window_api()) return &hostGui;
    if (!strcmp(id, CLAP_EXT_TIMER_SUPPORT) && vst3go_sa_timers_supported()) return &hostTimer;
    return NULL;
}

static void host_request_restart(const clap_host_t* h) { (void)h; atomic_store(&sa.restartRequested, true); }
static void host_request_process(const clap_host_t* h) { (void)h; }
static void host_request_callback(const clap_host_t* h) { (void)h; atomic_store(&sa.callbackRequested, true); }

static const clap_host_t host = {
    CLAP_VERSION_INIT, NULL, "vst3go standalone", "vst3go", "https://github.com/th-release/vst3go", "1.0",
    host_get_extension, host_request_restart, host_request_process, host_request_callback,
};

// ---- lifecycle ----

static const clap_plugin_factory_t* factory(void) {
    if (!sa.factory && clap_entry.init("")) {
        sa.factory = (const clap_plugin_factory_t*)clap_entry.get_factory(CLAP_PLUGIN_FACTORY_ID);
    }
    return sa.factory;
}

uint32_t vst3go_sa_plugin_count(void) {
    const clap_plugin_factory_t* f = factory();
    return f ? f->get_plugin_count(f) : 0;
}

const clap_plugin_descriptor_t* vst3go_sa_plugin_descriptor(uint32_t index) {
    const clap_plugin_factory_t* f = factory();
    return f ? f->get_plugin_descriptor(f, index) : NULL;
}

static bool alloc_ports(bool input, Port** ports, clap_audio_buffer_t** buffers, uint32_t* count) {
    uint32_t n = sa.audioPorts ? sa.audioPorts->count(sa.plugin, input) : 0;
    *count = n;
    *ports = (Port*)calloc(n ? n : 1, sizeof(Port));
    *buffers = (clap_audio_buffer_t*)calloc(n ? n : 1, sizeof(clap_audio_buffer_t));
    if (!*ports || !*buffers) return false;
    for (uint32_t i = 0; i < n; i++) {
        clap_audio_port_info_t info;
        if (!sa.audioPorts->get(sa.plugin, i, input, &info)) return false;
        (*ports)[i].channels = info.channel_count;
    }
    return true;
}

static void free_ports(Port* ports, uint32_t count) {
    for (uint32_t i = 0; ports && i < count; i++) {
        free(ports[i].data);
        free(ports[i].storage);
    }
    free(ports);
}

const char* vst3go_sa_create(uint32_t index) {
#ifdef _WIN32
    sa.mainThread = GetCurrentThreadId();
#else
    sa.mainThread = pthread_self();
#endif
    const clap_plugin_descriptor_t* desc = vst3go_sa_plugin_descriptor(index);
    if (!desc) return "no plugin registered";
    sa.plugin = sa.factory->create_plugin(sa.factory, &host, desc->id);
    if (!sa.plugin) return "create_plugin failed";
    if (!sa.plugin->init(sa.plugin)) {
        sa.plugin->destroy(sa.plugin);
        sa.plugin = NULL;
        return "plugin init failed";
    }
    sa.audioPorts = (const clap_plugin_audio_ports_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_AUDIO_PORTS);
    sa.notePorts = (const clap_plugin_note_ports_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_NOTE_PORTS);
    sa.state = (const clap_plugin_state_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_STATE);
    sa.gui = (const clap_plugin_gui_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_GUI);
    sa.latency = (const clap_plugin_latency_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_LATENCY);
    sa.timer = (const clap_plugin_timer_support_t*)sa.plugin->get_extension(sa.plugin, CLAP_EXT_TIMER_SUPPORT);
    if (!alloc_ports(true, &sa.ins, &sa.inBuffers, &sa.inCount) || !alloc_ports(false, &sa.outs, &sa.outBuffers, &sa.outCount)) {
        return "audio ports";
    }
    clap_note_port_info_t note;
    if (sa.notePorts && sa.notePorts->count(sa.plugin, true) > 0 && sa.notePorts->get(sa.plugin, 0, true, &note)) {
        sa.noteMIDI = note.supported_dialects & CLAP_NOTE_DIALECT_MIDI;
        sa.noteCLAP = note.supported_dialects & CLAP_NOTE_DIALECT_CLAP;
    }
    return NULL;
}

void vst3go_sa_destroy(void) {
    if (!sa.plugin) return;
    vst3go_sa_gui_destroy();
    vst3go_sa_deactivate();
    sa.plugin->destroy(sa.plugin);
    sa.plugin = NULL;
    free_ports(sa.ins, sa.inCount);
    free_ports(sa.outs, sa.outCount);
    free(sa.inBuffers);
    free(sa.outBuffers);
    sa.ins = sa.outs = NULL;
    sa.inBuffers = sa.outBuffers = NULL;
    clap_entry.deinit();
    sa.factory = NULL;
}

uint32_t vst3go_sa_main_channels(bool input) {
    uint32_t count = input ? sa.inCount : sa.outCount;
    Port* ports = input ? sa.ins : sa.outs;
    return count ? ports[0].channels : 0;
}

bool vst3go_sa_note_input(void) { return sa.noteMIDI || sa.noteCLAP; }

uint32_t vst3go_sa_latency(void) { return sa.latency ? sa.latency->get(sa.plugin) : 0; }

static bool alloc_buffers(Port* ports, clap_audio_buffer_t* buffers, uint32_t count, uint32_t frames) {
    for (uint32_t i = 0; i < count; i++) {
        Port* p = &ports[i];
        free(p->data);
        free(p->storage);
        p->data = (float**)calloc(p->channels ? p->channels : 1, sizeof(float*));
        p->storage = (float*)calloc((size_t)(p->channels ? p->channels : 1) * frames, sizeof(float));
        if (!p->data || !p->storage) return false;
        for (uint32_t ch = 0; ch < p->channels; ch++) p->data[ch] = p->storage + (size_t)ch * frames;
        buffers[i] = (clap_audio_buffer_t){ p->data, NULL, p->channels, 0, 0 };
    }
    return true;
}

bool vst3go_sa_activate(double sampleRate, uint32_t maxFrames) {
    if (!sa.plugin || sa.active || !maxFrames) return false;
    sa.maxFrames = maxFrames;
    if (!alloc_buffers(sa.ins, sa.inBuffers, sa.inCount, maxFrames) || !alloc_buffers(sa.outs, sa.outBuffers, sa.outCount, maxFrames)) {
        return false;
    }
    if (!sa.plugin->activate(sa.plugin, sampleRate, 1, maxFrames)) return false;
    sa.sampleRate = sampleRate;
    sa.active = true;
    sa.processing = false;
    atomic_store(&sa.stopRequest, false);
    atomic_store(&sa.stopped, false);
    atomic_store(&sa.restartRequested, false);
    return true;
}

void vst3go_sa_deactivate(void) {
    if (!sa.active) return;
    vst3go_sa_force_stop();
    sa.plugin->deactivate(sa.plugin);
    sa.active = false;
}

void vst3go_sa_request_stop(void) { atomic_store(&sa.stopRequest, true); }
bool vst3go_sa_stopped(void) { return atomic_load(&sa.stopped); }

void vst3go_sa_force_stop(void) {
    atomic_store(&sa.stopRequest, true);
    if (sa.processing) {
        sa.plugin->stop_processing(sa.plugin);
        sa.processing = false;
    }
    atomic_store(&sa.stopped, true);
}

// ---- audio ----

static uint32_t in_size(const clap_input_events_t* list) { (void)list; return sa.eventCount; }
static const clap_event_header_t* in_get(const clap_input_events_t* list, uint32_t i) {
    (void)list;
    return i < sa.eventCount ? sa.events[i] : NULL;
}
static const clap_input_events_t inEvents = { NULL, in_size, in_get };

static void push_midi_out(uint8_t status, uint8_t data1, uint8_t data2) {
    if (sa.midiOutCount >= sa.midiOutCapacity) return;
    uint8_t* m = sa.midiOut + 3 * sa.midiOutCount++;
    m[0] = status;
    m[1] = data1;
    m[2] = data2;
}

static bool out_try_push(const clap_output_events_t* list, const clap_event_header_t* e) {
    (void)list;
    if (e->space_id != CLAP_CORE_EVENT_SPACE_ID) return true;
    switch (e->type) {
    case CLAP_EVENT_MIDI: {
        const clap_event_midi_t* m = (const clap_event_midi_t*)e;
        push_midi_out(m->data[0], m->data[1], m->data[2]);
        break;
    }
    case CLAP_EVENT_NOTE_ON:
    case CLAP_EVENT_NOTE_OFF: {
        const clap_event_note_t* n = (const clap_event_note_t*)e;
        if (n->channel < 0 || n->channel > 15 || n->key < 0 || n->key > 127) break;
        int velocity = (int)(n->velocity * 127 + 0.5);
        if (velocity > 127) velocity = 127;
        if (e->type == CLAP_EVENT_NOTE_ON && velocity < 1) velocity = 1;
        push_midi_out((e->type == CLAP_EVENT_NOTE_ON ? 0x90 : 0x80) | n->channel, n->key, velocity);
        break;
    }
    }
    return true;
}
static const clap_output_events_t outEvents = { NULL, out_try_push };

// queue_midi turns the device's MIDI into input events of note port 0, as
// raw MIDI or, for plugins that only take CLAP notes, note events.
static void queue_midi(const uint8_t* midi, uint32_t count) {
    sa.eventCount = 0;
    if (!vst3go_sa_note_input()) return;
    for (uint32_t i = 0; i < count && sa.eventCount < MAX_MIDI_IN; i++) {
        const uint8_t* m = midi + 3 * i;
        if (sa.noteMIDI) {
            clap_event_midi_t* e = &sa.midiIn[sa.eventCount];
            *e = (clap_event_midi_t){ { sizeof(*e), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0 }, 0, { m[0], m[1], m[2] } };
            sa.events[sa.eventCount++] = &e->header;
            continue;
        }
        uint8_t kind = m[0] & 0xF0;
        if (kind != 0x80 && kind != 0x90) continue;
        bool on = kind == 0x90 && m[2] > 0;
        clap_event_note_t* e = &sa.notesIn[sa.eventCount];
        *e = (clap_event_note_t){ { sizeof(*e), 0, CLAP_CORE_EVENT_SPACE_ID, on ? CLAP_EVENT_NOTE_ON : CLAP_EVENT_NOTE_OFF, 0 },
                                  -1, 0, (int16_t)(m[0] & 0x0F), (int16_t)m[1], m[2] / 127.0 };
        sa.events[sa.eventCount++] = &e->header;
    }
}

// ---- transport ----

static uint64_t double_bits(double v) {
    uint64_t bits;
    memcpy(&bits, &v, sizeof bits);
    return bits;
}

static double bits_double(uint64_t bits) {
    double v;
    memcpy(&v, &bits, sizeof v);
    return v;
}

void vst3go_sa_transport_set(bool playing, double tempo, uint32_t numerator, uint32_t denominator) {
    if (tempo < VST3GO_SA_MIN_TEMPO) tempo = VST3GO_SA_MIN_TEMPO;
    if (tempo > VST3GO_SA_MAX_TEMPO) tempo = VST3GO_SA_MAX_TEMPO;
    if (numerator < 1) numerator = 1;
    if (numerator > 64) numerator = 64;
    if (denominator != 1 && denominator != 2 && denominator != 4 && denominator != 8 && denominator != 16 && denominator != 32) denominator = 4;
    atomic_store(&sa.tempoBits, double_bits(tempo));
    atomic_store(&sa.timeSignature, numerator << 16 | denominator);
    atomic_store(&sa.playing, playing);
}

void vst3go_sa_transport_rewind(void) {
    atomic_store(&sa.rewindRequest, true);
    if (!atomic_load(&sa.playing)) atomic_store(&sa.positionBits, double_bits(0));
}

double vst3go_sa_transport_position(void) { return bits_double(atomic_load(&sa.positionBits)); }

// update_transport fills sa.transport for a block of frames starting at the
// current position, then advances the position over the block if playing.
static void update_transport(uint32_t frames) {
    if (atomic_exchange(&sa.rewindRequest, false)) sa.beats = 0;
    bool playing = atomic_load(&sa.playing);
    double tempo = bits_double(atomic_load(&sa.tempoBits));
    if (!(tempo >= VST3GO_SA_MIN_TEMPO)) tempo = VST3GO_SA_DEFAULT_TEMPO;
    uint32_t signature = atomic_load(&sa.timeSignature);
    uint16_t numerator = signature ? (uint16_t)(signature >> 16) : 4, denominator = signature ? (uint16_t)signature : 4;
    double barBeats = numerator * 4.0 / denominator;
    double bar = (double)(int64_t)(sa.beats / barBeats);
    clap_event_transport_t* t = &sa.transport;
    *t = (clap_event_transport_t){ { sizeof(*t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_TRANSPORT, 0 } };
    t->flags = CLAP_TRANSPORT_HAS_TEMPO | CLAP_TRANSPORT_HAS_BEATS_TIMELINE | CLAP_TRANSPORT_HAS_SECONDS_TIMELINE |
               CLAP_TRANSPORT_HAS_TIME_SIGNATURE | (playing ? CLAP_TRANSPORT_IS_PLAYING : 0);
    t->song_pos_beats = (clap_beattime)(sa.beats * CLAP_BEATTIME_FACTOR);
    t->song_pos_seconds = (clap_sectime)(sa.beats * 60.0 / tempo * CLAP_SECTIME_FACTOR);
    t->tempo = tempo;
    t->bar_start = (clap_beattime)(bar * barBeats * CLAP_BEATTIME_FACTOR);
    t->bar_number = (int32_t)bar;
    t->tsig_num = numerator;
    t->tsig_denom = denominator;
    if (playing && sa.sampleRate > 0) sa.beats += frames * tempo / (60.0 * sa.sampleRate);
    atomic_store(&sa.positionBits, double_bits(sa.beats));
}

static void silence(float* out, uint32_t channels, uint32_t frames) {
    if (out) memset(out, 0, sizeof(float) * channels * frames);
}

uint32_t vst3go_sa_process(const float* in, uint32_t inChannels, float* out, uint32_t outChannels, uint32_t frames,
                           const uint8_t* midi, uint32_t midiCount, uint8_t* midiOut, uint32_t midiOutCapacity) {
    inAudioThread = true;
    sa.midiOut = midiOut;
    sa.midiOutCount = 0;
    sa.midiOutCapacity = midiOutCapacity;
    if (!sa.active || atomic_load(&sa.stopRequest)) {
        if (sa.processing) {
            sa.plugin->stop_processing(sa.plugin);
            sa.processing = false;
        }
        atomic_store(&sa.stopped, true);
        silence(out, outChannels, frames);
        return 0;
    }
    if (!sa.processing) {
        if (!sa.plugin->start_processing(sa.plugin)) {
            silence(out, outChannels, frames);
            return 0;
        }
        sa.processing = true;
    }
    queue_midi(midi, midiCount);
    for (uint32_t offset = 0; offset < frames;) {
        uint32_t n = frames - offset < sa.maxFrames ? frames - offset : sa.maxFrames;
        for (uint32_t p = 0; p < sa.inCount; p++) {
            Port* port = &sa.ins[p];
            for (uint32_t ch = 0; ch < port->channels; ch++) {
                float* dst = port->data[ch];
                if (p == 0 && in && inChannels) {
                    // A mono device feeds every channel of the port.
                    uint32_t src = ch < inChannels ? ch : inChannels - 1;
                    for (uint32_t i = 0; i < n; i++) dst[i] = in[(size_t)(offset + i) * inChannels + src];
                } else {
                    memset(dst, 0, sizeof(float) * n);
                }
            }
        }
        update_transport(n);
        clap_process_t process = {
            sa.steadyTime, n, &sa.transport, sa.inBuffers, sa.outBuffers, sa.inCount, sa.outCount, &inEvents, &outEvents,
        };
        sa.plugin->process(sa.plugin, &process);
        sa.eventCount = 0;
        sa.steadyTime += n;
        if (out) {
            uint32_t channels = sa.outCount ? sa.outs[0].channels : 0;
            for (uint32_t i = 0; i < n; i++) {
                float* frame = out + (size_t)(offset + i) * outChannels;
                for (uint32_t ch = 0; ch < outChannels; ch++) {
                    // A mono plugin output plays on every device channel.
                    frame[ch] = channels ? sa.outs[0].data[ch < channels ? ch : channels - 1][i] : 0;
                }
            }
        }
        offset += n;
    }
    return sa.midiOutCount;
}

// ---- state ----

typedef struct {
    uint8_t* data;
    size_t size, capacity;
    size_t read;
} Buffer;

static int64_t ostream_write(const clap_ostream_t* stream, const void* data, uint64_t size) {
    Buffer* b = (Buffer*)stream->ctx;
    if (b->size + size > b->capacity) {
        size_t capacity = b->capacity ? b->capacity : 4096;
        while (capacity < b->size + size) capacity *= 2;
        uint8_t* grown = (uint8_t*)realloc(b->data, capacity);
        if (!grown) return -1;
        b->data = grown;
        b->capacity = capacity;
    }
    memcpy(b->data + b->size, data, size);
    b->size += size;
    return (int64_t)size;
}

static int64_t istream_read(const clap_istream_t* stream, void* data, uint64_t size) {
    Buffer* b = (Buffer*)stream->ctx;
    size_t n = b->size - b->read < size ? b->size - b->read : size;
    memcpy(data, b->data + b->read, n);
    b->read += n;
    return (int64_t)n;
}

bool vst3go_sa_state_save(uint8_t** data, size_t* size) {
    *data = NULL;
    *size = 0;
    if (!sa.state) return false;
    Buffer b = { 0 };
    clap_ostream_t stream = { &b, ostream_write };
    if (!sa.state->save(sa.plugin, &stream)) {
        free(b.data);
        return false;
    }
    *data = b.data;
    *size = b.size;
    return true;
}

bool vst3go_sa_state_load(const uint8_t* data, size_t size) {
    if (!sa.state) return false;
    Buffer b = { (uint8_t*)data, size, size, 0 };
    clap_istream_t stream = { &b, istream_read };
    return sa.state->load(sa.plugin, &stream);
}

// ---- main thread ----

uint32_t vst3go_sa_service(void) {
    if (!sa.plugin) return 0;
    if (atomic_exchange(&sa.callbackRequested, false)) sa.plugin->on_main_thread(sa.plugin);
    uint64_t size = atomic_exchange(&sa.pendingSize, 0);
    if (size && sa.guiCreated) vst3go_sa_window_resize((uint32_t)(size >> 32), (uint32_t)size);
    return atomic_exchange(&sa.restartRequested, false) ? VST3GO_SA_RESTART : 0;
}

bool vst3go_sa_gui_create(const char* api, double scale, uint32_t* width, uint32_t* height, bool* resizable) {
    if (!sa.gui || !api || !sa.gui->is_api_supported(sa.plugin, api, false)) return false;
    if (!sa.gui->create(sa.plugin, api, false)) return false;
    sa.guiCreated = true;
    if (scale > 0) sa.gui->set_scale(sa.plugin, scale);
    if (!sa.gui->get_size(sa.plugin, width, height) || !*width || !*height) {
        vst3go_sa_gui_destroy();
        return false;
    }
    *resizable = sa.gui->can_resize(sa.plugin);
    return true;
}

bool vst3go_sa_gui_attach(const clap_window_t* window) {
    return sa.guiCreated && sa.gui->set_parent(sa.plugin, window) && sa.gui->show(sa.plugin);
}

void vst3go_sa_gui_adjust(uint32_t* width, uint32_t* height) {
    if (!sa.guiCreated) return;
    if (!sa.gui->can_resize(sa.plugin) || !sa.gui->adjust_size(sa.plugin, width, height)) {
        sa.gui->get_size(sa.plugin, width, height);
    }
}

void vst3go_sa_gui_set_size(uint32_t width, uint32_t height) {
    if (sa.guiCreated && sa.gui->can_resize(sa.plugin)) sa.gui->set_size(sa.plugin, width, height);
}

void vst3go_sa_gui_destroy(void) {
    if (!sa.guiCreated) return;
    sa.guiCreated = false;
    sa.gui->hide(sa.plugin);
    sa.gui->destroy(sa.plugin);
}

void vst3go_sa_timer_fire(clap_id id) {
    if (sa.timer) sa.timer->on_timer(sa.plugin, id);
}
