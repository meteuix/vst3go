// LV2 plugins on Linux. Every LV2 plugin of the binary hosts the plugin's
// own CLAP entry, like the Audio Unit wrapper: lv2_descriptor lists one
// descriptor per registered plugin (URI urn:vst3go:<plugin ID>), its ports
// come from the CLAP audio ports, note ports and parameters, and
// VST3GoLV2WriteBundle writes the matching Turtle files (manifest.ttl,
// plugins.ttl with the ports and UIs, presets.ttl with the factory
// programs; see scripts/lv2info.c and vst3go build -format lv2).
//
// Ports, in this order:
//   - one audio port per channel of every CLAP audio input, then output
//     (side chains are lv2:isSideChain and connection optional);
//   - "control", an atom sequence input with time:Position (host
//     transport) and, for plugins with a note input, MIDI;
//   - "midi_out", an atom sequence output, for plugins with a note output;
//   - "latency", the reported latency in samples;
//   - one control port per visible parameter (hidden parameters and the
//     program list are left out; factory programs are LV2 presets),
//     read-only parameters as control outputs.
// Control port changes reach the plugin as parameter events at the start
// of the block. The plugin state (state:interface) is the CLAP state.

#define _GNU_SOURCE  // dladdr
#include <dlfcn.h>
#include <math.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <lv2/atom/atom.h>
#include <lv2/atom/forge.h>
#include <lv2/atom/util.h>
#include <lv2/buf-size/buf-size.h>
#include <lv2/core/lv2.h>
#include <lv2/core/lv2_util.h>
#include <lv2/midi/midi.h>
#include <lv2/options/options.h>
#include <lv2/state/state.h>
#include <lv2/time/time.h>
#include <lv2/urid/urid.h>

#include "clap_bridge.h"
#include "lv2_linux.h"

extern const clap_plugin_entry_t clap_entry;
extern char* GoLV2Email(void);

enum {
    LV2_PROGRAM_PARAM_ID = 0x7EFFFFFF,
    LV2_MAX_EVENTS = 2048,
    LV2_DEFAULT_MAX_FRAMES = 4096,
};

#define LV2_URI_PREFIX "urn:vst3go:"
#define LV2_STATE_KEY "urn:vst3go:state"

// ---- descriptors ----

static const clap_plugin_factory_t* lv2Factory;
static LV2_Descriptor* lv2Descriptors;
static uint32_t lv2DescriptorCount;
static pthread_once_t lv2Once = PTHREAD_ONCE_INIT;

static LV2_Handle lv2_instantiate(const LV2_Descriptor* d, double rate, const char* bundle, const LV2_Feature* const* features);
static void lv2_connect_port(LV2_Handle h, uint32_t port, void* data);
static void lv2_activate(LV2_Handle h);
static void lv2_run(LV2_Handle h, uint32_t frames);
static void lv2_deactivate(LV2_Handle h);
static void lv2_cleanup(LV2_Handle h);
static const void* lv2_extension_data(const char* uri);

static char* str_concat(const char* a, const char* b) {
    size_t na = strlen(a), nb = strlen(b);
    char* s = (char*)malloc(na + nb + 1);
    if (!s) return NULL;
    memcpy(s, a, na);
    memcpy(s + na, b, nb + 1);
    return s;
}

static void lv2_init_descriptors(void) {
    Dl_info info;
    const char* path = dladdr((const void*)&clap_entry, &info) && info.dli_fname ? info.dli_fname : "";
    if (!clap_entry.init(path)) return;
    lv2Factory = (const clap_plugin_factory_t*)clap_entry.get_factory(CLAP_PLUGIN_FACTORY_ID);
    if (!lv2Factory) return;
    uint32_t count = lv2Factory->get_plugin_count(lv2Factory);
    lv2Descriptors = (LV2_Descriptor*)calloc(count ? count : 1, sizeof(LV2_Descriptor));
    if (!lv2Descriptors) return;
    for (uint32_t i = 0; i < count; i++) {
        const clap_plugin_descriptor_t* d = lv2Factory->get_plugin_descriptor(lv2Factory, i);
        char* uri = d && d->id ? str_concat(LV2_URI_PREFIX, d->id) : NULL;
        if (!uri) break;
        lv2Descriptors[i] = (LV2_Descriptor){ uri, lv2_instantiate, lv2_connect_port, lv2_activate, lv2_run,
                                              lv2_deactivate, lv2_cleanup, lv2_extension_data };
        lv2DescriptorCount = i + 1;
    }
}

LV2_SYMBOL_EXPORT const LV2_Descriptor* lv2_descriptor(uint32_t index) {
    pthread_once(&lv2Once, lv2_init_descriptors);
    return index < lv2DescriptorCount ? &lv2Descriptors[index] : NULL;
}

// ---- instance ----

typedef enum {
    PORT_AUDIO_IN,
    PORT_AUDIO_OUT,
    PORT_EVENTS_IN,
    PORT_EVENTS_OUT,
    PORT_LATENCY,
    PORT_PARAM_IN,
    PORT_PARAM_OUT,
} PortKind;

typedef struct {
    PortKind kind;
    uint32_t index;    // audio port or parameter index
    uint32_t channel;  // audio channel
} PortDef;

typedef union {
    clap_event_header_t header;
    clap_event_param_value_t param;
    clap_event_midi_t midi;
    clap_event_midi_sysex_t sysex;
    clap_event_transport_t transport;
} AnyEvent;

typedef struct {
    LV2_URID atomChunk, atomSequence, atomObject, atomBlank, atomFloat, atomDouble, atomInt, atomLong;
    LV2_URID midiEvent, timePosition, timeBar, timeBarBeat, timeBeatUnit, timeBeatsPerBar, timeBeatsPerMinute;
    LV2_URID timeSpeed, timeFrame, maxBlockLength, nominalBlockLength, stateKey;
} URIDs;

enum { LV2_INSTANCE_MAGIC = 0x56334C32 };

struct LV2Instance {
    uint32_t magic;
    const clap_plugin_descriptor_t* descriptor;
    const clap_plugin_t* plugin;
    clap_host_t host;
    const clap_plugin_params_t* params;
    const clap_plugin_audio_ports_t* audioPorts;
    const clap_plugin_note_ports_t* notePorts;
    const clap_plugin_state_t* state;
    const clap_plugin_latency_t* latency;
    const clap_plugin_gui_t* gui;
    const clap_plugin_timer_support_t* timer;

    clap_param_info_t* paramInfo;
    uint32_t paramCount;
    int32_t programParam;
    clap_audio_port_info_t* audioIn;
    clap_audio_port_info_t* audioOut;
    uint32_t audioInCount, audioOutCount;
    uint32_t inChannels, outChannels;
    bool noteIn, noteOut;
    PortDef* ports;
    uint32_t portCount;

    double rate;
    uint32_t maxFrames;
    LV2_URID_Map* map;
    URIDs urid;

    void** data;              // per port, from connect_port
    float* last;              // per port: control value last sent to the plugin
    _Atomic double* values;   // per parameter: last value from the plugin
    atomic_bool resend;       // send every control port on the next block
    clap_audio_buffer_t* inBuffers;
    clap_audio_buffer_t* outBuffers;
    float** inPointers;
    float** outPointers;
    float* inStore;   // inputs are copied: LV2 hosts may process in place
    float* scratch;   // outputs the host left unconnected

    bool active, processing;
    int64_t steadyTime;
    uint32_t latencySamples;
    atomic_bool restartRequested, callbackRequested, rescanValues;

    AnyEvent store[LV2_MAX_EVENTS];
    const clap_event_header_t* events[LV2_MAX_EVENTS];
    uint32_t eventCount;
    uint32_t chunkFirst, chunkEnd;  // events of the current chunk
    uint32_t chunkStart;            // frame of the current chunk in the run

    LV2_Atom_Forge forge;
    LV2_Atom_Forge_Frame forgeFrame;
    bool forging;

    bool hasPosition;
    double bpm, beatsPerBar, beatUnit, speed, barBeat;
    int64_t bar, frame;

    VST3GoLV2UIEdit uiEdits[VST3GO_LV2_UI_EDITS];
    atomic_uint uiHead, uiTail;
    atomic_bool uiOpen;
    void* ui;  // the open UI (main thread)
};

