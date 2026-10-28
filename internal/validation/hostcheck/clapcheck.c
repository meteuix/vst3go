// clapcheck loads a validation plugin through its CLAP entry point and
// checks behavior clap-validator does not: sample-accurate automation in the
// plain parameter domain, note and note-expression routing, raw MIDI, output
// events, programs, latency, 64-bit audio and state with host rescans.
// scripts/validate_clap.sh builds and runs it.
#include "clap/clap.h"
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)
#define N 128

// ---- host ----
static struct {
    int rescanValues, flushRequests, callbacks, restarts, latencyChanged, dirty, noteNames;
} hostLog;

static void params_rescan(const clap_host_t* h, clap_param_rescan_flags flags) { (void)h; if (flags & CLAP_PARAM_RESCAN_VALUES) hostLog.rescanValues++; }
static void params_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags flags) { (void)h; (void)id; (void)flags; }
static void params_request_flush(const clap_host_t* h) { (void)h; hostLog.flushRequests++; }
static const clap_host_params_t hostParams = { params_rescan, params_clear, params_request_flush };
static void latency_changed(const clap_host_t* h) { (void)h; hostLog.latencyChanged++; }
static const clap_host_latency_t hostLatency = { latency_changed };
static void state_mark_dirty(const clap_host_t* h) { (void)h; hostLog.dirty++; }
static const clap_host_state_t hostState = { state_mark_dirty };
static void note_name_changed(const clap_host_t* h) { (void)h; hostLog.noteNames++; }
static const clap_host_note_name_t hostNoteName = { note_name_changed };

static const void* host_get_extension(const clap_host_t* h, const char* id) {
    (void)h;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    if (!strcmp(id, CLAP_EXT_LATENCY)) return &hostLatency;
    if (!strcmp(id, CLAP_EXT_STATE)) return &hostState;
    if (!strcmp(id, CLAP_EXT_NOTE_NAME)) return &hostNoteName;
    return NULL;
}
static void host_request_restart(const clap_host_t* h) { (void)h; hostLog.restarts++; }
static void host_request_process(const clap_host_t* h) { (void)h; }
static void host_request_callback(const clap_host_t* h) { (void)h; hostLog.callbacks++; }
static const clap_host_t host = {
    CLAP_VERSION_INIT, NULL, "clapcheck", "vst3go", "", "1.0",
    host_get_extension, host_request_restart, host_request_process, host_request_callback,
};

// ---- event lists ----
typedef union {
    clap_event_header_t header;
    clap_event_note_t note;
    clap_event_note_expression_t expr;
    clap_event_param_value_t param;
    clap_event_param_gesture_t gesture;
    clap_event_midi_t midi;
    clap_event_midi_sysex_t sysex;
} Event;

typedef struct { clap_input_events_t list; Event events[32]; uint32_t count; } InEvents;
typedef struct { clap_output_events_t list; Event events[64]; uint32_t count; } OutEvents;

static uint32_t in_size(const clap_input_events_t* l) { return ((const InEvents*)l->ctx)->count; }
static const clap_event_header_t* in_get(const clap_input_events_t* l, uint32_t i) { return &((const InEvents*)l->ctx)->events[i].header; }
static bool out_push(const clap_output_events_t* l, const clap_event_header_t* ev) {
    OutEvents* out = (OutEvents*)l->ctx;
    if (out->count == 64 || ev->size > sizeof(Event)) return false;
    memcpy(&out->events[out->count++], ev, ev->size);
    return true;
}
static void in_init(InEvents* in) { in->list.ctx = in; in->list.size = in_size; in->list.get = in_get; in->count = 0; }
static void out_init(OutEvents* out) { out->list.ctx = out; out->list.try_push = out_push; out->count = 0; }

