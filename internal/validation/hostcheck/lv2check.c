// lv2check loads a validation plugin's LV2 bundle with lilv, the way LV2
// hosts do, and checks what the generic validators cannot: the generated
// ports and their properties, sample-accurate audio through the control
// ports, MIDI in and out through the atom ports, state round trips,
// factory programs as presets, latency reporting and side chains.
//
// Built with -DLV2CHECK_UI (and X11/XTest), "lv2check --ui [--custom]
// <bundle.lv2>" opens the X11 UI of a webkitgtk build in an X window the
// way LV2 hosts do (instance-access, ui:parent, ui:resize, ui:touch, the
// idle interface): keyboard input in the page must reach the host as a
// control port write with touch gestures, and the UI must resize and
// close cleanly. --custom expects the synth's page, which asks for 720x420.
// Run it under an X server (e.g. xvfb-run).
//
// usage: lv2check gain|synth|midifx|ducker <bundle.lv2>
//        lv2check --ui [--custom] <bundle.lv2>
#include <lilv/lilv.h>
#include <lv2/atom/forge.h>
#include <lv2/atom/util.h>
#include <lv2/buf-size/buf-size.h>
#include <lv2/core/lv2.h>
#include <lv2/midi/midi.h>
#include <lv2/options/options.h>
#include <lv2/port-props/port-props.h>
#include <lv2/presets/presets.h>
#include <lv2/units/units.h>
#include <lv2/urid/urid.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)
#define N 256
#define RATE 48000

// ---- URID map ----

static char* uris[1024];
static uint32_t uriCount;

static LV2_URID map_uri(LV2_URID_Map_Handle h, const char* uri) {
    (void)h;
    for (uint32_t i = 0; i < uriCount; i++) {
        if (!strcmp(uris[i], uri)) return i + 1;
    }
    CHECK(uriCount < 1024, "too many URIs");
    uris[uriCount] = strdup(uri);
    return ++uriCount;
}

static const char* unmap_uri(LV2_URID_Unmap_Handle h, LV2_URID urid) {
    (void)h;
    return urid >= 1 && urid <= uriCount ? uris[urid - 1] : NULL;
}

static LV2_URID_Map map = { NULL, map_uri };
static LV2_URID_Unmap unmap = { NULL, unmap_uri };

// ---- plugin ----

typedef struct {
    LilvWorld* world;
    const LilvPlugin* plugin;
    LilvInstance* instance;
    float* controls;  // per port
    uint8_t eventsIn[8192];
    uint8_t eventsOut[8192];
    LV2_Atom_Forge forge;
    LV2_Atom_Forge_Frame frame;
} Plugin;

static LilvNode* uri(Plugin* p, const char* s) { return lilv_new_uri(p->world, s); }

static Plugin load(const char* bundle) {
    Plugin p = { 0 };
    p.world = lilv_world_new();
    char path[4096];
    snprintf(path, sizeof(path), "%s/", bundle);
    LilvNode* bundleURI = lilv_new_file_uri(p.world, NULL, path);
    lilv_world_load_specifications(p.world);
    lilv_world_load_plugin_classes(p.world);
    lilv_world_load_bundle(p.world, bundleURI);
    const LilvPlugins* plugins = lilv_world_get_all_plugins(p.world);
    LILV_FOREACH (plugins, i, plugins) {
        const LilvPlugin* candidate = lilv_plugins_get(plugins, i);
        if (lilv_node_equals(lilv_plugin_get_bundle_uri(candidate), bundleURI)) p.plugin = candidate;
    }
    CHECK(p.plugin, "no plugin in %s", bundle);
    CHECK(lilv_plugin_verify(p.plugin), "lilv_plugin_verify");
    lilv_node_free(bundleURI);
    return p;
}

