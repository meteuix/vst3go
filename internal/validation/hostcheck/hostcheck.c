// hostcheck is a minimal VST3 host that checks the processing behavior of
// the validation plugins (sample-accurate automation and notes, MIDI
// mapping, event output, bus negotiation, latency). scripts/validate_vst3.sh
// builds and runs it.
#include "vst3/vst3_c_api.h"
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

// ---- IParamValueQueue / IParameterChanges ----
typedef struct { Steinberg_int32 offset; double value; } Point;
typedef struct {
    struct Steinberg_Vst_IParamValueQueueVtbl* lpVtbl;
    Steinberg_Vst_ParamID id;
    Point points[16];
    int count;
} Queue;
typedef struct {
    struct Steinberg_Vst_IParameterChangesVtbl* lpVtbl;
    Queue queues[16];
    int count;
} Changes;

static Steinberg_tresult SMTG_STDMETHODCALLTYPE qi(void* t, const Steinberg_TUID iid, void** o) { (void)t; (void)iid; *o = NULL; return Steinberg_kNoInterface; }
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE ref(void* t) { (void)t; return 1; }

static Steinberg_Vst_ParamID SMTG_STDMETHODCALLTYPE q_id(void* t) { return ((Queue*)t)->id; }
static Steinberg_int32 SMTG_STDMETHODCALLTYPE q_count(void* t) { return ((Queue*)t)->count; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE q_get(void* t, Steinberg_int32 i, Steinberg_int32* off, Steinberg_Vst_ParamValue* v) {
    Queue* q = t; if (i < 0 || i >= q->count) return Steinberg_kInvalidArgument;
    *off = q->points[i].offset; *v = q->points[i].value; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE q_add(void* t, Steinberg_int32 off, Steinberg_Vst_ParamValue v, Steinberg_int32* idx) {
    Queue* q = t; if (q->count >= 16) return Steinberg_kResultFalse;
    q->points[q->count].offset = off; q->points[q->count].value = v; if (idx) *idx = q->count; q->count++; return Steinberg_kResultOk;
}
static struct Steinberg_Vst_IParamValueQueueVtbl queueVtbl = { qi, ref, ref, q_id, q_count, q_get, q_add };

static Steinberg_int32 SMTG_STDMETHODCALLTYPE c_count(void* t) { return ((Changes*)t)->count; }
static struct Steinberg_Vst_IParamValueQueue* SMTG_STDMETHODCALLTYPE c_get(void* t, Steinberg_int32 i) {
    Changes* c = t; return (i >= 0 && i < c->count) ? (struct Steinberg_Vst_IParamValueQueue*)&c->queues[i] : NULL;
}
static struct Steinberg_Vst_IParamValueQueue* SMTG_STDMETHODCALLTYPE c_add(void* t, const Steinberg_Vst_ParamID* id, Steinberg_int32* idx) {
    Changes* c = t;
    for (int i = 0; i < c->count; i++) if (c->queues[i].id == *id) { if (idx) *idx = i; return (struct Steinberg_Vst_IParamValueQueue*)&c->queues[i]; }
    if (c->count >= 16) return NULL;
    Queue* q = &c->queues[c->count]; q->lpVtbl = &queueVtbl; q->id = *id; q->count = 0; if (idx) *idx = c->count; c->count++;
    return (struct Steinberg_Vst_IParamValueQueue*)q;
}
static struct Steinberg_Vst_IParameterChangesVtbl changesVtbl = { qi, ref, ref, c_count, c_get, c_add };
static void changes_init(Changes* c) { memset(c, 0, sizeof(*c)); c->lpVtbl = &changesVtbl; }

// ---- IEventList ----
typedef struct {
    struct Steinberg_Vst_IEventListVtbl* lpVtbl;
    struct Steinberg_Vst_Event events[64];
    int count;
} Events;
static Steinberg_int32 SMTG_STDMETHODCALLTYPE e_count(void* t) { return ((Events*)t)->count; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE e_get(void* t, Steinberg_int32 i, struct Steinberg_Vst_Event* e) {
    Events* l = t; if (i < 0 || i >= l->count) return Steinberg_kInvalidArgument; *e = l->events[i]; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE e_add(void* t, struct Steinberg_Vst_Event* e) {
    Events* l = t; if (l->count >= 64) return Steinberg_kResultFalse; l->events[l->count++] = *e; return Steinberg_kResultOk;
}
static struct Steinberg_Vst_IEventListVtbl eventsVtbl = { qi, ref, ref, e_count, e_get, e_add };
static void events_init(Events* l) { memset(l, 0, sizeof(*l)); l->lpVtbl = &eventsVtbl; }

static struct Steinberg_Vst_Event note_on(int offset, int pitch, float velocity, int noteId) {
    struct Steinberg_Vst_Event e; memset(&e, 0, sizeof(e));
    e.type = Steinberg_Vst_Event_EventTypes_kNoteOnEvent; e.sampleOffset = offset;
    e.Steinberg_Vst_Event_noteOn.pitch = pitch; e.Steinberg_Vst_Event_noteOn.velocity = velocity;
    e.Steinberg_Vst_Event_noteOn.noteId = noteId; e.Steinberg_Vst_Event_noteOn.channel = 0;
    return e;
}
static struct Steinberg_Vst_Event note_off(int offset, int pitch, int noteId) {
    struct Steinberg_Vst_Event e; memset(&e, 0, sizeof(e));
    e.type = Steinberg_Vst_Event_EventTypes_kNoteOffEvent; e.sampleOffset = offset;
    e.Steinberg_Vst_Event_noteOff.pitch = pitch; e.Steinberg_Vst_Event_noteOff.noteId = noteId;
    return e;
}

// ---- plugin loading ----
typedef struct {
    struct Steinberg_Vst_IComponent* comp;
    struct Steinberg_Vst_IAudioProcessor* proc;
    struct Steinberg_Vst_IEditController* ctrl;
    struct Steinberg_Vst_IMidiMapping* midi;
} Plugin;

static Plugin load(const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", path, dlerror());
    typedef struct Steinberg_IPluginFactory* (*GetFactory)(void);
    GetFactory get = (GetFactory)dlsym(lib, "GetPluginFactory");
    CHECK(get, "no GetPluginFactory");
    struct Steinberg_IPluginFactory* f = get();
    struct Steinberg_PClassInfo ci;
    CHECK(f->lpVtbl->getClassInfo(f, 0, &ci) == Steinberg_kResultOk, "getClassInfo");
    Plugin p; memset(&p, 0, sizeof(p));
    CHECK(f->lpVtbl->createInstance(f, ci.cid, Steinberg_Vst_IComponent_iid, (void**)&p.comp) == Steinberg_kResultOk, "createInstance");
    CHECK(p.comp->lpVtbl->initialize(p.comp, NULL) == Steinberg_kResultOk, "initialize");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IAudioProcessor_iid, (void**)&p.proc) == Steinberg_kResultOk, "QI processor");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IEditController_iid, (void**)&p.ctrl) == Steinberg_kResultOk, "QI controller");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IMidiMapping_iid, (void**)&p.midi) == Steinberg_kResultOk, "QI midi mapping");
    return p;
}