static clap_event_header_t header(uint16_t type, uint32_t size, uint32_t time) {
    clap_event_header_t h = { size, time, CLAP_CORE_EVENT_SPACE_ID, type, 0 };
    return h;
}
static void add_note(InEvents* in, uint16_t type, uint32_t time, int16_t key, int32_t noteId, double velocity) {
    Event* e = &in->events[in->count++];
    e->note.header = header(type, sizeof(clap_event_note_t), time);
    e->note.note_id = noteId; e->note.port_index = 0; e->note.channel = 0; e->note.key = key; e->note.velocity = velocity;
}
static void add_param(InEvents* in, uint32_t time, clap_id id, double value) {
    Event* e = &in->events[in->count++];
    memset(e, 0, sizeof(*e));
    e->param.header = header(CLAP_EVENT_PARAM_VALUE, sizeof(clap_event_param_value_t), time);
    e->param.param_id = id; e->param.note_id = -1; e->param.port_index = -1; e->param.channel = -1; e->param.key = -1; e->param.value = value;
}
static void add_midi(InEvents* in, uint32_t time, uint8_t b0, uint8_t b1, uint8_t b2) {
    Event* e = &in->events[in->count++];
    e->midi.header = header(CLAP_EVENT_MIDI, sizeof(clap_event_midi_t), time);
    e->midi.port_index = 0; e->midi.data[0] = b0; e->midi.data[1] = b1; e->midi.data[2] = b2;
}

// ---- streams ----
typedef struct { uint8_t data[4096]; size_t size, pos, chunk; } Stream;
static int64_t ostream_write(const clap_ostream_t* s, const void* buf, uint64_t size) {
    Stream* st = (Stream*)s->ctx;
    if (size > st->chunk) size = st->chunk; // short writes
    if (st->size + size > sizeof(st->data)) return -1;
    memcpy(st->data + st->size, buf, size); st->size += size;
    return (int64_t)size;
}
static int64_t istream_read(const clap_istream_t* s, void* buf, uint64_t size) {
    Stream* st = (Stream*)s->ctx;
    if (size > st->chunk) size = st->chunk; // short reads
    if (size > st->size - st->pos) size = st->size - st->pos;
    memcpy(buf, st->data + st->pos, size); st->pos += size;
    return (int64_t)size;
}

// ---- plugin ----
typedef struct {
    const clap_plugin_t* plugin;
    const clap_plugin_params_t* params;
    const clap_plugin_state_t* state;
    const clap_plugin_audio_ports_t* audio;
    const clap_plugin_note_ports_t* notes;
    const clap_plugin_latency_t* latency;
    const clap_plugin_note_name_t* noteName;
    const char** features;
} Plugin;

static Plugin load(const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", path, dlerror());
    const clap_plugin_entry_t* entry = (const clap_plugin_entry_t*)dlsym(lib, "clap_entry");
    CHECK(entry && entry->init(path), "clap_entry");
    const clap_plugin_factory_t* factory = (const clap_plugin_factory_t*)entry->get_factory(CLAP_PLUGIN_FACTORY_ID);
    CHECK(factory && factory->get_plugin_count(factory) == 1, "one plugin in the factory");
    const clap_plugin_descriptor_t* desc = factory->get_plugin_descriptor(factory, 0);
    CHECK(desc && desc->id && desc->name && desc->features && desc->features[0], "descriptor");
    Plugin p; memset(&p, 0, sizeof(p));
    p.features = (const char**)desc->features;
    p.plugin = factory->create_plugin(factory, &host, desc->id);
    CHECK(p.plugin && p.plugin->init(p.plugin), "create and init %s", desc->id);
    p.params = (const clap_plugin_params_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_PARAMS);
    p.state = (const clap_plugin_state_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_STATE);
    p.audio = (const clap_plugin_audio_ports_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_AUDIO_PORTS);
    p.notes = (const clap_plugin_note_ports_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_NOTE_PORTS);
    p.latency = (const clap_plugin_latency_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_LATENCY);
    p.noteName = (const clap_plugin_note_name_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_NOTE_NAME);
    CHECK(p.params && p.state && p.audio && p.notes && p.latency, "extensions");
    return p;
}

static void start(Plugin* p) {
    CHECK(p->plugin->activate(p->plugin, 48000, 1, 4 * N), "activate");
    CHECK(p->plugin->start_processing(p->plugin), "start processing");
}

static void stop(Plugin* p) {
    p->plugin->stop_processing(p->plugin);
    p->plugin->deactivate(p->plugin);
    p->plugin->destroy(p->plugin);
}

static clap_process_t make_process(clap_audio_buffer_t* ins, uint32_t nin, clap_audio_buffer_t* outs, uint32_t nout, uint32_t frames, InEvents* in, OutEvents* out) {
    clap_process_t proc; memset(&proc, 0, sizeof(proc));
    proc.steady_time = -1; proc.frames_count = frames;
    proc.audio_inputs = ins; proc.audio_inputs_count = nin;
    proc.audio_outputs = outs; proc.audio_outputs_count = nout;
    proc.in_events = &in->list; proc.out_events = &out->list;
    return proc;
}