static void instantiate(Plugin* p) {
    static int32_t maxBlock = N;
    LV2_Options_Option options[] = {
        { LV2_OPTIONS_INSTANCE, 0, map_uri(NULL, LV2_BUF_SIZE__maxBlockLength), sizeof(int32_t), map_uri(NULL, LV2_ATOM__Int), &maxBlock },
        { LV2_OPTIONS_INSTANCE, 0, 0, 0, 0, NULL },
    };
    LV2_Feature mapFeature = { LV2_URID__map, &map }, unmapFeature = { LV2_URID__unmap, &unmap };
    LV2_Feature optionsFeature = { LV2_OPTIONS__options, options };
    const LV2_Feature* features[] = { &mapFeature, &unmapFeature, &optionsFeature, NULL };
    p->instance = lilv_plugin_instantiate(p->plugin, RATE, features);
    CHECK(p->instance, "instantiate");
    uint32_t ports = lilv_plugin_get_num_ports(p->plugin);
    p->controls = (float*)calloc(ports, sizeof(float));
    float* defaults = (float*)calloc(ports, sizeof(float));
    lilv_plugin_get_port_ranges_float(p->plugin, NULL, NULL, defaults);
    LilvNode* control = uri(p, LV2_CORE__ControlPort);
    LilvNode* atom = uri(p, LV2_ATOM__AtomPort);
    LilvNode* input = uri(p, LV2_CORE__InputPort);
    for (uint32_t i = 0; i < ports; i++) {
        const LilvPort* port = lilv_plugin_get_port_by_index(p->plugin, i);
        if (lilv_port_is_a(p->plugin, port, control)) {
            p->controls[i] = isnan(defaults[i]) ? 0 : defaults[i];
            lilv_instance_connect_port(p->instance, i, &p->controls[i]);
        } else if (lilv_port_is_a(p->plugin, port, atom)) {
            lilv_instance_connect_port(p->instance, i, lilv_port_is_a(p->plugin, port, input) ? (void*)p->eventsIn : (void*)p->eventsOut);
        }
    }
    free(defaults);
    lilv_node_free(control);
    lilv_node_free(atom);
    lilv_node_free(input);
    lv2_atom_forge_init(&p->forge, &map);
}

static uint32_t port_index(Plugin* p, const char* symbol) {
    LilvNode* s = lilv_new_string(p->world, symbol);
    const LilvPort* port = lilv_plugin_get_port_by_symbol(p->plugin, s);
    lilv_node_free(s);
    CHECK(port, "no port %s", symbol);
    return lilv_port_get_index(p->plugin, port);
}

// param_port finds the control port of the parameter with this name.
static uint32_t param_port(Plugin* p, const char* name) {
    for (uint32_t i = 0; i < lilv_plugin_get_num_ports(p->plugin); i++) {
        const LilvPort* port = lilv_plugin_get_port_by_index(p->plugin, i);
        LilvNode* n = lilv_port_get_name(p->plugin, port);
        bool match = n && !strcmp(lilv_node_as_string(n), name);
        lilv_node_free(n);
        if (match) return i;
    }
    CHECK(0, "no port named %s", name);
    return 0;
}

static bool port_has(Plugin* p, uint32_t index, const char* property) {
    LilvNode* prop = uri(p, property);
    bool has = lilv_port_has_property(p->plugin, lilv_plugin_get_port_by_index(p->plugin, index), prop);
    lilv_node_free(prop);
    return has;
}

static const char* port_unit(Plugin* p, uint32_t index) {
    LilvNode* unitPred = uri(p, LV2_UNITS__unit);
    LilvNodes* units = lilv_port_get_value(p->plugin, lilv_plugin_get_port_by_index(p->plugin, index), unitPred);
    static char unit[256];
    unit[0] = 0;
    if (units && lilv_nodes_size(units)) snprintf(unit, sizeof(unit), "%s", lilv_node_as_uri(lilv_nodes_get_first(units)));
    lilv_nodes_free(units);
    lilv_node_free(unitPred);
    return unit;
}

static void range(Plugin* p, uint32_t index, float* def, float* min, float* max) {
    LilvNode *d = NULL, *lo = NULL, *hi = NULL;
    lilv_port_get_range(p->plugin, lilv_plugin_get_port_by_index(p->plugin, index), &d, &lo, &hi);
    CHECK(d && lo && hi, "port %u range", index);
    *def = lilv_node_as_float(d);
    *min = lilv_node_as_float(lo);
    *max = lilv_node_as_float(hi);
    lilv_node_free(d);
    lilv_node_free(lo);
    lilv_node_free(hi);
}

// ---- atom sequences ----

static void events_begin(Plugin* p) {
    lv2_atom_forge_set_buffer(&p->forge, p->eventsIn, sizeof(p->eventsIn));
    lv2_atom_forge_sequence_head(&p->forge, &p->frame, 0);
    LV2_Atom* out = (LV2_Atom*)p->eventsOut;
    out->size = sizeof(p->eventsOut) - sizeof(LV2_Atom);
    out->type = map_uri(NULL, LV2_ATOM__Chunk);
}