static void start(Plugin* p, int maxBlock) {
    struct Steinberg_Vst_ProcessSetup setup = { Steinberg_Vst_ProcessModes_kRealtime, Steinberg_Vst_SymbolicSampleSizes_kSample32, maxBlock, 48000.0 };
    CHECK(p->proc->lpVtbl->setupProcessing(p->proc, &setup) == Steinberg_kResultOk, "setupProcessing");
    for (int dir = 0; dir < 2; dir++)
        for (int type = 0; type < 2; type++) {
            int n = p->comp->lpVtbl->getBusCount(p->comp, type, dir);
            for (int i = 0; i < n; i++) p->comp->lpVtbl->activateBus(p->comp, type, dir, i, 1);
        }
    CHECK(p->comp->lpVtbl->setActive(p->comp, 1) == Steinberg_kResultOk, "setActive");
    CHECK(p->proc->lpVtbl->setProcessing(p->proc, 1) == Steinberg_kResultOk, "setProcessing");
}

#define N 64

static void test_gain(const char* path) {
    Plugin p = load(path);
    start(&p, 512);
    float inL[N], inR[N], outL[N], outR[N];
    for (int i = 0; i < N; i++) { inL[i] = 0.5f; inR[i] = -0.25f; }
    float* ins[2] = { inL, inR }; float* outs[2] = { outL, outR };
    struct Steinberg_Vst_AudioBusBuffers in = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = ins } };
    struct Steinberg_Vst_AudioBusBuffers out = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = outs } };
    Changes inChanges, outChanges; changes_init(&inChanges); changes_init(&outChanges);
    Steinberg_Vst_ParamID gainID = 0; Steinberg_int32 idx;
    struct Steinberg_Vst_IParamValueQueue* q = inChanges.lpVtbl->addParameterData(&inChanges, &gainID, &idx);
    double plus6 = (6.0 + 80.0) / 92.0;
    q->lpVtbl->addPoint(q, 32, plus6, &idx);

    struct Steinberg_Vst_ProcessData data; memset(&data, 0, sizeof(data));
    data.processMode = Steinberg_Vst_ProcessModes_kRealtime;
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample32;
    data.numSamples = N; data.numInputs = 1; data.numOutputs = 1; data.inputs = &in; data.outputs = &out;
    data.inputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&inChanges;
    data.outputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&outChanges;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");

    float g = powf(10.f, 6.f / 20.f);
    for (int i = 0; i < N; i++) {
        float want = i < 32 ? 0.5f : 0.5f * g;
        CHECK(fabsf(outL[i] - want) < 1e-4f, "gain L[%d]=%f want %f", i, outL[i], want);
        CHECK(fabsf(outR[i] - (i < 32 ? -0.25f : -0.25f * g)) < 1e-4f, "gain R[%d]=%f", i, outR[i]);
    }
    CHECK(fabs(p.ctrl->lpVtbl->getParamNormalized(p.ctrl, 0) - plus6) < 1e-9, "controller sees automated value");
    int meter = -1;
    for (int i = 0; i < outChanges.count; i++) if (outChanges.queues[i].id == 2) meter = i;
    CHECK(meter >= 0, "meter output parameter missing");
    double level = outChanges.queues[meter].points[outChanges.queues[meter].count - 1].value;
    double wantLevel = (20 * log10(0.5 * g) + 60) / 60;
    CHECK(fabs(level - wantLevel) < 1e-3, "meter %f want %f", level, wantLevel);

    // Mono layout negotiation.
    p.comp->lpVtbl->setActive(p.comp, 0);
    Steinberg_Vst_SpeakerArrangement mono = Steinberg_Vst_kSpeakerM, surround = 0x3F;
    CHECK(p.proc->lpVtbl->setBusArrangements(p.proc, &mono, 1, &mono, 1) == Steinberg_kResultOk, "mono accepted");
    CHECK(p.proc->lpVtbl->setBusArrangements(p.proc, &surround, 1, &surround, 1) == Steinberg_kResultFalse, "5.1 rejected");
    struct Steinberg_Vst_BusInfo bi;
    p.comp->lpVtbl->getBusInfo(p.comp, Steinberg_Vst_MediaTypes_kAudio, Steinberg_Vst_BusDirections_kInput, 0, &bi);
    CHECK(bi.channelCount == 1, "mono bus has %d channels", bi.channelCount);
    printf("gain ok\n");
}