static bool has_feature(const Plugin* p, const char* feature) {
    for (const char** f = p->features; *f; f++) if (!strcmp(*f, feature)) return true;
    return false;
}

static double param_value(const Plugin* p, clap_id id) {
    double v = NAN;
    CHECK(p->params->get_value(p->plugin, id, &v), "get_value %u", id);
    return v;
}

static int find_param(const Plugin* p, const char* name, clap_param_info_t* info) {
    uint32_t count = p->params->count(p->plugin);
    for (uint32_t i = 0; i < count; i++) {
        CHECK(p->params->get_info(p->plugin, i, info), "param info %u", i);
        if (!strcmp(info->name, name)) return 1;
    }
    return 0;
}

static void test_gain(const char* path) {
    Plugin p = load(path);
    CHECK(has_feature(&p, CLAP_PLUGIN_FEATURE_AUDIO_EFFECT) && has_feature(&p, CLAP_PLUGIN_FEATURE_UTILITY), "gain features");
    // The output level meter is VST3-only.
    CHECK(p.params->count(p.plugin) == 2, "gain exposes Gain and Bypass, got %u", p.params->count(p.plugin));
    clap_param_info_t info;
    CHECK(find_param(&p, "Gain", &info) && info.flags & CLAP_PARAM_IS_AUTOMATABLE && !(info.flags & CLAP_PARAM_IS_STEPPED), "gain info");
    CHECK(info.max_value > info.min_value && fabs(info.default_value) < 1e-9, "gain range %g..%g default %g", info.min_value, info.max_value, info.default_value);
    char text[64];
    CHECK(p.params->value_to_text(p.plugin, info.id, -6, text, sizeof(text)), "value_to_text");
    double parsed;
    CHECK(p.params->text_to_value(p.plugin, info.id, text, &parsed) && fabs(parsed + 6) < 0.01, "text %s parses to %g", text, parsed);

    // The editor is offered for the platform's window API only (on Linux
    // only in a webkitgtk build); editorcheck opens it.
    const clap_plugin_gui_t* gui = (const clap_plugin_gui_t*)p.plugin->get_extension(p.plugin, CLAP_EXT_GUI);
#if defined(__APPLE__)
    CHECK(gui, "gui extension");
    const char* platformAPI = CLAP_WINDOW_API_COCOA;
#else
    const char* platformAPI = CLAP_WINDOW_API_X11;
#endif
    if (gui) {
        const char* api = NULL;
        bool floating = true;
        CHECK(gui->get_preferred_api(p.plugin, &api, &floating) && api && !strcmp(api, platformAPI) && !floating, "preferred gui api");
        CHECK(gui->is_api_supported(p.plugin, platformAPI, false) && !gui->is_api_supported(p.plugin, platformAPI, true), "embedded gui only");
        CHECK(!gui->is_api_supported(p.plugin, CLAP_WINDOW_API_WIN32, false) || !strcmp(platformAPI, CLAP_WINDOW_API_WIN32), "foreign gui api");
        CHECK(p.plugin->get_extension(p.plugin, CLAP_EXT_TIMER_SUPPORT), "timer-support with the gui");
    } else {
        CHECK(!p.plugin->get_extension(p.plugin, CLAP_EXT_TIMER_SUPPORT), "timer-support without a gui");
    }

    clap_audio_port_info_t port;
    CHECK(p.audio->count(p.plugin, true) == 1 && p.audio->get(p.plugin, 0, false, &port), "audio ports");
    CHECK(port.channel_count == 2 && port.flags & CLAP_AUDIO_PORT_IS_MAIN && port.flags & CLAP_AUDIO_PORT_SUPPORTS_64BITS && !strcmp(port.port_type, CLAP_PORT_STEREO), "main stereo 64-bit port");

    start(&p);
    float inL[N], inR[N], outL[N], outR[N];
    for (int i = 0; i < N; i++) { inL[i] = inR[i] = 0.5f; }
    float* ins[2] = { inL, inR }; float* outs[2] = { outL, outR };
    clap_audio_buffer_t ib = { ins, NULL, 2, 0, 0 }, ob = { outs, NULL, 2, 0, 0 };
    InEvents in; OutEvents out; in_init(&in); out_init(&out);
    add_param(&in, 64, info.id, -6.0);
    clap_process_t proc = make_process(&ib, 1, &ob, 1, N, &in, &out);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    float g = powf(10.0f, -6.0f / 20.0f);
    CHECK(fabsf(outL[63] - 0.5f) < 1e-6f && fabsf(outL[64] - 0.5f * g) < 1e-6f && fabsf(outR[N - 1] - 0.5f * g) < 1e-6f,
          "sample-accurate gain: out[63]=%f out[64]=%f", outL[63], outL[64]);
    CHECK(fabs(param_value(&p, info.id) + 6) < 1e-9, "gain value after automation");
    CHECK(out.count == 0, "no output events, got %u", out.count);

    // 64-bit buffers.
    double in64[N], out64[N]; for (int i = 0; i < N; i++) in64[i] = 0.125;
    double* ins64[1] = { in64 }; double* outs64[1] = { out64 };
    clap_audio_buffer_t ib64 = { NULL, ins64, 1, 0, 0 }, ob64 = { NULL, outs64, 1, 0, 0 };
    in_init(&in);
    proc = make_process(&ib64, 1, &ob64, 1, N, &in, &out);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process 64");
    CHECK(fabs(out64[N - 1] - 0.125 * pow(10, -6.0 / 20)) < 1e-12, "64-bit gain %.15f", out64[N - 1]);

    // State round trip through short reads and writes, with a value rescan.
    Stream st; memset(&st, 0, sizeof(st)); st.chunk = 7;
    clap_ostream_t os = { &st, ostream_write };
    CHECK(p.state->save(p.plugin, &os) && st.size > 0, "state save");
    in_init(&in); out_init(&out);
    add_param(&in, 0, info.id, 3.0);
    p.plugin->stop_processing(p.plugin);
    p.params->flush(p.plugin, &in.list, &out.list);
    CHECK(fabs(param_value(&p, info.id) - 3) < 1e-9, "flush sets the value");
    clap_istream_t is = { &st, istream_read };
    int rescans = hostLog.rescanValues;
    CHECK(p.state->load(p.plugin, &is), "state load");
    CHECK(fabs(param_value(&p, info.id) + 6) < 1e-9 && hostLog.rescanValues > rescans, "state restores -6 dB and requests a rescan");
    p.plugin->deactivate(p.plugin);
    p.plugin->destroy(p.plugin);
    printf("gain ok\n");
}