static void events_midi(Plugin* p, int64_t time, const uint8_t* msg, uint32_t size) {
    lv2_atom_forge_frame_time(&p->forge, time);
    lv2_atom_forge_atom(&p->forge, size, map_uri(NULL, LV2_MIDI__MidiEvent));
    lv2_atom_forge_write(&p->forge, msg, size);
}

static void events_end(Plugin* p) { lv2_atom_forge_pop(&p->forge, &p->frame); }

// ---- state ----

static const void* get_port_value(const char* symbol, void* user, uint32_t* size, uint32_t* type) {
    Plugin* p = (Plugin*)user;
    *size = sizeof(float);
    *type = map_uri(NULL, LV2_ATOM__Float);
    return &p->controls[port_index(p, symbol)];
}

static void set_port_value(const char* symbol, void* user, const void* value, uint32_t size, uint32_t type) {
    Plugin* p = (Plugin*)user;
    CHECK(size == sizeof(float) && type == map_uri(NULL, LV2_ATOM__Float), "preset value type");
    p->controls[port_index(p, symbol)] = *(const float*)value;
}

static LilvState* save_state(Plugin* p) {
    LilvState* st = lilv_state_new_from_instance(p->plugin, p->instance, &map, NULL, NULL, NULL, NULL, get_port_value, p, 0, NULL);
    CHECK(st, "state save");
    // Through Turtle, as hosts store it.
    char* text = lilv_state_to_string(p->world, &map, &unmap, st, "urn:lv2check:state", NULL);
    CHECK(text && strstr(text, "urn:vst3go:state"), "state Turtle carries the plugin state");
    lilv_state_free(st);
    st = lilv_state_new_from_string(p->world, &map, text);
    CHECK(st, "state parse");
    lilv_free(text);
    return st;
}