static _Thread_local bool lv2InAudioThread;

// ---- host extensions ----

static LV2Instance* host_instance(const clap_host_t* h) { return (LV2Instance*)h->host_data; }

static void host_log(const clap_host_t* h, clap_log_severity severity, const char* msg) {
    (void)h;
    if (severity >= CLAP_LOG_WARNING) fprintf(stderr, "vst3go lv2: %s\n", msg ? msg : "");
}
static const clap_host_log_t hostLog = { host_log };

static bool host_is_main_thread(const clap_host_t* h) { (void)h; return !lv2InAudioThread; }
static bool host_is_audio_thread(const clap_host_t* h) { (void)h; return lv2InAudioThread; }
static const clap_host_thread_check_t hostThreadCheck = { host_is_main_thread, host_is_audio_thread };

// The host runs the plugin continuously, so edits reach it on the next
// block. Values the plugin changed itself (a program chosen in the editor)
// go to the host ports from the UI.
static void host_params_rescan(const clap_host_t* h, clap_param_rescan_flags flags) {
    LV2Instance* in = host_instance(h);
    if (in && (flags & (CLAP_PARAM_RESCAN_VALUES | CLAP_PARAM_RESCAN_ALL))) atomic_store(&in->rescanValues, true);
}
static void host_params_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags flags) { (void)h; (void)id; (void)flags; }
static void host_params_request_flush(const clap_host_t* h) { (void)h; }
static const clap_host_params_t hostParams = { host_params_rescan, host_params_clear, host_params_request_flush };

static void host_latency_changed(const clap_host_t* h) { (void)h; }
static const clap_host_latency_t hostLatency = { host_latency_changed };

static uint32_t host_note_dialects(const clap_host_t* h) { (void)h; return CLAP_NOTE_DIALECT_MIDI; }
static void host_note_rescan(const clap_host_t* h, uint32_t flags) { (void)h; (void)flags; }
static const clap_host_note_ports_t hostNotePorts = { host_note_dialects, host_note_rescan };

static void host_state_mark_dirty(const clap_host_t* h) { (void)h; }
static const clap_host_state_t hostState = { host_state_mark_dirty };

static const void* host_get_extension(const clap_host_t* h, const char* id) {
    if (!id) return NULL;
    if (!strcmp(id, CLAP_EXT_LOG)) return &hostLog;
    if (!strcmp(id, CLAP_EXT_THREAD_CHECK)) return &hostThreadCheck;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    if (!strcmp(id, CLAP_EXT_LATENCY)) return &hostLatency;
    if (!strcmp(id, CLAP_EXT_NOTE_PORTS)) return &hostNotePorts;
    if (!strcmp(id, CLAP_EXT_STATE)) return &hostState;
    return vst3go_lv2_ui_host_extension(host_instance(h), id);
}

static void host_request_restart(const clap_host_t* h) {
    LV2Instance* in = host_instance(h);
    if (in) atomic_store(&in->restartRequested, true);
}
static void host_request_process(const clap_host_t* h) { (void)h; }
static void host_request_callback(const clap_host_t* h) {
    LV2Instance* in = host_instance(h);
    if (in) atomic_store(&in->callbackRequested, true);
}

void vst3go_lv2_main_thread(LV2Instance* in) {
    if (atomic_exchange(&in->callbackRequested, false)) in->plugin->on_main_thread(in->plugin);
}

// ---- creation and port layout ----

static bool param_visible(const LV2Instance* in, uint32_t i) {
    return !(in->paramInfo[i].flags & CLAP_PARAM_IS_HIDDEN) && (int32_t)i != in->programParam;
}

static bool add_port(LV2Instance* in, PortKind kind, uint32_t index, uint32_t channel) {
    PortDef* grown = (PortDef*)realloc(in->ports, sizeof(PortDef) * (in->portCount + 1));
    if (!grown) return false;
    in->ports = grown;
    in->ports[in->portCount++] = (PortDef){ kind, index, channel };
    return true;
}

static bool read_audio_ports(LV2Instance* in, bool input) {
    uint32_t n = in->audioPorts ? in->audioPorts->count(in->plugin, input) : 0;
    clap_audio_port_info_t* infos = (clap_audio_port_info_t*)calloc(n ? n : 1, sizeof(clap_audio_port_info_t));
    if (!infos) return false;
    uint32_t channels = 0;
    for (uint32_t i = 0; i < n; i++) {
        if (!in->audioPorts->get(in->plugin, i, input, &infos[i])) {
            free(infos);
            return false;
        }
        channels += infos[i].channel_count;
    }
    if (input) {
        in->audioIn = infos;
        in->audioInCount = n;
        in->inChannels = channels;
    } else {
        in->audioOut = infos;
        in->audioOutCount = n;
        in->outChannels = channels;
    }
    return true;
}

static void destroy_instance(LV2Instance* in) {
    if (!in) return;
    in->magic = 0;
    if (in->plugin) in->plugin->destroy(in->plugin);
    free(in->paramInfo);
    free((void*)in->values);
    free(in->audioIn);
    free(in->audioOut);
    free(in->ports);
    free(in->data);
    free(in->last);
    free(in->inBuffers);
    free(in->outBuffers);
    free(in->inPointers);
    free(in->outPointers);
    free(in->inStore);
    free(in->scratch);
    free(in);
}

// create_instance creates the CLAP plugin of a descriptor and its port layout.
static LV2Instance* create_instance(uint32_t index) {
    pthread_once(&lv2Once, lv2_init_descriptors);
    if (!lv2Factory || index >= lv2DescriptorCount) return NULL;
    LV2Instance* in = (LV2Instance*)calloc(1, sizeof(LV2Instance));
    if (!in) return NULL;
    in->descriptor = lv2Factory->get_plugin_descriptor(lv2Factory, index);
    in->host = (clap_host_t){ CLAP_VERSION_INIT, in, "vst3go LV2", "vst3go", "https://github.com/th-release/vst3go", "1.0",
                              host_get_extension, host_request_restart, host_request_process, host_request_callback };
    in->magic = LV2_INSTANCE_MAGIC;
    in->programParam = -1;
    in->plugin = in->descriptor ? lv2Factory->create_plugin(lv2Factory, &in->host, in->descriptor->id) : NULL;
    if (!in->plugin || !in->plugin->init(in->plugin)) {
        destroy_instance(in);
        return NULL;
    }
#define EXT(field, type, id) in->field = (const type*)in->plugin->get_extension(in->plugin, id)
    EXT(params, clap_plugin_params_t, CLAP_EXT_PARAMS);
    EXT(audioPorts, clap_plugin_audio_ports_t, CLAP_EXT_AUDIO_PORTS);
    EXT(notePorts, clap_plugin_note_ports_t, CLAP_EXT_NOTE_PORTS);
    EXT(state, clap_plugin_state_t, CLAP_EXT_STATE);
    EXT(latency, clap_plugin_latency_t, CLAP_EXT_LATENCY);
    EXT(gui, clap_plugin_gui_t, CLAP_EXT_GUI);
    EXT(timer, clap_plugin_timer_support_t, CLAP_EXT_TIMER_SUPPORT);
#undef EXT

    if (!read_audio_ports(in, true) || !read_audio_ports(in, false)) goto fail;
    in->noteIn = in->notePorts && in->notePorts->count(in->plugin, true) > 0;
    in->noteOut = in->notePorts && in->notePorts->count(in->plugin, false) > 0;

    in->paramCount = in->params ? in->params->count(in->plugin) : 0;
    in->paramInfo = (clap_param_info_t*)calloc(in->paramCount ? in->paramCount : 1, sizeof(clap_param_info_t));
    in->values = (_Atomic double*)calloc(in->paramCount ? in->paramCount : 1, sizeof(double));
    if (!in->paramInfo || !in->values) goto fail;
    for (uint32_t i = 0; i < in->paramCount; i++) {
        if (!in->params->get_info(in->plugin, i, &in->paramInfo[i])) goto fail;
        const clap_param_info_t* p = &in->paramInfo[i];
        if (p->id == LV2_PROGRAM_PARAM_ID && (p->flags & CLAP_PARAM_IS_STEPPED)) in->programParam = (int32_t)i;
        double v = p->default_value;
        in->params->get_value(in->plugin, p->id, &v);
        atomic_store(&in->values[i], v);
    }

    for (uint32_t p = 0; p < in->audioInCount; p++) {
        for (uint32_t ch = 0; ch < in->audioIn[p].channel_count; ch++) {
            if (!add_port(in, PORT_AUDIO_IN, p, ch)) goto fail;
        }
    }
    for (uint32_t p = 0; p < in->audioOutCount; p++) {
        for (uint32_t ch = 0; ch < in->audioOut[p].channel_count; ch++) {
            if (!add_port(in, PORT_AUDIO_OUT, p, ch)) goto fail;
        }
    }
    if (!add_port(in, PORT_EVENTS_IN, 0, 0)) goto fail;
    if (in->noteOut && !add_port(in, PORT_EVENTS_OUT, 0, 0)) goto fail;
    if (!add_port(in, PORT_LATENCY, 0, 0)) goto fail;
    for (uint32_t i = 0; i < in->paramCount; i++) {
        if (!param_visible(in, i)) continue;
        PortKind kind = in->paramInfo[i].flags & CLAP_PARAM_IS_READONLY ? PORT_PARAM_OUT : PORT_PARAM_IN;
        if (!add_port(in, kind, i, 0)) goto fail;
    }
    in->data = (void**)calloc(in->portCount, sizeof(void*));
    in->last = (float*)calloc(in->portCount, sizeof(float));
    if (!in->data || !in->last) goto fail;
    atomic_store(&in->resend, true);
    return in;
fail:
    destroy_instance(in);
    return NULL;
}