static void test_synth(const char* path) {
    Plugin p = load(path);
    CHECK(has_feature(&p, CLAP_PLUGIN_FEATURE_INSTRUMENT) && has_feature(&p, CLAP_PLUGIN_FEATURE_SYNTHESIZER), "synth features");
    clap_note_port_info_t np;
    CHECK(p.notes->count(p.plugin, true) == 1 && p.notes->get(p.plugin, 0, true, &np), "note input port");
    CHECK(np.supported_dialects & CLAP_NOTE_DIALECT_CLAP && np.supported_dialects & CLAP_NOTE_DIALECT_MIDI && np.preferred_dialect == CLAP_NOTE_DIALECT_CLAP, "dialects");
    CHECK(p.audio->count(p.plugin, true) == 0 && p.audio->count(p.plugin, false) == 1, "generator ports");
    clap_param_info_t program, shape;
    CHECK(find_param(&p, "Program", &program) && program.flags & CLAP_PARAM_IS_ENUM && program.max_value == 3, "program selector");
    CHECK(find_param(&p, "Shape", &shape) && shape.flags & CLAP_PARAM_IS_STEPPED && shape.max_value == 1, "shape is stepped");
    char text[64];
    CHECK(p.params->value_to_text(p.plugin, program.id, 1, text, sizeof(text)) && !strcmp(text, "Pluck"), "program name %s", text);

    start(&p);
    float outL[N], outR[N]; float* outs[2] = { outL, outR };
    clap_audio_buffer_t ob = { outs, NULL, 2, 0, 0 };
    InEvents in; OutEvents out; in_init(&in); out_init(&out);
    add_note(&in, CLAP_EVENT_NOTE_ON, 10, 69, -1, 1.0); // no host note ID
    clap_process_t proc = make_process(NULL, 0, &ob, 1, N, &in, &out);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    for (int i = 0; i < 10; i++) CHECK(outL[i] == 0, "silence before the note [%d]=%f", i, outL[i]);
    CHECK(outL[10] != 0 && outL[N - 1] != 0 && ob.constant_mask == 0, "note starts at offset 10");

    // Note expression by key (note ID -1): volume 0 silences the note.
    in_init(&in);
    Event* e = &in.events[in.count++];
    e->expr.header = header(CLAP_EVENT_NOTE_EXPRESSION, sizeof(clap_event_note_expression_t), 0);
    e->expr.expression_id = CLAP_NOTE_EXPRESSION_VOLUME; e->expr.note_id = -1; e->expr.port_index = 0; e->expr.channel = 0; e->expr.key = 69; e->expr.value = 0;
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    for (int i = 0; i < N; i++) CHECK(outL[i] == 0, "note expression volume 0: [%d]=%f", i, outL[i]);

    // Wildcard note off ends it; the release decays to a constant output.
    in_init(&in);
    add_note(&in, CLAP_EVENT_NOTE_OFF, 0, -1, -1, 0);
    for (int block = 0; block < 2 * 48000 / N + 2; block++) {
        CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
        in.count = 0;
    }
    CHECK(ob.constant_mask == 3, "released synth reports constant (silent) channels, got %llu", (unsigned long long)ob.constant_mask);

    // Raw MIDI 1.0: a note on through the MIDI dialect.
    in_init(&in);
    add_midi(&in, 20, 0x90, 60, 100);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    CHECK(outL[19] == 0 && outL[20] != 0, "MIDI note on at offset 20");
    in_init(&in);
    add_midi(&in, 0, 0x80, 60, 0);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");

    // Selecting a program by automation loads its values and asks the host
    // to re-read them on the main thread.
    clap_param_info_t release;
    CHECK(find_param(&p, "Release", &release), "release param");
    in_init(&in);
    add_param(&in, 0, program.id, 1); // Pluck: release 80 ms
    int callbacks = hostLog.callbacks, rescans = hostLog.rescanValues;
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    CHECK(fabs(param_value(&p, release.id) - 80) < 1e-6, "Pluck release %g", param_value(&p, release.id));
    CHECK(hostLog.callbacks > callbacks, "program change requests a main-thread callback");
    p.plugin->on_main_thread(p.plugin);
    CHECK(hostLog.rescanValues > rescans, "program change rescans values");

    // The Keyswitched program names its keyswitches.
    in_init(&in);
    add_param(&in, 0, program.id, 3);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    p.plugin->on_main_thread(p.plugin);
    CHECK(p.noteName && p.noteName->count(p.plugin) == 2, "2 note names");
    clap_note_name_t name;
    CHECK(p.noteName->get(p.plugin, 0, &name) && name.key == 24 && !strcmp(name.name, "Saw"), "note name %d %s", name.key, name.name);
    stop(&p);
    printf("synth ok\n");
}