static bool plugin_is_a(Plugin* p, const char* cls) {
    LilvNode* type = uri(p, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type");
    LilvNode* c = uri(p, cls);
    bool is = lilv_world_ask(p->world, lilv_plugin_get_uri(p->plugin), type, c);
    lilv_node_free(type);
    lilv_node_free(c);
    return is;
}

static void check_common(Plugin* p) {
    uint32_t latency = port_index(p, "latency");
    CHECK(port_has(p, latency, LV2_CORE__reportsLatency), "latency port reports latency");
    uint32_t control = port_index(p, "control");
    LilvNode* designation = uri(p, LV2_CORE__designation);
    LilvNode* controlDesignation = uri(p, LV2_CORE__control);
    LilvNodes* d = lilv_port_get_value(p->plugin, lilv_plugin_get_port_by_index(p->plugin, control), designation);
    CHECK(d && lilv_node_equals(lilv_nodes_get_first(d), controlDesignation), "control port designation");
    lilv_nodes_free(d);
    lilv_node_free(designation);
    lilv_node_free(controlDesignation);
    LilvNode* stateIface = uri(p, "http://lv2plug.in/ns/ext/state#interface");
    CHECK(lilv_plugin_has_extension_data(p->plugin, stateIface), "state:interface");
    lilv_node_free(stateIface);
}

// ---- tests ----

static void test_gain(const char* bundle) {
    Plugin p = load(bundle);
    check_common(&p);
    CHECK(plugin_is_a(&p, LV2_CORE__UtilityPlugin), "gain is a utility plugin");
    CHECK(lilv_plugin_get_num_ports(p.plugin) == 8, "gain has 8 ports, got %u", lilv_plugin_get_num_ports(p.plugin));
    uint32_t gain = param_port(&p, "Gain"), bypass = param_port(&p, "Bypass");
    float def, min, max;
    range(&p, gain, &def, &min, &max);
    CHECK(def == 0 && min < -40 && max > 0, "gain range %g..%g default %g", min, max, def);
    CHECK(!strcmp(port_unit(&p, gain), LV2_UNITS__db), "gain unit %s", port_unit(&p, gain));
    CHECK(port_has(&p, bypass, LV2_CORE__toggled) && port_has(&p, bypass, LV2_CORE__integer), "bypass is a toggle");

    instantiate(&p);
    static float io[2][N];
    uint32_t in1 = port_index(&p, "in_1_1"), in2 = port_index(&p, "in_1_2");
    uint32_t out1 = port_index(&p, "out_1_1"), out2 = port_index(&p, "out_1_2");
    // In place, as hosts may run it.
    lilv_instance_connect_port(p.instance, in1, io[0]);
    lilv_instance_connect_port(p.instance, out1, io[0]);
    lilv_instance_connect_port(p.instance, in2, io[1]);
    lilv_instance_connect_port(p.instance, out2, io[1]);
    lilv_instance_activate(p.instance);
    p.controls[gain] = -6;
    for (int block = 0; block < 20; block++) {  // past the smoothing ramp
        for (int i = 0; i < N; i++) io[0][i] = io[1][i] = 0.5f;
        events_begin(&p);
        events_end(&p);
        lilv_instance_run(p.instance, N);
    }
    float g = powf(10, -6.0f / 20);
    CHECK(fabsf(io[0][N - 1] - 0.5f * g) < 1e-5f && fabsf(io[1][0] - 0.5f * g) < 1e-5f, "gain -6 dB in place: %f", io[0][N - 1]);
    // A block longer than maxBlockLength is split.
    static float longIn[3 * N], longOut[3 * N];
    for (int i = 0; i < 3 * N; i++) longIn[i] = 0.25f;
    lilv_instance_connect_port(p.instance, in1, longIn);
    lilv_instance_connect_port(p.instance, out1, longOut);
    events_begin(&p);
    events_end(&p);
    lilv_instance_run(p.instance, 3 * N);
    CHECK(fabsf(longOut[3 * N - 1] - 0.25f * g) < 1e-5f && fabsf(longOut[N] - 0.25f * g) < 1e-5f, "split block %f", longOut[3 * N - 1]);
    CHECK(p.controls[port_index(&p, "latency")] == 0, "no latency");

    // The state and the ports restore a new instance.
    LilvState* st = save_state(&p);
    Plugin q = load(bundle);
    instantiate(&q);
    lilv_state_restore(st, q.instance, set_port_value, &q, 0, NULL);
    CHECK(q.controls[gain] == -6, "restored gain port %f", q.controls[gain]);
    static float qi[2][N], qo[2][N];
    lilv_instance_connect_port(q.instance, in1, qi[0]);
    lilv_instance_connect_port(q.instance, in2, qi[1]);
    lilv_instance_connect_port(q.instance, out1, qo[0]);
    lilv_instance_connect_port(q.instance, out2, qo[1]);
    lilv_instance_activate(q.instance);
    for (int block = 0; block < 20; block++) {
        for (int i = 0; i < N; i++) qi[0][i] = qi[1][i] = 1;
        events_begin(&q);
        events_end(&q);
        lilv_instance_run(q.instance, N);
    }
    CHECK(fabsf(qo[1][N - 1] - g) < 1e-5f, "restored instance gain %f", qo[1][N - 1]);
    // Bypass passes the input through.
    q.controls[bypass] = 1;
    for (int block = 0; block < 4; block++) {
        for (int i = 0; i < N; i++) qi[0][i] = qi[1][i] = 1;
        lilv_instance_run(q.instance, N);
    }
    CHECK(qo[0][N - 1] == 1, "bypassed %f", qo[0][N - 1]);
    lilv_state_free(st);
    lilv_instance_deactivate(q.instance);
    lilv_instance_free(q.instance);
    lilv_instance_deactivate(p.instance);
    lilv_instance_free(p.instance);
    printf("gain ok\n");
}

static float peak(const float* x, int n) {
    float m = 0;
    for (int i = 0; i < n; i++) m = fmaxf(m, fabsf(x[i]));
    return m;
}

static void test_synth(const char* bundle) {
    Plugin p = load(bundle);
    check_common(&p);
    CHECK(plugin_is_a(&p, LV2_CORE__InstrumentPlugin), "synth is an instrument");
    LilvNode* midiEvent = uri(&p, LV2_MIDI__MidiEvent);
    LilvNode* supports = uri(&p, LV2_ATOM__supports);
    LilvNodes* s = lilv_port_get_value(p.plugin, lilv_plugin_get_port_by_index(p.plugin, port_index(&p, "control")), supports);
    CHECK(s && lilv_nodes_contains(s, midiEvent), "control port takes MIDI");
    lilv_nodes_free(s);

    // Factory programs are presets.
    LilvNode* presetClass = uri(&p, LV2_PRESETS__Preset);
    LilvNodes* presets = lilv_plugin_get_related(p.plugin, presetClass);
    CHECK(presets && lilv_nodes_size(presets) == 4, "4 presets, got %u", presets ? lilv_nodes_size(presets) : 0);
    LilvState* pluck = NULL;
    LILV_FOREACH (nodes, i, presets) {
        const LilvNode* preset = lilv_nodes_get(presets, i);
        lilv_world_load_resource(p.world, preset);
        LilvState* st = lilv_state_new_from_world(p.world, &map, preset);
        CHECK(st, "preset %s", lilv_node_as_uri(preset));
        if (lilv_state_get_label(st) && !strcmp(lilv_state_get_label(st), "Pluck")) pluck = st;
        else lilv_state_free(st);
    }
    CHECK(pluck, "Pluck preset");
    lilv_nodes_free(presets);
    lilv_node_free(presetClass);

    instantiate(&p);
    uint32_t release = param_port(&p, "Release");
    lilv_state_restore(pluck, p.instance, set_port_value, &p, 0, NULL);
    CHECK(p.controls[release] == 80, "Pluck release %f", p.controls[release]);
    lilv_state_free(pluck);

    static float out[2][N];
    lilv_instance_connect_port(p.instance, port_index(&p, "out_1_1"), out[0]);
    lilv_instance_connect_port(p.instance, port_index(&p, "out_1_2"), out[1]);
    lilv_instance_activate(p.instance);
    events_begin(&p);
    events_end(&p);
    lilv_instance_run(p.instance, N);
    CHECK(peak(out[0], N) == 0, "silent without notes");
    events_begin(&p);
    const uint8_t on[] = { 0x90, 60, 100 };
    events_midi(&p, 32, on, 3);
    events_end(&p);
    lilv_instance_run(p.instance, N);
    CHECK(peak(out[0], 32) == 0 && peak(out[0] + 32, N - 32) > 0.01f, "note sounds from its frame: %f", peak(out[0] + 32, N - 32));
    events_begin(&p);
    const uint8_t off[] = { 0x80, 60, 0 };
    events_midi(&p, 0, off, 3);
    events_end(&p);
    for (int block = 0; block < 200; block++) {
        lilv_instance_run(p.instance, N);
        events_begin(&p);
        events_end(&p);
    }
    CHECK(peak(out[0], N) < 1e-4f, "released after 80 ms: %f", peak(out[0], N));
    lilv_instance_deactivate(p.instance);
    lilv_instance_free(p.instance);
    printf("synth ok\n");
}

static void test_midifx(const char* bundle) {
    Plugin p = load(bundle);
    check_common(&p);
    CHECK(plugin_is_a(&p, LV2_CORE_PREFIX "MIDIPlugin"), "midifx is a MIDI plugin");
    uint32_t transpose = param_port(&p, "Transpose");
    CHECK(port_has(&p, transpose, LV2_CORE__integer), "stepped transpose is an integer port");
    instantiate(&p);
    lilv_instance_activate(p.instance);
    p.controls[transpose] = 36;  // +12 semitones
    events_begin(&p);
    const uint8_t on[] = { 0x93, 60, 64 }, off[] = { 0x83, 60, 0 };
    const uint8_t sysex[] = { 0xF0, 0x7D, 0x01, 0x02, 0xF7 };
    events_midi(&p, 5, on, 3);
    events_midi(&p, 7, sysex, sizeof(sysex));
    events_midi(&p, 40, off, 3);
    events_end(&p);
    lilv_instance_run(p.instance, N);
    const LV2_Atom_Sequence* seq = (const LV2_Atom_Sequence*)p.eventsOut;
    CHECK(seq->atom.type == map_uri(NULL, LV2_ATOM__Sequence), "output is a sequence");
    int count = 0;
    LV2_ATOM_SEQUENCE_FOREACH (seq, ev) {
        const uint8_t* msg = (const uint8_t*)LV2_ATOM_BODY_CONST(&ev->body);
        CHECK(ev->body.type == map_uri(NULL, LV2_MIDI__MidiEvent), "MIDI output");
        if (count == 0) CHECK(ev->time.frames == 5 && ev->body.size == 3 && msg[0] == 0x93 && msg[1] == 72 && msg[2] == 64, "transposed note on %u %u", msg[0], msg[1]);
        if (count == 1) CHECK(ev->time.frames == 7 && ev->body.size == sizeof(sysex) && !memcmp(msg, sysex, sizeof(sysex)), "SysEx passes through");
        if (count == 2) CHECK(ev->time.frames == 40 && (msg[0] & 0xF0) == 0x80 && msg[1] == 72, "matching note off");
        count++;
    }
    CHECK(count == 3, "3 output events, got %d", count);
    lilv_instance_deactivate(p.instance);
    lilv_instance_free(p.instance);
    printf("midifx ok\n");
}

static void test_ducker(const char* bundle) {
    Plugin p = load(bundle);
    check_common(&p);
    uint32_t sc = port_index(&p, "in_2_1");
    CHECK(port_has(&p, sc, LV2_CORE__isSideChain) && port_has(&p, sc, LV2_CORE__connectionOptional), "side chain port");
    CHECK(!port_has(&p, port_index(&p, "in_1_1"), LV2_CORE__isSideChain), "main input");
    instantiate(&p);
    static float in[2][4 * N], out[2][4 * N];
    in[0][0] = in[1][0] = 1;
    lilv_instance_connect_port(p.instance, port_index(&p, "in_1_1"), in[0]);
    lilv_instance_connect_port(p.instance, port_index(&p, "in_1_2"), in[1]);
    lilv_instance_connect_port(p.instance, port_index(&p, "out_1_1"), out[0]);
    lilv_instance_connect_port(p.instance, port_index(&p, "out_1_2"), out[1]);
    // The side chain stays unconnected.
    lilv_instance_activate(p.instance);
    events_begin(&p);
    events_end(&p);
    lilv_instance_run(p.instance, 4 * N);
    float latency = p.controls[port_index(&p, "latency")];
    CHECK(latency == 96, "latency %f, want 96 (2 ms @ 48k)", latency);
    for (int i = 0; i < 4 * N; i++) CHECK(out[0][i] == (i == 96 ? 1.0f : 0.0f), "impulse delayed by latency: out[%d]=%f", i, out[0][i]);
    lilv_instance_deactivate(p.instance);
    lilv_instance_free(p.instance);
    printf("ducker ok\n");
}

#ifdef LV2CHECK_UI
#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>
#include <X11/keysym.h>
#include <dlfcn.h>
#include <lv2/instance-access/instance-access.h>
#include <lv2/ui/ui.h>
#include <time.h>

static struct {
    int writes, touches, resizes;
    uint32_t lastPort;
    int width, height;
    Plugin* plugin;
} uiHost;

static void ui_write(LV2UI_Controller controller, uint32_t port, uint32_t size, uint32_t protocol, const void* buffer) {
    (void)controller;
    CHECK(protocol == 0 && size == sizeof(float), "control port write");
    uiHost.writes++;
    uiHost.lastPort = port;
    uiHost.plugin->controls[port] = *(const float*)buffer;  // as the host applies it
}

static int ui_host_resize(LV2UI_Feature_Handle h, int width, int height) {
    (void)h;
    uiHost.resizes++;
    uiHost.width = width;
    uiHost.height = height;
    return 0;
}

static void ui_touch(LV2UI_Feature_Handle h, uint32_t port, bool grabbed) {
    (void)h;
    (void)grabbed;
    CHECK(port == uiHost.lastPort || uiHost.writes == 0, "touch on port %u", port);
    uiHost.touches++;
}

static unsigned int child_count(Display* display, Window window) {
    Window root, parent, *children = NULL;
    unsigned int count = 0;
    XQueryTree(display, window, &root, &parent, &children, &count);
    if (children) XFree(children);
    return count;
}

static void press(Display* display, KeySym sym) {
    KeyCode code = XKeysymToKeycode(display, sym);
    XTestFakeKeyEvent(display, code, True, 0);
    XTestFakeKeyEvent(display, code, False, 0);
    XFlush(display);
}

// ui_run is the host's UI loop: audio blocks, the UI idle callback and X
// events.
static void ui_run(Display* display, Plugin* p, const LV2UI_Idle_Interface* idle, LV2UI_Handle ui, int ms) {
    struct timespec tick = { 0, 16 * 1000 * 1000 };
    for (int elapsed = 0; elapsed < ms; elapsed += 16) {
        events_begin(p);
        events_end(p);
        lilv_instance_run(p->instance, N);
        CHECK(idle->idle(ui) == 0, "UI closed itself");
        while (XPending(display)) {
            XEvent e;
            XNextEvent(display, &e);
        }
        nanosleep(&tick, NULL);
    }
}

static void test_ui(const char* bundle, bool custom) {
    Display* display = XOpenDisplay(NULL);
    CHECK(display, "no X display (run under xvfb-run)");
    Window parent = XCreateSimpleWindow(display, DefaultRootWindow(display), 0, 0, 900, 640, 0, 0, 0);
    XMapWindow(display, parent);
    XSync(display, False);

    Plugin p = load(bundle);
    LilvUIs* uis = lilv_plugin_get_uis(p.plugin);
    CHECK(uis && lilv_uis_size(uis) == 1, "one UI");
    const LilvUI* lui = lilv_uis_get(uis, lilv_uis_begin(uis));
    LilvNode* x11 = uri(&p, LV2_UI__X11UI);
    CHECK(lilv_ui_is_a(lui, x11), "X11UI");
    char* binary = lilv_file_uri_parse(lilv_node_as_uri(lilv_ui_get_binary_uri(lui)), NULL);
    instantiate(&p);
    LilvNode* audio = uri(&p, LV2_CORE__AudioPort);
    uint32_t ports = lilv_plugin_get_num_ports(p.plugin);
    float* buffers = (float*)calloc((size_t)ports * N, sizeof(float));
    for (uint32_t i = 0; i < ports; i++) {
        if (lilv_port_is_a(p.plugin, lilv_plugin_get_port_by_index(p.plugin, i), audio)) lilv_instance_connect_port(p.instance, i, buffers + (size_t)i * N);
    }
    lilv_node_free(audio);
    lilv_instance_activate(p.instance);

    void* lib = dlopen(binary, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", binary, dlerror());
    typedef const LV2UI_Descriptor* (*UIDescriptor)(uint32_t);
    UIDescriptor get = (UIDescriptor)dlsym(lib, "lv2ui_descriptor");
    CHECK(get, "lv2ui_descriptor");
    const LV2UI_Descriptor* d = NULL;
    for (uint32_t i = 0; (d = get(i)); i++) {
        if (!strcmp(d->URI, lilv_node_as_uri(lilv_ui_get_uri(lui)))) break;
    }
    CHECK(d, "UI descriptor %s", lilv_node_as_uri(lilv_ui_get_uri(lui)));
    const LV2UI_Idle_Interface* idle = (const LV2UI_Idle_Interface*)d->extension_data(LV2_UI__idleInterface);
    const LV2UI_Resize* uiResize = (const LV2UI_Resize*)d->extension_data(LV2_UI__resize);
    CHECK(idle && uiResize, "idle and resize interfaces");

    uiHost.plugin = &p;
    LV2UI_Resize resize = { NULL, ui_host_resize };
    LV2UI_Touch touch = { NULL, ui_touch };
    LV2_Feature instanceFeature = { LV2_INSTANCE_ACCESS_URI, lilv_instance_get_handle(p.instance) };
    LV2_Feature parentFeature = { LV2_UI__parent, (void*)(uintptr_t)parent };
    LV2_Feature resizeFeature = { LV2_UI__resize, &resize }, touchFeature = { LV2_UI__touch, &touch };
    LV2_Feature mapFeature = { LV2_URID__map, &map };
    const LV2_Feature* features[] = { &instanceFeature, &parentFeature, &resizeFeature, &touchFeature, &mapFeature, NULL };
    LV2UI_Widget widget = NULL;
    LV2UI_Handle ui = d->instantiate(d, lilv_node_as_uri(lilv_plugin_get_uri(p.plugin)), bundle, ui_write, NULL, &widget, features);
    CHECK(ui && widget, "UI instantiate");
    // A second UI on the same instance is refused.
    LV2UI_Widget second = NULL;
    CHECK(!d->instantiate(d, lilv_node_as_uri(lilv_plugin_get_uri(p.plugin)), bundle, ui_write, NULL, &second, features), "second UI refused");
    CHECK(uiHost.resizes >= 1 && uiHost.width > 0 && uiHost.height > 0, "UI reports its size");
    ui_run(display, &p, idle, ui, 1500);
    CHECK(child_count(display, parent) >= 1, "editor window embedded into the host window");
    Window root, widgetParent, *children = NULL;
    unsigned int count = 0;
    XQueryTree(display, (Window)(uintptr_t)widget, &root, &widgetParent, &children, &count);
    if (children) XFree(children);
    CHECK(widgetParent == parent, "the widget is the editor window inside the parent");
    if (custom) {
        CHECK(uiHost.width == 720 && uiHost.height == 420, "startup vst3go.resize request %dx%d", uiHost.width, uiHost.height);
    }

    // Keyboard input in the page goes to the host as a port write with a
    // touch gesture.
    XSetInputFocus(display, (Window)(uintptr_t)widget, RevertToParent, CurrentTime);
    ui_run(display, &p, idle, ui, 100);
    for (int tab = 0; tab < 8 && uiHost.writes == 0; tab++) {
        press(display, XK_Tab);
        ui_run(display, &p, idle, ui, 50);
        press(display, XK_End);
        ui_run(display, &p, idle, ui, 150);
    }
    CHECK(uiHost.writes >= 1 && uiHost.touches >= 2, "editor edit reached the host (%d writes, %d touches)", uiHost.writes, uiHost.touches);
    printf("lv2 ui input wrote port %u = %g\n", uiHost.lastPort, p.controls[uiHost.lastPort]);

    // Host automation reaches the open editor, then the host resizes it.
    float def, min, max;
    range(&p, uiHost.lastPort, &def, &min, &max);
    for (int i = 0; i < 10; i++) {
        p.controls[uiHost.lastPort] = i % 2 ? max : min;
        ui_run(display, &p, idle, ui, 32);
    }
    if (custom) {
        // A MIDI program change selects Pluck in the plugin; the UI writes
        // the new values to the host ports.
        uint32_t release = param_port(&p, "Release");
        events_begin(&p);
        const uint8_t program[] = { 0xC0, 1 };
        events_midi(&p, 0, program, sizeof(program));
        events_end(&p);
        lilv_instance_run(p.instance, N);
        for (int i = 0; i < 20 && p.controls[release] != 80; i++) {
            CHECK(idle->idle(ui) == 0, "UI closed itself");
            struct timespec tick = { 0, 16 * 1000 * 1000 };
            nanosleep(&tick, NULL);
        }
        CHECK(p.controls[release] == 80, "program values reach the host ports: release %f", p.controls[release]);
    }
    CHECK(uiResize->ui_resize(ui, 640, 480) == 0, "host resize");
    ui_run(display, &p, idle, ui, 200);
    d->cleanup(ui);
    XSync(display, False);
    CHECK(child_count(display, parent) == 0, "editor window destroyed with the UI");
    // The instance takes a new UI after the first closed.
    ui = d->instantiate(d, lilv_node_as_uri(lilv_plugin_get_uri(p.plugin)), bundle, ui_write, NULL, &widget, features);
    CHECK(ui, "reopen");
    ui_run(display, &p, idle, ui, 300);
    d->cleanup(ui);
    lilv_instance_deactivate(p.instance);
    lilv_instance_free(p.instance);
    free(buffers);
    lilv_free(binary);
    lilv_node_free(x11);
    lilv_uis_free(uis);
    printf("lv2 ui ok\n");
}
#endif

int main(int argc, char** argv) {
#ifdef LV2CHECK_UI
    if (argc >= 3 && !strcmp(argv[1], "--ui")) {
        bool custom = argc == 4 && !strcmp(argv[2], "--custom");
        test_ui(argv[argc - 1], custom);
        return 0;
    }
#endif
    CHECK(argc == 3, "usage: lv2check gain|synth|midifx|ducker <bundle.lv2>");
    if (!strcmp(argv[1], "gain")) test_gain(argv[2]);
    else if (!strcmp(argv[1], "synth")) test_synth(argv[2]);
    else if (!strcmp(argv[1], "midifx")) test_midifx(argv[2]);
    else if (!strcmp(argv[1], "ducker")) test_ducker(argv[2]);
    else CHECK(0, "unknown test %s", argv[1]);
    return 0;
}