static int32_t param_index(const LV2Instance* in, clap_id id) {
    for (uint32_t i = 0; i < in->paramCount; i++) {
        if (in->paramInfo[i].id == id) return (int32_t)i;
    }
    return -1;
}

// ---- LV2 plugin interface ----

static uint32_t descriptor_index(const LV2_Descriptor* d) {
    return (uint32_t)(d - lv2Descriptors);
}

static void map_urids(LV2Instance* in) {
    LV2_URID_Map* m = in->map;
#define MAP(field, uri) in->urid.field = m->map(m->handle, uri)
    MAP(atomChunk, LV2_ATOM__Chunk);
    MAP(atomSequence, LV2_ATOM__Sequence);
    MAP(atomObject, LV2_ATOM__Object);
    MAP(atomBlank, LV2_ATOM__Blank);
    MAP(atomFloat, LV2_ATOM__Float);
    MAP(atomDouble, LV2_ATOM__Double);
    MAP(atomInt, LV2_ATOM__Int);
    MAP(atomLong, LV2_ATOM__Long);
    MAP(midiEvent, LV2_MIDI__MidiEvent);
    MAP(timePosition, LV2_TIME__Position);
    MAP(timeBar, LV2_TIME__bar);
    MAP(timeBarBeat, LV2_TIME__barBeat);
    MAP(timeBeatUnit, LV2_TIME__beatUnit);
    MAP(timeBeatsPerBar, LV2_TIME__beatsPerBar);
    MAP(timeBeatsPerMinute, LV2_TIME__beatsPerMinute);
    MAP(timeSpeed, LV2_TIME__speed);
    MAP(timeFrame, LV2_TIME__frame);
    MAP(maxBlockLength, LV2_BUF_SIZE__maxBlockLength);
    MAP(nominalBlockLength, LV2_BUF_SIZE__nominalBlockLength);
    MAP(stateKey, LV2_STATE_KEY);
#undef MAP
}

static bool atom_number(const LV2Instance* in, const LV2_Atom* a, double* value) {
    if (!a) return false;
    if (a->type == in->urid.atomFloat) *value = ((const LV2_Atom_Float*)a)->body;
    else if (a->type == in->urid.atomDouble) *value = ((const LV2_Atom_Double*)a)->body;
    else if (a->type == in->urid.atomInt) *value = ((const LV2_Atom_Int*)a)->body;
    else if (a->type == in->urid.atomLong) *value = (double)((const LV2_Atom_Long*)a)->body;
    else return false;
    return true;
}

static LV2_Handle lv2_instantiate(const LV2_Descriptor* d, double rate, const char* bundle, const LV2_Feature* const* features) {
    (void)bundle;
    LV2_URID_Map* map = NULL;
    const LV2_Options_Option* options = NULL;
    const char* missing = lv2_features_query(features, LV2_URID__map, &map, true, LV2_OPTIONS__options, &options, false, NULL);
    if (missing || rate <= 0) return NULL;
    LV2Instance* in = create_instance(descriptor_index(d));
    if (!in) return NULL;
    in->map = map;
    in->rate = rate;
    map_urids(in);
    in->maxFrames = LV2_DEFAULT_MAX_FRAMES;
    for (const LV2_Options_Option* o = options; o && o->key; o++) {
        if (o->key == in->urid.maxBlockLength && o->size == sizeof(int32_t) && o->type == in->urid.atomInt) {
            int32_t n = *(const int32_t*)o->value;
            if (n > 0 && n <= 65536) in->maxFrames = (uint32_t)n;
        }
    }
    in->inBuffers = (clap_audio_buffer_t*)calloc(in->audioInCount ? in->audioInCount : 1, sizeof(clap_audio_buffer_t));
    in->outBuffers = (clap_audio_buffer_t*)calloc(in->audioOutCount ? in->audioOutCount : 1, sizeof(clap_audio_buffer_t));
    in->inPointers = (float**)calloc(in->inChannels ? in->inChannels : 1, sizeof(float*));
    in->outPointers = (float**)calloc(in->outChannels ? in->outChannels : 1, sizeof(float*));
    in->inStore = (float*)calloc((size_t)(in->inChannels ? in->inChannels : 1) * in->maxFrames, sizeof(float));
    in->scratch = (float*)calloc((size_t)(in->outChannels ? in->outChannels : 1) * in->maxFrames, sizeof(float));
    if (!in->inBuffers || !in->outBuffers || !in->inPointers || !in->outPointers || !in->inStore || !in->scratch) {
        destroy_instance(in);
        return NULL;
    }
    uint32_t k = 0;
    for (uint32_t p = 0; p < in->audioInCount; p++) {
        in->inBuffers[p] = (clap_audio_buffer_t){ in->inPointers + k, NULL, in->audioIn[p].channel_count, 0, 0 };
        k += in->audioIn[p].channel_count;
    }
    k = 0;
    for (uint32_t p = 0; p < in->audioOutCount; p++) {
        in->outBuffers[p] = (clap_audio_buffer_t){ in->outPointers + k, NULL, in->audioOut[p].channel_count, 0, 0 };
        k += in->audioOut[p].channel_count;
    }
    lv2_atom_forge_init(&in->forge, map);
    return (LV2_Handle)in;
}

static void lv2_connect_port(LV2_Handle h, uint32_t port, void* data) {
    LV2Instance* in = (LV2Instance*)h;
    if (port < in->portCount) in->data[port] = data;
}

static void lv2_activate(LV2_Handle h) {
    LV2Instance* in = (LV2Instance*)h;
    vst3go_lv2_main_thread(in);
    if (in->active) return;
    in->active = in->plugin->activate(in->plugin, in->rate, 1, in->maxFrames);
    in->latencySamples = in->active && in->latency ? in->latency->get(in->plugin) : 0;
    in->steadyTime = 0;
    in->hasPosition = false;
    atomic_store(&in->resend, true);
}

static void stop(LV2Instance* in) {
    if (in->processing) {
        in->plugin->stop_processing(in->plugin);
        in->processing = false;
    }
}

static void lv2_deactivate(LV2_Handle h) {
    LV2Instance* in = (LV2Instance*)h;
    if (!in->active) return;
    stop(in);
    in->plugin->deactivate(in->plugin);
    in->active = false;
    vst3go_lv2_main_thread(in);
}

static void lv2_cleanup(LV2_Handle h) {
    LV2Instance* in = (LV2Instance*)h;
    lv2_deactivate(h);
    destroy_instance(in);
}

// ---- events ----

static uint32_t in_size(const clap_input_events_t* list) {
    const LV2Instance* in = (const LV2Instance*)list->ctx;
    return in->chunkEnd - in->chunkFirst;
}