static void test_midifx(const char* path) {
    Plugin p = load(path);
    CHECK(has_feature(&p, CLAP_PLUGIN_FEATURE_NOTE_EFFECT), "note effect feature");
    CHECK(p.notes->count(p.plugin, true) == 1 && p.notes->count(p.plugin, false) == 1, "note ports in and out");
    clap_param_info_t transpose;
    CHECK(find_param(&p, "Transpose", &transpose) && transpose.flags & CLAP_PARAM_IS_STEPPED && transpose.min_value == 0 && transpose.max_value == 48, "transpose steps");
    char text[64];
    CHECK(p.params->value_to_text(p.plugin, transpose.id, 36, text, sizeof(text)) && !strcmp(text, "12 st"), "step 36 is %s", text);

    start(&p);
    InEvents in; OutEvents out; in_init(&in); out_init(&out);
    add_param(&in, 0, transpose.id, 36); // +12 semitones
    add_note(&in, CLAP_EVENT_NOTE_ON, 5, 60, 3, 0.5);
    add_note(&in, CLAP_EVENT_NOTE_OFF, 40, 60, 3, 0);
    clap_process_t proc = make_process(NULL, 0, NULL, 0, N, &in, &out);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    CHECK(out.count == 2, "2 output events, got %u", out.count);
    CHECK(out.events[0].header.type == CLAP_EVENT_NOTE_ON && out.events[0].header.time == 5 && out.events[0].note.key == 72 && out.events[0].note.note_id == 3 && fabs(out.events[0].note.velocity - 0.5) < 1e-6, "transposed note on");
    CHECK(out.events[1].header.type == CLAP_EVENT_NOTE_OFF && out.events[1].header.time == 40 && out.events[1].note.key == 72, "matching note off");

    // SysEx and controllers pass through.
    static const uint8_t sysex[] = { 0xF0, 0x7D, 0x01, 0x02, 0xF7 };
    in_init(&in); out_init(&out);
    Event* e = &in.events[in.count++];
    e->sysex.header = header(CLAP_EVENT_MIDI_SYSEX, sizeof(clap_event_midi_sysex_t), 3);
    e->sysex.port_index = 0; e->sysex.buffer = sysex; e->sysex.size = sizeof(sysex);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    CHECK(out.count == 1 && out.events[0].header.type == CLAP_EVENT_MIDI_SYSEX && out.events[0].header.time == 3, "SysEx output");
    CHECK(out.events[0].sysex.size == sizeof(sysex) && out.events[0].sysex.buffer != sysex && !memcmp(out.events[0].sysex.buffer, sysex, sizeof(sysex)), "SysEx payload copied");
    stop(&p);
    printf("midifx ok\n");
}