static void test_synth(const char* path) {
    Plugin p = load(path);
    start(&p, 512);
    float outL[N], outR[N]; float* outs[2] = { outL, outR };
    struct Steinberg_Vst_AudioBusBuffers out = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = outs } };
    Events in; events_init(&in);
    in.events[in.count++] = note_on(10, 69, 1.0f, 7);
    struct Steinberg_Vst_ProcessData data; memset(&data, 0, sizeof(data));
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample32;
    data.numSamples = N; data.numOutputs = 1; data.outputs = &out;
    data.inputEvents = (struct Steinberg_Vst_IEventList*)&in;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    for (int i = 0; i < 10; i++) CHECK(outL[i] == 0, "silence before note, [%d]=%f", i, outL[i]);
    int sounding = 0;
    for (int i = 10; i < N; i++) if (outL[i] != 0) sounding++; CHECK(outL[10] != 0, "note must start exactly at offset 10");
    CHECK(sounding > 40, "note should sound after offset 10 (%d samples)", sounding);
    CHECK(memcmp(outL, outR, sizeof(outL)) == 0, "stereo copy");
    CHECK(out.silenceFlags == 0, "sounding block flagged silent");

    // Pitch bend arrives through IMidiMapping.
    Steinberg_Vst_ParamID bendID = 0;
    CHECK(p.midi->lpVtbl->getMidiControllerAssignment(p.midi, 0, 0, Steinberg_Vst_ControllerNumbers_kPitchBend, &bendID) == Steinberg_kResultOk, "pitch bend mapping");
    Steinberg_Vst_ParamID none;
    CHECK(p.midi->lpVtbl->getMidiControllerAssignment(p.midi, 0, 5, 1, &none) == Steinberg_kResultFalse, "channel 6 unmapped");

    // Note off by ID, then the release decays to silence and flags it.
    events_init(&in);
    in.events[in.count++] = note_off(0, 69, 7);
    for (int block = 0; block < 2 * 48000 / N + 2; block++) {
        out.silenceFlags = 0;
        CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
        in.count = 0;
    }
    CHECK(out.silenceFlags == 3, "released synth should flag silence, got %llu", (unsigned long long)out.silenceFlags);
    printf("synth ok (pitch bend param %#x)\n", bendID);
}