static const clap_event_header_t* in_get(const clap_input_events_t* list, uint32_t index) {
    LV2Instance* in = (LV2Instance*)list->ctx;
    if (index >= in->chunkEnd - in->chunkFirst) return NULL;
    // Times are relative to the chunk.
    AnyEvent* e = (AnyEvent*)in->events[in->chunkFirst + index];
    return &e->header;
}

static void push_ui_edit(LV2Instance* in, uint32_t kind, uint32_t param, double value) {
    if (!atomic_load(&in->uiOpen)) return;
    unsigned head = atomic_load(&in->uiHead);
    unsigned next = (head + 1) % VST3GO_LV2_UI_EDITS;
    if (next == atomic_load(&in->uiTail)) return;  // full: the UI is not draining
    in->uiEdits[head] = (VST3GoLV2UIEdit){ kind, param, value };
    atomic_store(&in->uiHead, next);
}

bool vst3go_lv2_ui_pop(LV2Instance* in, VST3GoLV2UIEdit* edit) {
    unsigned tail = atomic_load(&in->uiTail);
    if (tail == atomic_load(&in->uiHead)) return false;
    *edit = in->uiEdits[tail];
    atomic_store(&in->uiTail, (tail + 1) % VST3GO_LV2_UI_EDITS);
    return true;
}

static void forge_midi(LV2Instance* in, uint32_t time, const uint8_t* data, uint32_t size) {
    if (!in->forging || !size) return;
    if (!lv2_atom_forge_frame_time(&in->forge, in->chunkStart + time) ||
        !lv2_atom_forge_atom(&in->forge, size, in->urid.midiEvent) || !lv2_atom_forge_write(&in->forge, data, size)) {
        in->forging = false;  // the host buffer is full
    }
}

static bool out_try_push(const clap_output_events_t* list, const clap_event_header_t* e) {
    LV2Instance* in = (LV2Instance*)list->ctx;
    if (e->space_id != CLAP_CORE_EVENT_SPACE_ID) return true;
    switch (e->type) {
    case CLAP_EVENT_PARAM_VALUE: {
        const clap_event_param_value_t* v = (const clap_event_param_value_t*)e;
        int32_t i = param_index(in, v->param_id);
        if (i >= 0) {
            atomic_store(&in->values[i], v->value);
            if (!(in->paramInfo[i].flags & CLAP_PARAM_IS_READONLY)) push_ui_edit(in, VST3GO_LV2_UI_VALUE, (uint32_t)i, v->value);
        }
        break;
    }
    case CLAP_EVENT_PARAM_GESTURE_BEGIN:
    case CLAP_EVENT_PARAM_GESTURE_END: {
        const clap_event_param_gesture_t* g = (const clap_event_param_gesture_t*)e;
        int32_t i = param_index(in, g->param_id);
        if (i >= 0) push_ui_edit(in, e->type == CLAP_EVENT_PARAM_GESTURE_BEGIN ? VST3GO_LV2_UI_BEGIN : VST3GO_LV2_UI_END, (uint32_t)i, 0);
        break;
    }
    case CLAP_EVENT_MIDI: {
        const clap_event_midi_t* m = (const clap_event_midi_t*)e;
        uint8_t status = m->data[0] & 0xF0;
        uint32_t size = status == 0xC0 || status == 0xD0 ? 2 : 3;
        if (m->data[0] >= 0xF0) size = m->data[0] == 0xF1 || m->data[0] == 0xF3 ? 2 : m->data[0] == 0xF2 ? 3 : 1;
        forge_midi(in, e->time, m->data, size);
        break;
    }
    case CLAP_EVENT_MIDI_SYSEX: {
        const clap_event_midi_sysex_t* s = (const clap_event_midi_sysex_t*)e;
        if (s->buffer) forge_midi(in, e->time, s->buffer, s->size);
        break;
    }
    case CLAP_EVENT_NOTE_ON:
    case CLAP_EVENT_NOTE_OFF: {
        const clap_event_note_t* n = (const clap_event_note_t*)e;
        if (n->channel < 0 || n->channel > 15 || n->key < 0 || n->key > 127) break;
        int velocity = (int)(n->velocity * 127 + 0.5);
        if (velocity > 127) velocity = 127;
        if (e->type == CLAP_EVENT_NOTE_ON && velocity < 1) velocity = 1;
        uint8_t data[3] = { (uint8_t)((e->type == CLAP_EVENT_NOTE_ON ? 0x90 : 0x80) | n->channel), (uint8_t)n->key, (uint8_t)velocity };
        forge_midi(in, e->time, data, 3);
        break;
    }
    }
    return true;
}

static AnyEvent* add_event(LV2Instance* in) {
    if (in->eventCount >= LV2_MAX_EVENTS) return NULL;
    AnyEvent* e = &in->store[in->eventCount];
    in->events[in->eventCount++] = &e->header;
    return e;
}

static void read_position(LV2Instance* in, const LV2_Atom_Object* obj) {
    const LV2_Atom *bar = NULL, *barBeat = NULL, *beatUnit = NULL, *beatsPerBar = NULL, *bpm = NULL, *speed = NULL, *frame = NULL;
    lv2_atom_object_get(obj, in->urid.timeBar, &bar, in->urid.timeBarBeat, &barBeat, in->urid.timeBeatUnit, &beatUnit,
                        in->urid.timeBeatsPerBar, &beatsPerBar, in->urid.timeBeatsPerMinute, &bpm, in->urid.timeSpeed, &speed,
                        in->urid.timeFrame, &frame, 0);
    double v;
    if (!in->hasPosition) {
        in->bpm = 120;
        in->beatsPerBar = 4;
        in->beatUnit = 4;
        in->speed = 0;
        in->barBeat = 0;
        in->bar = 0;
        in->frame = 0;
        in->hasPosition = true;
    }
    if (atom_number(in, bpm, &v) && v > 0) in->bpm = v;
    if (atom_number(in, beatsPerBar, &v) && v > 0) in->beatsPerBar = v;
    if (atom_number(in, beatUnit, &v) && v > 0) in->beatUnit = v;
    if (atom_number(in, speed, &v)) in->speed = v;
    if (atom_number(in, barBeat, &v)) in->barBeat = v;
    if (atom_number(in, bar, &v)) in->bar = (int64_t)v;
    if (atom_number(in, frame, &v)) in->frame = (int64_t)v;
}