static void test_ducker(const char* path) {
    Plugin p = load(path);
    CHECK(has_feature(&p, CLAP_PLUGIN_FEATURE_AUDIO_EFFECT) && has_feature(&p, CLAP_PLUGIN_FEATURE_COMPRESSOR), "ducker features");
    CHECK(p.audio->count(p.plugin, true) == 2, "main and sidechain inputs");
    clap_audio_port_info_t sc;
    CHECK(p.audio->get(p.plugin, 1, true, &sc) && !(sc.flags & CLAP_AUDIO_PORT_IS_MAIN) && sc.id != 0, "sidechain port");
    clap_param_info_t bypass, reduction;
    CHECK(find_param(&p, "Bypass", &bypass) && bypass.flags & CLAP_PARAM_IS_BYPASS, "bypass flag");
    CHECK(!find_param(&p, "Gain Reduction", &reduction), "read-only meter is not a CLAP parameter");

    start(&p);
    uint32_t latency = p.latency->get(p.plugin);
    CHECK(latency == 96, "latency %u, want 96 (2 ms @ 48k)", latency);
    static float main0[4 * N], main1[4 * N], sc0[4 * N], sc1[4 * N], o0[4 * N], o1[4 * N];
    for (int i = 0; i < 4 * N; i++) { main0[i] = main1[i] = (i == 0) ? 1.0f : 0.0f; }
    float* m[2] = { main0, main1 }; float* s[2] = { sc0, sc1 }; float* o[2] = { o0, o1 };
    clap_audio_buffer_t ins[2] = { { m, NULL, 2, 0, 0 }, { s, NULL, 2, 0, 0 } };
    clap_audio_buffer_t ob = { o, NULL, 2, 0, 0 };
    InEvents in; OutEvents out; in_init(&in); out_init(&out);
    clap_process_t proc = make_process(ins, 2, &ob, 1, 4 * N, &in, &out);
    CHECK(p.plugin->process(p.plugin, &proc) == CLAP_PROCESS_CONTINUE, "process");
    for (int i = 0; i < 4 * N; i++) CHECK(o0[i] == (i == (int)latency ? 1.0f : 0.0f), "impulse delayed by latency: o[%d]=%f", i, o0[i]);
    stop(&p);
    printf("ducker ok\n");
}

int main(int argc, char** argv) {
    CHECK(argc == 3, "usage: clapcheck gain|synth|midifx|ducker <binary>");
    if (!strcmp(argv[1], "gain")) test_gain(argv[2]);
    else if (!strcmp(argv[1], "synth")) test_synth(argv[2]);
    else if (!strcmp(argv[1], "midifx")) test_midifx(argv[2]);
    else if (!strcmp(argv[1], "ducker")) test_ducker(argv[2]);
    else CHECK(0, "unknown test %s", argv[1]);
    return 0;
}