static void test_midifx(const char* path) {
    Plugin p = load(path);
    start(&p, 512);
    Events in, outEv; events_init(&in); events_init(&outEv);
    in.events[in.count++] = note_on(5, 60, 0.5f, 3);
    in.events[in.count++] = note_off(40, 60, 3);
    Changes changes; changes_init(&changes);
    Steinberg_Vst_ParamID transpose = 0; Steinberg_int32 idx;
    struct Steinberg_Vst_IParamValueQueue* q = changes.lpVtbl->addParameterData(&changes, &transpose, &idx);
    q->lpVtbl->addPoint(q, 0, (12.0 + 24.0) / 48.0, &idx);
    // Change transpose mid-block: the note-off must still match the note-on.
    q->lpVtbl->addPoint(q, 20, (-5.0 + 24.0) / 48.0, &idx);
    struct Steinberg_Vst_ProcessData data; memset(&data, 0, sizeof(data));
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample32;
    data.numSamples = N;
    data.inputEvents = (struct Steinberg_Vst_IEventList*)&in;
    data.outputEvents = (struct Steinberg_Vst_IEventList*)&outEv;
    data.inputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&changes;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    CHECK(outEv.count == 2, "want 2 output events, got %d", outEv.count);
    CHECK(outEv.events[0].type == Steinberg_Vst_Event_EventTypes_kNoteOnEvent && outEv.events[0].Steinberg_Vst_Event_noteOn.pitch == 72 && outEv.events[0].sampleOffset == 5, "transposed note on");
    CHECK(fabsf(outEv.events[0].Steinberg_Vst_Event_noteOn.velocity - 0.5f) < 1e-6f && outEv.events[0].Steinberg_Vst_Event_noteOn.noteId == 3, "velocity/noteId kept");
    CHECK(outEv.events[1].type == Steinberg_Vst_Event_EventTypes_kNoteOffEvent && outEv.events[1].Steinberg_Vst_Event_noteOff.pitch == 72 && outEv.events[1].sampleOffset == 40, "matching note off");
    printf("midifx ok\n");
}

static void test_ducker(const char* path) {
    Plugin p = load(path);
    start(&p, 512);
    Steinberg_uint32 latency = p.proc->lpVtbl->getLatencySamples(p.proc);
    CHECK(latency == 96, "latency %u, want 96 (2 ms @ 48k)", latency);
    static float main0[N * 4], main1[N * 4], sc0[N * 4], sc1[N * 4], o0[N * 4], o1[N * 4];
    for (int i = 0; i < N * 4; i++) { main0[i] = main1[i] = (i == 0) ? 1.0f : 0.0f; sc0[i] = sc1[i] = 0; }
    float* m[2] = { main0, main1 }; float* s[2] = { sc0, sc1 }; float* o[2] = { o0, o1 };
    struct Steinberg_Vst_AudioBusBuffers ins[2] = {
        { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = m } },
        { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = s } },
    };
    struct Steinberg_Vst_AudioBusBuffers out = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = o } };
    struct Steinberg_Vst_ProcessData data; memset(&data, 0, sizeof(data));
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample32;
    data.numSamples = N * 4; data.numInputs = 2; data.numOutputs = 1; data.inputs = ins; data.outputs = &out;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    for (int i = 0; i < N * 4; i++) CHECK(o0[i] == (i == (int)latency ? 1.0f : 0.0f), "impulse delayed by latency: o[%d]=%f", i, o0[i]);
    printf("ducker ok\n");
}

int main(int argc, char** argv) {
    CHECK(argc == 3, "usage: hostcheck gain|synth|midifx|ducker <binary>");
    if (!strcmp(argv[1], "gain")) test_gain(argv[2]);
    else if (!strcmp(argv[1], "synth")) test_synth(argv[2]);
    else if (!strcmp(argv[1], "midifx")) test_midifx(argv[2]);
    else if (!strcmp(argv[1], "ducker")) test_ducker(argv[2]);
    else CHECK(0, "unknown test %s", argv[1]);
    return 0;
}