// collect_events gathers the run's events in time order: control port
// changes at frame 0, then the control sequence (MIDI; transport is read
// into the position state).
static void collect_events(LV2Instance* in, uint32_t frames) {
    in->eventCount = 0;
    bool resend = atomic_exchange(&in->resend, false);
    for (uint32_t i = 0; i < in->portCount; i++) {
        if (in->ports[i].kind != PORT_PARAM_IN || !in->data[i]) continue;
        float value = *(const float*)in->data[i];
        if (!resend && value == in->last[i]) continue;
        in->last[i] = value;
        const clap_param_info_t* p = &in->paramInfo[in->ports[i].index];
        double v = isfinite(value) ? value : p->default_value;
        if (v < p->min_value) v = p->min_value;
        if (v > p->max_value) v = p->max_value;
        if (p->flags & CLAP_PARAM_IS_STEPPED) v = round(v);
        AnyEvent* e = add_event(in);
        if (!e) break;
        e->param = (clap_event_param_value_t){ { sizeof(clap_event_param_value_t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 },
                                               p->id, p->cookie, -1, -1, -1, -1, v };
        atomic_store(&in->values[in->ports[i].index], v);
    }
    for (uint32_t i = 0; i < in->portCount; i++) {
        if (in->ports[i].kind != PORT_EVENTS_IN || !in->data[i]) continue;
        const LV2_Atom_Sequence* seq = (const LV2_Atom_Sequence*)in->data[i];
        if (seq->atom.type != in->urid.atomSequence) continue;
        LV2_ATOM_SEQUENCE_FOREACH (seq, ev) {
            uint32_t time = ev->time.frames < 0 ? 0 : ev->time.frames >= frames ? (frames ? frames - 1 : 0) : (uint32_t)ev->time.frames;
            if (ev->body.type == in->urid.midiEvent && in->noteIn && ev->body.size > 0) {
                const uint8_t* msg = (const uint8_t*)LV2_ATOM_BODY_CONST(&ev->body);
                AnyEvent* e = add_event(in);
                if (!e) break;
                if (msg[0] == 0xF0) {
                    e->sysex = (clap_event_midi_sysex_t){ { sizeof(clap_event_midi_sysex_t), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI_SYSEX, 0 },
                                                          0, msg, ev->body.size };
                } else {
                    e->midi = (clap_event_midi_t){ { sizeof(clap_event_midi_t), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0 }, 0,
                                                   { msg[0], ev->body.size > 1 ? msg[1] : 0, ev->body.size > 2 ? msg[2] : 0 } };
                }
            } else if (ev->body.type == in->urid.atomObject || ev->body.type == in->urid.atomBlank) {
                const LV2_Atom_Object* obj = (const LV2_Atom_Object*)&ev->body;
                if (obj->body.otype == in->urid.timePosition) read_position(in, obj);
            }
        }
    }
}

static const clap_event_transport_t* chunk_transport(LV2Instance* in, clap_event_transport_t* t) {
    if (!in->hasPosition) return NULL;
    memset(t, 0, sizeof(*t));
    t->header = (clap_event_header_t){ sizeof(*t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_TRANSPORT, 0 };
    t->flags = CLAP_TRANSPORT_HAS_TEMPO | CLAP_TRANSPORT_HAS_BEATS_TIMELINE | CLAP_TRANSPORT_HAS_SECONDS_TIMELINE |
               CLAP_TRANSPORT_HAS_TIME_SIGNATURE | (in->speed != 0 ? CLAP_TRANSPORT_IS_PLAYING : 0);
    // LV2 counts beats of beatUnit, CLAP quarter notes.
    double quarters = 4 / in->beatUnit;
    double barStart = (double)in->bar * in->beatsPerBar * quarters;
    t->tempo = in->bpm;
    t->song_pos_beats = (clap_beattime)llround((barStart + in->barBeat * quarters) * CLAP_BEATTIME_FACTOR);
    t->song_pos_seconds = (clap_sectime)llround((double)in->frame / in->rate * CLAP_SECTIME_FACTOR);
    t->bar_start = (clap_beattime)llround(barStart * CLAP_BEATTIME_FACTOR);
    t->bar_number = (int32_t)in->bar;
    t->tsig_num = (uint16_t)in->beatsPerBar;
    t->tsig_denom = (uint16_t)in->beatUnit;
    return t;
}

static void advance_position(LV2Instance* in, uint32_t frames) {
    if (!in->hasPosition || in->speed == 0) return;
    in->frame += (int64_t)llround(frames * in->speed);
    in->barBeat += frames / in->rate * in->bpm / 60 * in->speed;
    while (in->barBeat >= in->beatsPerBar) {
        in->barBeat -= in->beatsPerBar;
        in->bar++;
    }
    while (in->barBeat < 0 && in->bar > 0) {
        in->barBeat += in->beatsPerBar;
        in->bar--;
    }
}

static void restart(LV2Instance* in) {
    stop(in);
    in->plugin->deactivate(in->plugin);
    in->active = in->plugin->activate(in->plugin, in->rate, 1, in->maxFrames);
    in->latencySamples = in->active && in->latency ? in->latency->get(in->plugin) : 0;
}

static void lv2_run(LV2_Handle h, uint32_t frames) {
    LV2Instance* in = (LV2Instance*)h;
    lv2InAudioThread = true;
    in->forging = false;
    for (uint32_t i = 0; i < in->portCount; i++) {
        if (in->ports[i].kind == PORT_EVENTS_OUT && in->data[i]) {
            // The host sets the capacity in the atom size.
            LV2_Atom_Sequence* seq = (LV2_Atom_Sequence*)in->data[i];
            lv2_atom_forge_set_buffer(&in->forge, (uint8_t*)seq, seq->atom.size);
            in->forging = lv2_atom_forge_sequence_head(&in->forge, &in->forgeFrame, 0) != 0;
        }
    }
    if (in->active && atomic_exchange(&in->restartRequested, false)) restart(in);
    if (in->active && !in->processing) in->processing = in->plugin->start_processing(in->plugin);
    if (!in->active || !in->processing) {
        for (uint32_t i = 0; i < in->portCount; i++) {
            if (in->ports[i].kind == PORT_AUDIO_OUT && in->data[i]) memset(in->data[i], 0, sizeof(float) * frames);
        }
        if (in->forging) lv2_atom_forge_pop(&in->forge, &in->forgeFrame);
        lv2InAudioThread = false;
        return;
    }

    collect_events(in, frames);
    clap_input_events_t inEvents = { in, in_size, in_get };
    clap_output_events_t outEvents = { in, out_try_push };
    clap_event_transport_t transport;
    uint32_t next = 0;
    for (uint32_t offset = 0; offset < frames || (frames == 0 && offset == 0);) {
        uint32_t n = frames - offset < in->maxFrames ? frames - offset : in->maxFrames;
        // This chunk's events, with times relative to it.
        in->chunkStart = offset;
        in->chunkFirst = next;
        while (next < in->eventCount && in->events[next]->time < offset + n) {
            ((AnyEvent*)in->events[next])->header.time -= offset;
            next++;
        }
        in->chunkEnd = next;
        uint32_t ki = 0, ko = 0;
        for (uint32_t i = 0; i < in->portCount; i++) {
            const PortDef* port = &in->ports[i];
            if (port->kind == PORT_AUDIO_IN) {
                float* dst = in->inStore + (size_t)ki * in->maxFrames;
                if (in->data[i]) memcpy(dst, (const float*)in->data[i] + offset, sizeof(float) * n);
                else memset(dst, 0, sizeof(float) * n);
                in->inPointers[ki++] = dst;
            } else if (port->kind == PORT_AUDIO_OUT) {
                in->outPointers[ko] = in->data[i] ? (float*)in->data[i] + offset : in->scratch + (size_t)ko * in->maxFrames;
                ko++;
            }
        }
        clap_process_t process = { in->steadyTime, n, chunk_transport(in, &transport), in->inBuffers, in->outBuffers,
                                   in->audioInCount, in->audioOutCount, &inEvents, &outEvents };
        in->plugin->process(in->plugin, &process);
        in->steadyTime += n;
        advance_position(in, n);
        offset += n;
        if (frames == 0) break;
    }
    in->eventCount = 0;

    for (uint32_t i = 0; i < in->portCount; i++) {
        if (!in->data[i]) continue;
        if (in->ports[i].kind == PORT_PARAM_OUT) *(float*)in->data[i] = (float)atomic_load(&in->values[in->ports[i].index]);
        else if (in->ports[i].kind == PORT_LATENCY) *(float*)in->data[i] = (float)in->latencySamples;
    }
    if (in->forging) lv2_atom_forge_pop(&in->forge, &in->forgeFrame);
    lv2InAudioThread = false;
}

// ---- state ----

typedef struct {
    uint8_t* data;
    size_t size, capacity, read;
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

static LV2_State_Status state_save(LV2_Handle h, LV2_State_Store_Function store, LV2_State_Handle handle, uint32_t flags,
                                   const LV2_Feature* const* features) {
    (void)flags;
    (void)features;
    LV2Instance* in = (LV2Instance*)h;
    if (!in->state) return LV2_STATE_ERR_NO_FEATURE;
    vst3go_lv2_main_thread(in);
    Buffer b = { 0 };
    clap_ostream_t stream = { &b, ostream_write };
    if (!in->state->save(in->plugin, &stream)) {
        free(b.data);
        return LV2_STATE_ERR_UNKNOWN;
    }
    LV2_State_Status status = store(handle, in->urid.stateKey, b.data, b.size, in->urid.atomChunk,
                                    LV2_STATE_IS_POD | LV2_STATE_IS_PORTABLE);
    free(b.data);
    return status;
}

static LV2_State_Status state_restore(LV2_Handle h, LV2_State_Retrieve_Function retrieve, LV2_State_Handle handle, uint32_t flags,
                                      const LV2_Feature* const* features) {
    (void)flags;
    (void)features;
    LV2Instance* in = (LV2Instance*)h;
    if (!in->state) return LV2_STATE_ERR_NO_FEATURE;
    size_t size = 0;
    uint32_t type = 0, valueFlags = 0;
    const void* data = retrieve(handle, in->urid.stateKey, &size, &type, &valueFlags);
    if (!data) return LV2_STATE_ERR_NO_PROPERTY;
    if (type != in->urid.atomChunk) return LV2_STATE_ERR_BAD_TYPE;
    Buffer b = { (uint8_t*)data, size, size, 0 };
    clap_istream_t stream = { &b, istream_read };
    if (!in->state->load(in->plugin, &stream)) return LV2_STATE_ERR_UNKNOWN;
    for (uint32_t i = 0; i < in->paramCount; i++) {
        double v;
        if (in->params->get_value(in->plugin, in->paramInfo[i].id, &v)) atomic_store(&in->values[i], v);
    }
    // The host restores the control ports with the state; they apply on
    // the next block.
    atomic_store(&in->resend, true);
    vst3go_lv2_main_thread(in);
    return LV2_STATE_SUCCESS;
}

static const LV2_State_Interface stateInterface = { state_save, state_restore };

static const void* lv2_extension_data(const char* uri) {
    if (!strcmp(uri, LV2_STATE__interface)) return &stateInterface;
    return NULL;
}

// ---- accessors for the UI ----

const clap_plugin_t* vst3go_lv2_plugin(LV2Instance* in) { return in->plugin; }
const clap_plugin_gui_t* vst3go_lv2_gui(LV2Instance* in) { return in->gui; }
const clap_plugin_timer_support_t* vst3go_lv2_timer(LV2Instance* in) { return in->timer; }
void* vst3go_lv2_ui(LV2Instance* in) { return in->ui; }
void vst3go_lv2_set_ui(LV2Instance* in, void* ui) {
    in->ui = ui;
    if (!ui) atomic_store(&in->uiTail, atomic_load(&in->uiHead));
    atomic_store(&in->uiOpen, ui != NULL);
}

bool vst3go_lv2_is_instance(const void* handle) {
    return handle && ((const LV2Instance*)handle)->magic == LV2_INSTANCE_MAGIC;
}

const char* vst3go_lv2_uri(uint32_t index) {
    const LV2_Descriptor* d = lv2_descriptor(index);
    return d ? d->URI : NULL;
}

const clap_plugin_params_t* vst3go_lv2_params(LV2Instance* in) { return in->params; }
const clap_param_info_t* vst3go_lv2_param_info(LV2Instance* in, uint32_t param) { return param < in->paramCount ? &in->paramInfo[param] : NULL; }
uint32_t vst3go_lv2_param_count(LV2Instance* in) { return in->paramCount; }
bool vst3go_lv2_take_rescan(LV2Instance* in) { return atomic_exchange(&in->rescanValues, false); }

int32_t vst3go_lv2_param_port(LV2Instance* in, uint32_t param) {
    for (uint32_t i = 0; i < in->portCount; i++) {
        if ((in->ports[i].kind == PORT_PARAM_IN || in->ports[i].kind == PORT_PARAM_OUT) && in->ports[i].index == param) return (int32_t)i;
    }
    return -1;
}

// ---- Turtle ----

typedef struct {
    char* data;
    size_t size, capacity;
    bool failed;
} Text;

static void text_printf(Text* t, const char* format, ...) __attribute__((format(printf, 2, 3)));
static void text_printf(Text* t, const char* format, ...) {
    if (t->failed) return;
    for (;;) {
        va_list args;
        va_start(args, format);
        int n = vsnprintf(t->data ? t->data + t->size : NULL, t->data ? t->capacity - t->size : 0, format, args);
        va_end(args);
        if (n < 0) {
            t->failed = true;
            return;
        }
        if (t->data && t->size + (size_t)n < t->capacity) {
            t->size += (size_t)n;
            return;
        }
        size_t capacity = t->capacity ? t->capacity : 16384;
        while (capacity <= t->size + (size_t)n) capacity *= 2;
        char* grown = (char*)realloc(t->data, capacity);
        if (!grown) {
            t->failed = true;
            return;
        }
        t->data = grown;
        t->capacity = capacity;
    }
}

// text_string writes a Turtle string literal.
static void text_string(Text* t, const char* s) {
    text_printf(t, "\"");
    for (; s && *s; s++) {
        switch (*s) {
        case '"': text_printf(t, "\\\""); break;
        case '\\': text_printf(t, "\\\\"); break;
        case '\n': text_printf(t, "\\n"); break;
        case '\r': text_printf(t, "\\r"); break;
        case '\t': text_printf(t, "\\t"); break;
        default:
            if ((unsigned char)*s >= 0x20) text_printf(t, "%c", *s);
        }
    }
    text_printf(t, "\"");
}

// text_decimal writes a number as a Turtle decimal or double.
static void text_decimal(Text* t, double v) {
    // Plain values converted from normalized ones carry rounding noise.
    if (!isfinite(v) || fabs(v) < 1e-12) v = 0;
    char buf[64];
    snprintf(buf, sizeof(buf), "%.9g", v);
    if (!strpbrk(buf, ".e")) strcat(buf, ".0");
    text_printf(t, "%s", buf);
}

static bool write_file(const char* dir, const char* name, const Text* t) {
    if (t->failed) return false;
    size_t n = strlen(dir) + strlen(name) + 2;
    char* path = (char*)malloc(n);
    if (!path) return false;
    snprintf(path, n, "%s/%s", dir, name);
    FILE* f = fopen(path, "wb");
    free(path);
    if (!f) return false;
    bool ok = fwrite(t->data ? t->data : "", 1, t->size, f) == t->size;
    return fclose(f) == 0 && ok;
}

static const char* turtlePrefixes =
    "@prefix atom: <http://lv2plug.in/ns/ext/atom#> .\n"
    "@prefix doap: <http://usefulinc.com/ns/doap#> .\n"
    "@prefix foaf: <http://xmlns.com/foaf/0.1/> .\n"
    "@prefix lv2: <http://lv2plug.in/ns/lv2core#> .\n"
    "@prefix midi: <http://lv2plug.in/ns/ext/midi#> .\n"
    "@prefix opts: <http://lv2plug.in/ns/ext/options#> .\n"
    "@prefix pprop: <http://lv2plug.in/ns/ext/port-props#> .\n"
    "@prefix pset: <http://lv2plug.in/ns/ext/presets#> .\n"
    "@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .\n"
    "@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .\n"
    "@prefix state: <http://lv2plug.in/ns/ext/state#> .\n"
    "@prefix time: <http://lv2plug.in/ns/ext/time#> .\n"
    "@prefix ui: <http://lv2plug.in/ns/extensions/ui#> .\n"
    "@prefix units: <http://lv2plug.in/ns/extensions/units#> .\n"
    "@prefix urid: <http://lv2plug.in/ns/ext/urid#> .\n"
    "@prefix bufsz: <http://lv2plug.in/ns/ext/buf-size#> .\n\n";

static bool has_feature(const clap_plugin_descriptor_t* d, const char* feature) {
    for (const char* const* f = d->features; f && *f; f++) {
        if (!strcmp(*f, feature)) return true;
    }
    return false;
}

// plugin_class maps CLAP features to the most specific LV2 plugin class.
static const char* plugin_class(const LV2Instance* in) {
    static const struct {
        const char* feature;
        const char* lv2class;
    } classes[] = {
        { CLAP_PLUGIN_FEATURE_INSTRUMENT, "lv2:InstrumentPlugin" },
        { CLAP_PLUGIN_FEATURE_NOTE_EFFECT, "lv2:MIDIPlugin" },
        { CLAP_PLUGIN_FEATURE_ANALYZER, "lv2:AnalyserPlugin" },
        { CLAP_PLUGIN_FEATURE_REVERB, "lv2:ReverbPlugin" },
        { CLAP_PLUGIN_FEATURE_DELAY, "lv2:DelayPlugin" },
        { CLAP_PLUGIN_FEATURE_CHORUS, "lv2:ChorusPlugin" },
        { CLAP_PLUGIN_FEATURE_FLANGER, "lv2:FlangerPlugin" },
        { CLAP_PLUGIN_FEATURE_PHASER, "lv2:PhaserPlugin" },
        { CLAP_PLUGIN_FEATURE_EQUALIZER, "lv2:EQPlugin" },
        { CLAP_PLUGIN_FEATURE_LIMITER, "lv2:LimiterPlugin" },
        { CLAP_PLUGIN_FEATURE_COMPRESSOR, "lv2:CompressorPlugin" },
        { CLAP_PLUGIN_FEATURE_EXPANDER, "lv2:ExpanderPlugin" },
        { CLAP_PLUGIN_FEATURE_GATE, "lv2:GatePlugin" },
        { CLAP_PLUGIN_FEATURE_DISTORTION, "lv2:DistortionPlugin" },
        { CLAP_PLUGIN_FEATURE_FILTER, "lv2:FilterPlugin" },
        { CLAP_PLUGIN_FEATURE_PITCH_SHIFTER, "lv2:PitchPlugin" },
        { CLAP_PLUGIN_FEATURE_MIXING, "lv2:MixerPlugin" },
        { CLAP_PLUGIN_FEATURE_UTILITY, "lv2:UtilityPlugin" },
    };
    for (size_t i = 0; i < sizeof(classes) / sizeof(classes[0]); i++) {
        if (has_feature(in->descriptor, classes[i].feature)) return classes[i].lv2class;
    }
    return NULL;
}

static bool ends_with(const char* s, const char* suffix) {
    size_t n = strlen(s), m = strlen(suffix);
    return n >= m && !strcmp(s + n - m, suffix);
}

// param_unit derives the LV2 unit from the unit the plugin appends to
// its value text.
static const char* param_unit(LV2Instance* in, const clap_param_info_t* p) {
    char text[256] = "";
    if (!in->params->value_to_text(in->plugin, p->id, p->default_value, text, sizeof(text))) return NULL;
    static const struct {
        const char* suffix;
        const char* unit;
    } units[] = {
        { " dB", "units:db" }, { " kHz", "units:khz" }, { " Hz", "units:hz" }, { " ms", "units:ms" },
        { " s", "units:s" }, { " %", "units:pc" }, { "%", "units:pc" }, { " st", "units:semitone12TET" },
        { " semitones", "units:semitone12TET" }, { " ct", "units:cent" }, { " cents", "units:cent" },
        { " bpm", "units:bpm" }, { " BPM", "units:bpm" }, { " oct", "units:oct" },
    };
    for (size_t i = 0; i < sizeof(units) / sizeof(units[0]); i++) {
        if (ends_with(text, units[i].suffix)) return units[i].unit;
    }
    return NULL;
}

static void audio_port_name(const clap_audio_port_info_t* info, uint32_t channel, char* out, size_t size) {
    static const char* stereo[] = { "Left", "Right" };
    if (info->channel_count == 1) snprintf(out, size, "%s", info->name);
    else if (info->channel_count == 2) snprintf(out, size, "%s %s", info->name, stereo[channel]);
    else snprintf(out, size, "%s %u", info->name, channel + 1);
}

static void write_ports(Text* t, LV2Instance* in) {
    for (uint32_t i = 0; i < in->portCount; i++) {
        const PortDef* port = &in->ports[i];
        text_printf(t, i ? " , [\n" : "  lv2:port [\n");
        char name[512];
        switch (port->kind) {
        case PORT_AUDIO_IN:
        case PORT_AUDIO_OUT: {
            bool input = port->kind == PORT_AUDIO_IN;
            const clap_audio_port_info_t* info = input ? &in->audioIn[port->index] : &in->audioOut[port->index];
            bool main = port->index == 0 && (info->flags & CLAP_AUDIO_PORT_IS_MAIN);
            text_printf(t, "    a lv2:%s, lv2:AudioPort ;\n", input ? "InputPort" : "OutputPort");
            text_printf(t, "    lv2:index %u ;\n    lv2:symbol \"%s_%u_%u\" ;\n", i, input ? "in" : "out", port->index + 1, port->channel + 1);
            audio_port_name(info, port->channel, name, sizeof(name));
            text_printf(t, "    lv2:name ");
            text_string(t, name);
            if (!main) {
                text_printf(t, " ;\n    lv2:portProperty lv2:connectionOptional");
                if (input) text_printf(t, ", lv2:isSideChain");
            }
            text_printf(t, "\n  ]");
            break;
        }
        case PORT_EVENTS_IN:
            text_printf(t, "    a lv2:InputPort, atom:AtomPort ;\n    atom:bufferType atom:Sequence ;\n");
            text_printf(t, "    atom:supports time:Position%s ;\n", in->noteIn ? ", midi:MidiEvent" : "");
            text_printf(t, "    lv2:designation lv2:control ;\n    lv2:index %u ;\n    lv2:symbol \"control\" ;\n    lv2:name \"%s\"\n  ]",
                        i, in->noteIn ? "MIDI In" : "Control");
            break;
        case PORT_EVENTS_OUT:
            text_printf(t, "    a lv2:OutputPort, atom:AtomPort ;\n    atom:bufferType atom:Sequence ;\n    atom:supports midi:MidiEvent ;\n");
            text_printf(t, "    lv2:index %u ;\n    lv2:symbol \"midi_out\" ;\n    lv2:name \"MIDI Out\"\n  ]", i);
            break;
        case PORT_LATENCY:
            text_printf(t, "    a lv2:OutputPort, lv2:ControlPort ;\n    lv2:index %u ;\n    lv2:symbol \"latency\" ;\n    lv2:name \"Latency\" ;\n", i);
            text_printf(t, "    lv2:designation lv2:latency ;\n    lv2:portProperty lv2:reportsLatency, lv2:integer, pprop:notOnGUI ;\n");
            text_printf(t, "    lv2:minimum 0 ;\n    lv2:maximum %d ;\n    units:unit units:frame\n  ]", 10 * 192000);
            break;
        case PORT_PARAM_IN:
        case PORT_PARAM_OUT: {
            const clap_param_info_t* p = &in->paramInfo[port->index];
            bool stepped = p->flags & CLAP_PARAM_IS_STEPPED;
            text_printf(t, "    a lv2:%s, lv2:ControlPort ;\n", port->kind == PORT_PARAM_IN ? "InputPort" : "OutputPort");
            text_printf(t, "    lv2:index %u ;\n    lv2:symbol \"param_%u\" ;\n    lv2:name ", i, p->id);
            text_string(t, p->name);
            text_printf(t, " ;\n    lv2:default ");
            text_decimal(t, p->default_value);
            text_printf(t, " ;\n    lv2:minimum ");
            text_decimal(t, p->min_value);
            text_printf(t, " ;\n    lv2:maximum ");
            text_decimal(t, p->max_value);
            int props = 0;
            if (stepped) {
                text_printf(t, " ;\n    lv2:portProperty lv2:integer");
                props++;
                bool toggle = (p->flags & CLAP_PARAM_IS_BYPASS) || (p->min_value == 0 && p->max_value == 1);
                text_printf(t, toggle ? ", lv2:toggled" : (p->flags & CLAP_PARAM_IS_ENUM) ? ", lv2:enumeration" : "");
            }
            if (port->kind == PORT_PARAM_IN && !(p->flags & CLAP_PARAM_IS_AUTOMATABLE)) {
                text_printf(t, props ? ", pprop:notAutomatic" : " ;\n    lv2:portProperty pprop:notAutomatic");
            }
            const char* unit = param_unit(in, p);
            if (unit) text_printf(t, " ;\n    units:unit %s", unit);
            if (stepped && (p->flags & CLAP_PARAM_IS_ENUM) && p->max_value - p->min_value <= 256) {
                for (double v = p->min_value; v <= p->max_value; v += 1) {
                    char label[256] = "";
                    if (!in->params->value_to_text(in->plugin, p->id, v, label, sizeof(label))) snprintf(label, sizeof(label), "%g", v);
                    text_printf(t, " ;\n    lv2:scalePoint [ rdfs:label ");
                    text_string(t, label);
                    text_printf(t, " ; rdf:value ");
                    text_decimal(t, v);
                    text_printf(t, " ]");
                }
            }
            text_printf(t, "\n  ]");
            break;
        }
        }
    }
    text_printf(t, " ");
}

static void plugin_uri(Text* t, const LV2Instance* in) {
    text_printf(t, "<%s%s>", LV2_URI_PREFIX, in->descriptor->id);
}

static void ui_uri(Text* t, const LV2Instance* in) {
    text_printf(t, "<%s%s:ui>", LV2_URI_PREFIX, in->descriptor->id);
}

static void write_plugin(Text* t, LV2Instance* in, const char* binary) {
    const clap_plugin_descriptor_t* d = in->descriptor;
    const char* cls = plugin_class(in);
    plugin_uri(t, in);
    text_printf(t, "\n  a lv2:Plugin%s%s ;\n  doap:name ", cls ? ", " : "", cls ? cls : "");
    text_string(t, d->name);
    text_printf(t, " ;\n");
    bool project = d->vendor && *d->vendor;
    if (project) {
        text_printf(t, "  lv2:project <%s%s:project> ;\n", LV2_URI_PREFIX, d->id);
    }
    if (d->description && *d->description) {
        text_printf(t, "  rdfs:comment ");
        text_string(t, d->description);
        text_printf(t, " ;\n");
    }
    unsigned major = 0, minor = 0, micro = 0;
    if (d->version) sscanf(d->version, "%u.%u.%u", &major, &minor, &micro);
    text_printf(t, "  lv2:minorVersion %u ;\n  lv2:microVersion %u ;\n", minor, micro);
    text_printf(t, "  lv2:binary <%s> ;\n", binary);
    text_printf(t, "  lv2:requiredFeature urid:map ;\n  lv2:optionalFeature opts:options ;\n");
    text_printf(t, "  opts:supportedOption bufsz:maxBlockLength ;\n  lv2:extensionData state:interface ;\n");
    if (vst3go_lv2_ui_supported(in)) {
        text_printf(t, "  ui:ui ");
        ui_uri(t, in);
        text_printf(t, " ;\n");
    }
    write_ports(t, in);
    text_printf(t, ".\n\n");
    if (project) {
        // The vendor maintains the plugin's project.
        text_printf(t, "<%s%s:project>\n  a doap:Project ;\n  doap:name ", LV2_URI_PREFIX, d->id);
        text_string(t, d->name);
        text_printf(t, " ;\n  doap:maintainer [\n    foaf:name ");
        text_string(t, d->vendor);
        if (d->url && (!strncmp(d->url, "http://", 7) || !strncmp(d->url, "https://", 8)) && !strpbrk(d->url, "<>\" {}|\\^`")) {
            text_printf(t, " ;\n    foaf:homepage <%s>", d->url);
        }
        char* email = GoLV2Email();
        if (email && strchr(email, '@') && !strpbrk(email, "<>\" {}|\\^`:")) text_printf(t, " ;\n    foaf:mbox <mailto:%s>", email);
        free(email);
        text_printf(t, "\n  ] .\n\n");
    }
}

static void write_ui(Text* t, LV2Instance* in, const char* binary) {
    if (!vst3go_lv2_ui_supported(in)) return;
    ui_uri(t, in);
    text_printf(t, "\n  a ui:X11UI ;\n  ui:binary <%s> ;\n", binary);
    text_printf(t, "  lv2:requiredFeature <http://lv2plug.in/ns/ext/instance-access>, ui:parent ;\n");
    text_printf(t, "  lv2:optionalFeature ui:resize, ui:touch ;\n  lv2:extensionData ui:idleInterface, ui:resize .\n\n");
}

// write_presets writes each factory program as a preset of the visible
// parameter values.
static bool write_presets(Text* t, Text* manifest, LV2Instance* in) {
    if (in->programParam < 0) return true;
    const clap_param_info_t* program = &in->paramInfo[in->programParam];
    for (double v = program->min_value; v <= program->max_value && v - program->min_value < 1024; v += 1) {
        in->eventCount = 0;
        AnyEvent* e = add_event(in);
        e->param = (clap_event_param_value_t){ { sizeof(clap_event_param_value_t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 },
                                               program->id, program->cookie, -1, -1, -1, -1, v };
        in->chunkFirst = 0;
        in->chunkEnd = in->eventCount;
        clap_input_events_t inEvents = { in, in_size, in_get };
        clap_output_events_t outEvents = { in, out_try_push };
        in->params->flush(in->plugin, &inEvents, &outEvents);
        in->eventCount = 0;

        char label[256] = "";
        if (!in->params->value_to_text(in->plugin, program->id, v, label, sizeof(label))) snprintf(label, sizeof(label), "Program %g", v + 1);
        unsigned number = (unsigned)(v - program->min_value) + 1;
        for (int pass = 0; pass < 2; pass++) {
            Text* out = pass ? manifest : t;
            text_printf(out, "<%s%s:preset:%u>\n  a pset:Preset ;\n  lv2:appliesTo ", LV2_URI_PREFIX, in->descriptor->id, number);
            plugin_uri(out, in);
            text_printf(out, " ;\n  rdfs:label ");
            text_string(out, label);
            if (pass) {
                text_printf(out, " ;\n  rdfs:seeAlso <presets.ttl> .\n\n");
                continue;
            }
            bool first = true;
            for (uint32_t i = 0; i < in->portCount; i++) {
                if (in->ports[i].kind != PORT_PARAM_IN) continue;
                const clap_param_info_t* p = &in->paramInfo[in->ports[i].index];
                double value = p->default_value;
                in->params->get_value(in->plugin, p->id, &value);
                text_printf(out, first ? " ;\n  lv2:port [\n" : " , [\n");
                text_printf(out, "    lv2:symbol \"param_%u\" ;\n    pset:value ", p->id);
                text_decimal(out, value);
                text_printf(out, "\n  ]");
                first = false;
            }
            text_printf(out, " .\n\n");
        }
    }
    return !t->failed && !manifest->failed;
}

// iri_escape percent-encodes a file name for a relative IRI.
static char* iri_escape(const char* s) {
    char* out = (char*)malloc(strlen(s) * 3 + 1);
    if (!out) return NULL;
    char* o = out;
    for (const unsigned char* c = (const unsigned char*)s; *c; c++) {
        if ((*c >= 'a' && *c <= 'z') || (*c >= 'A' && *c <= 'Z') || (*c >= '0' && *c <= '9') || strchr("-._~", *c)) {
            *o++ = (char)*c;
        } else {
            o += sprintf(o, "%%%02X", *c);
        }
    }
    *o = 0;
    return out;
}

const char* VST3GoLV2WriteBundle(const char* dir, const char* binaryName) {
    pthread_once(&lv2Once, lv2_init_descriptors);
    if (!lv2DescriptorCount) return "no plugins registered";
    char* binary = iri_escape(binaryName);
    if (!binary) return "out of memory";
    Text manifest = { 0 }, plugins = { 0 }, presets = { 0 };
    const char* err = NULL;
    text_printf(&manifest, "%s", turtlePrefixes);
    text_printf(&plugins, "%s", turtlePrefixes);
    text_printf(&presets, "%s", turtlePrefixes);
    bool anyPresets = false;
    for (uint32_t i = 0; i < lv2DescriptorCount && !err; i++) {
        LV2Instance* in = create_instance(i);
        if (!in) {
            err = "a plugin failed to initialize";
            break;
        }
        plugin_uri(&manifest, in);
        text_printf(&manifest, "\n  a lv2:Plugin ;\n  lv2:binary <%s> ;\n  rdfs:seeAlso <plugins.ttl> .\n\n", binary);
        if (vst3go_lv2_ui_supported(in)) {
            ui_uri(&manifest, in);
            text_printf(&manifest, "\n  a ui:X11UI ;\n  ui:binary <%s> ;\n  rdfs:seeAlso <plugins.ttl> .\n\n", binary);
        }
        write_plugin(&plugins, in, binary);
        write_ui(&plugins, in, binary);
        anyPresets = anyPresets || in->programParam >= 0;
        if (!write_presets(&presets, &manifest, in)) err = "writing the presets failed";
        destroy_instance(in);
    }
    if (!err && !(write_file(dir, "manifest.ttl", &manifest) && write_file(dir, "plugins.ttl", &plugins) &&
                  (!anyPresets || write_file(dir, "presets.ttl", &presets)))) {
        err = "writing the Turtle files failed";
    }
    free(manifest.data);
    free(plugins.data);
    free(presets.data);
    free(binary);
    return err;
}
