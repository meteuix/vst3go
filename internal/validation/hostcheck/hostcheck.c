// hostcheck is a minimal VST3 host that checks the processing behavior of
// the validation plugins (sample-accurate automation and notes, MIDI
// mapping and learn, event output, bus negotiation, latency, 64-bit
// processing, programs, note expression, distributed messaging).
// scripts/validate_vst3.sh builds and runs it.
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


// ---- host objects: IAttributeList, IMessage, IHostApplication, IComponentHandler ----
static int uid_eq(const Steinberg_TUID a, const Steinberg_TUID b) { return memcmp(a, b, sizeof(Steinberg_TUID)) == 0; }

typedef struct { char id[64]; Steinberg_int64 i; double f; unsigned char bin[256]; Steinberg_uint32 size; Steinberg_Vst_TChar str[128]; } Attr;
typedef struct { struct Steinberg_Vst_IAttributeListVtbl* lpVtbl; Attr attrs[8]; int count; } AttrList;

static Attr* attr(void* t, const char* id, int create) {
    AttrList* l = t;
    for (int i = 0; i < l->count; i++) if (!strcmp(l->attrs[i].id, id)) return &l->attrs[i];
    if (!create || l->count >= 8) return NULL;
    Attr* a = &l->attrs[l->count++]; memset(a, 0, sizeof(*a)); snprintf(a->id, sizeof(a->id), "%s", id); return a;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_setInt(void* t, const char* id, Steinberg_int64 v) { attr(t, id, 1)->i = v; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_getInt(void* t, const char* id, Steinberg_int64* v) { Attr* a = attr(t, id, 0); if (!a) return Steinberg_kResultFalse; *v = a->i; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_setFloat(void* t, const char* id, double v) { attr(t, id, 1)->f = v; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_getFloat(void* t, const char* id, double* v) { Attr* a = attr(t, id, 0); if (!a) return Steinberg_kResultFalse; *v = a->f; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_setString(void* t, const char* id, const Steinberg_Vst_TChar* s) {
    Attr* a = attr(t, id, 1); int i = 0; for (; s[i] && i < 127; i++) a->str[i] = s[i]; a->str[i] = 0; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_getString(void* t, const char* id, Steinberg_Vst_TChar* s, Steinberg_uint32 bytes) {
    Attr* a = attr(t, id, 0); if (!a) return Steinberg_kResultFalse;
    Steinberg_uint32 n = bytes / sizeof(Steinberg_Vst_TChar); if (n == 0) return Steinberg_kResultFalse;
    Steinberg_uint32 i = 0; for (; a->str[i] && i + 1 < n; i++) s[i] = a->str[i]; s[i] = 0; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_setBinary(void* t, const char* id, const void* d, Steinberg_uint32 n) {
    if (n > 256) return Steinberg_kResultFalse;
    Attr* a = attr(t, id, 1); memcpy(a->bin, d, n); a->size = n; return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE a_getBinary(void* t, const char* id, const void** d, Steinberg_uint32* n) {
    Attr* a = attr(t, id, 0); if (!a) return Steinberg_kResultFalse; *d = a->bin; *n = a->size; return Steinberg_kResultOk;
}
static struct Steinberg_Vst_IAttributeListVtbl attrVtbl = { qi, ref, ref, a_setInt, a_getInt, a_setFloat, a_getFloat, a_setString, a_getString, a_setBinary, a_getBinary };
static void attrs_init(AttrList* l) { memset(l, 0, sizeof(*l)); l->lpVtbl = &attrVtbl; }

typedef struct { struct Steinberg_Vst_IMessageVtbl* lpVtbl; char id[64]; AttrList attrs; int refs; } Message;
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE m_addRef(void* t) { return ++((Message*)t)->refs; }
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE m_release(void* t) { Message* m = t; int r = --m->refs; if (r == 0) free(m); return r; }
static const char* SMTG_STDMETHODCALLTYPE m_getID(void* t) { return ((Message*)t)->id; }
static void SMTG_STDMETHODCALLTYPE m_setID(void* t, const char* id) { snprintf(((Message*)t)->id, 64, "%s", id); }
static struct Steinberg_Vst_IAttributeList* SMTG_STDMETHODCALLTYPE m_attrs(void* t) { return (struct Steinberg_Vst_IAttributeList*)&((Message*)t)->attrs; }
static struct Steinberg_Vst_IMessageVtbl messageVtbl = { qi, m_addRef, m_release, m_getID, m_setID, m_attrs };

typedef struct { struct Steinberg_Vst_IHostApplicationVtbl* lpVtbl; } HostApp;
static HostApp hostApp;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE h_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_Vst_IHostApplication_iid)) { *o = t; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE h_name(void* t, Steinberg_Vst_String128 name) { (void)t; const char* n = "hostcheck"; int i = 0; for (; n[i]; i++) name[i] = n[i]; name[i] = 0; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE h_create(void* t, Steinberg_TUID cid, Steinberg_TUID iid, void** o) {
    (void)t;
    if (uid_eq(cid, Steinberg_Vst_IMessage_iid) && uid_eq(iid, Steinberg_Vst_IMessage_iid)) {
        Message* m = calloc(1, sizeof(Message)); m->lpVtbl = &messageVtbl; attrs_init(&m->attrs); m->refs = 1; *o = m; return Steinberg_kResultOk;
    }
    *o = NULL; return Steinberg_kNoInterface;
}
static struct Steinberg_Vst_IHostApplicationVtbl hostVtbl = { h_qi, ref, ref, h_name, h_create };

typedef struct { struct Steinberg_Vst_IComponentHandlerVtbl* lpVtbl; Steinberg_int32 restarts; int edits; Steinberg_Vst_ParamID lastEdit; double lastValue; } Handler;
static Handler handler;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE hd_qi(void* t, const Steinberg_TUID iid, void** o) {
    if (uid_eq(iid, Steinberg_FUnknown_iid) || uid_eq(iid, Steinberg_Vst_IComponentHandler_iid)) { *o = t; return Steinberg_kResultOk; }
    *o = NULL; return Steinberg_kNoInterface;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE hd_edit(void* t, Steinberg_Vst_ParamID id) { (void)t; (void)id; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE hd_perform(void* t, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v) { Handler* h = t; h->edits++; h->lastEdit = id; h->lastValue = v; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE hd_restart(void* t, Steinberg_int32 flags) { ((Handler*)t)->restarts |= flags; return Steinberg_kResultOk; }
static struct Steinberg_Vst_IComponentHandlerVtbl handlerVtbl = { hd_qi, ref, ref, hd_edit, hd_perform, hd_edit, hd_restart };

// Connection proxy between processor and controller that logs message IDs.
typedef struct { struct Steinberg_Vst_IConnectionPointVtbl* lpVtbl; struct Steinberg_Vst_IConnectionPoint* target; char log[16][64]; int count; } Proxy;
static Steinberg_tresult SMTG_STDMETHODCALLTYPE p_connect(void* t, struct Steinberg_Vst_IConnectionPoint* o) { (void)t; (void)o; return Steinberg_kResultOk; }
static Steinberg_tresult SMTG_STDMETHODCALLTYPE p_notify(void* t, struct Steinberg_Vst_IMessage* m) {
    Proxy* p = t; if (p->count < 16) snprintf(p->log[p->count++], 64, "%s", m->lpVtbl->getMessageID(m));
    return p->target->lpVtbl->notify(p->target, m);
}
static struct Steinberg_Vst_IConnectionPointVtbl proxyVtbl = { qi, ref, ref, p_connect, p_connect, p_notify };
static Proxy toController, toProcessor;
static int logged(Proxy* p, const char* id) { for (int i = 0; i < p->count; i++) if (!strcmp(p->log[i], id)) return 1; return 0; }

// UTF-16 helpers
static void to_tchar(Steinberg_Vst_TChar* dst, const char* src) { int i = 0; for (; src[i]; i++) dst[i] = (Steinberg_Vst_TChar)src[i]; dst[i] = 0; }
static int tchar_eq(const Steinberg_Vst_TChar* a, const char* b) { int i = 0; for (; b[i]; i++) if (a[i] != (Steinberg_Vst_TChar)b[i]) return 0; return a[i] == 0; }

// ---- plugin loading ----
typedef struct {
    struct Steinberg_IPluginFactory* factory;
    struct Steinberg_Vst_IComponent* comp;
    struct Steinberg_Vst_IAudioProcessor* proc;
    struct Steinberg_Vst_IEditController* ctrl;
    struct Steinberg_Vst_IMidiMapping* midi;
    int distributed;
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
    p.factory = f;
    hostApp.lpVtbl = &hostVtbl;
    handler.lpVtbl = &handlerVtbl;
    CHECK(f->lpVtbl->createInstance(f, ci.cid, Steinberg_Vst_IComponent_iid, (void**)&p.comp) == Steinberg_kResultOk, "createInstance");
    CHECK(p.comp->lpVtbl->initialize(p.comp, (struct Steinberg_FUnknown*)&hostApp) == Steinberg_kResultOk, "initialize");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IAudioProcessor_iid, (void**)&p.proc) == Steinberg_kResultOk, "QI processor");
    Steinberg_TUID controllerCid;
    if (p.comp->lpVtbl->getControllerClassId(p.comp, controllerCid) == Steinberg_kResultOk) {
        // Distributed plugin: separate controller, connected through proxies.
        p.distributed = 1;
        void* none = NULL;
        CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IEditController_iid, &none) == Steinberg_kNoInterface, "processor must not be a controller");
        CHECK(f->lpVtbl->createInstance(f, controllerCid, Steinberg_Vst_IEditController_iid, (void**)&p.ctrl) == Steinberg_kResultOk, "create controller");
        CHECK(p.ctrl->lpVtbl->initialize(p.ctrl, (struct Steinberg_FUnknown*)&hostApp) == Steinberg_kResultOk, "controller initialize");
        struct Steinberg_Vst_IConnectionPoint *compCP, *ctrlCP;
        CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IConnectionPoint_iid, (void**)&compCP) == Steinberg_kResultOk, "processor connection point");
        CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IConnectionPoint_iid, (void**)&ctrlCP) == Steinberg_kResultOk, "controller connection point");
        toController.lpVtbl = &proxyVtbl; toController.target = ctrlCP;
        toProcessor.lpVtbl = &proxyVtbl; toProcessor.target = compCP;
        CHECK(compCP->lpVtbl->connect(compCP, (struct Steinberg_Vst_IConnectionPoint*)&toController) == Steinberg_kResultOk, "connect processor");
        CHECK(ctrlCP->lpVtbl->connect(ctrlCP, (struct Steinberg_Vst_IConnectionPoint*)&toProcessor) == Steinberg_kResultOk, "connect controller");
    } else {
        CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IEditController_iid, (void**)&p.ctrl) == Steinberg_kResultOk, "QI controller");
    }
    CHECK(p.ctrl->lpVtbl->setComponentHandler(p.ctrl, (struct Steinberg_Vst_IComponentHandler*)&handler) == Steinberg_kResultOk, "setComponentHandler");
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IMidiMapping_iid, (void**)&p.midi) == Steinberg_kResultOk, "QI midi mapping");
    return p;
}

static void start_size(Plugin* p, int maxBlock, Steinberg_int32 sampleSize) {
    struct Steinberg_Vst_ProcessSetup setup = { Steinberg_Vst_ProcessModes_kRealtime, sampleSize, maxBlock, 48000.0 };
    CHECK(p->proc->lpVtbl->setupProcessing(p->proc, &setup) == Steinberg_kResultOk, "setupProcessing");
    for (int dir = 0; dir < 2; dir++)
        for (int type = 0; type < 2; type++) {
            int n = p->comp->lpVtbl->getBusCount(p->comp, type, dir);
            for (int i = 0; i < n; i++) p->comp->lpVtbl->activateBus(p->comp, type, dir, i, 1);
        }
    CHECK(p->comp->lpVtbl->setActive(p->comp, 1) == Steinberg_kResultOk, "setActive");
    CHECK(p->proc->lpVtbl->setProcessing(p->proc, 1) == Steinberg_kResultOk, "setProcessing");
}

static void start(Plugin* p, int maxBlock) { start_size(p, maxBlock, Steinberg_Vst_SymbolicSampleSizes_kSample32); }

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

    // 64-bit processing (gain is still +6 dB).
    CHECK(p.proc->lpVtbl->canProcessSampleSize(p.proc, Steinberg_Vst_SymbolicSampleSizes_kSample64) == Steinberg_kResultOk, "gain supports 64-bit");
    p.proc->lpVtbl->setProcessing(p.proc, 0);
    start_size(&p, 512, Steinberg_Vst_SymbolicSampleSizes_kSample64);
    double in64[N], out64[N]; double* ins64[1] = { in64 }; double* outs64[1] = { out64 };
    for (int i = 0; i < N; i++) in64[i] = 0.125;
    struct Steinberg_Vst_AudioBusBuffers in64Bus = { 1, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers64 = ins64 } };
    struct Steinberg_Vst_AudioBusBuffers out64Bus = { 1, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers64 = outs64 } };
    memset(&data, 0, sizeof(data));
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample64;
    data.numSamples = N; data.numInputs = 1; data.numOutputs = 1; data.inputs = &in64Bus; data.outputs = &out64Bus;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process 64-bit");
    double g64 = pow(10.0, 6.0 / 20.0);
    for (int i = 0; i < N; i++) CHECK(fabs(out64[i] - 0.125 * g64) < 1e-12, "64-bit gain [%d]=%.15f", i, out64[i]);

    // Channel context (IInfoListener).
    struct Steinberg_Vst_ChannelContext_IInfoListener* info;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_ChannelContext_IInfoListener_iid, (void**)&info) == Steinberg_kResultOk, "QI info listener");
    AttrList channel; attrs_init(&channel);
    Steinberg_Vst_TChar name[128]; to_tchar(name, "Vox");
    channel.lpVtbl->setString(&channel, Steinberg_Vst_ChannelContext_kChannelNameKey, name);
    channel.lpVtbl->setInt(&channel, Steinberg_Vst_ChannelContext_kChannelColorKey, 0xFF336699);
    CHECK(info->lpVtbl->setChannelContextInfos(info, (struct Steinberg_Vst_IAttributeList*)&channel) == Steinberg_kResultOk, "setChannelContextInfos");
    void* none;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IUnitInfo_iid, &none) == Steinberg_kNoInterface, "gain has no units");
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
    for (int i = 10; i < N; i++) {
        if (outL[i] != 0) sounding++;
    }
    CHECK(outL[10] != 0, "note must start exactly at offset 10");
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
    CHECK(p.proc->lpVtbl->canProcessSampleSize(p.proc, Steinberg_Vst_SymbolicSampleSizes_kSample64) == Steinberg_kResultFalse, "synth is 32-bit only");

    // Note expression: volume 0 for note ID 9 silences only that note.
    events_init(&in);
    in.events[in.count++] = note_on(0, 60, 1.0f, 9);
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    CHECK(outL[N - 1] != 0, "note 9 sounding");
    events_init(&in);
    struct Steinberg_Vst_Event expr; memset(&expr, 0, sizeof(expr));
    expr.type = Steinberg_Vst_Event_EventTypes_kNoteExpressionValueEvent;
    expr.Steinberg_Vst_Event_noteExpressionValue.typeId = Steinberg_Vst_NoteExpressionTypeIDs_kVolumeTypeID;
    expr.Steinberg_Vst_Event_noteExpressionValue.noteId = 9;
    expr.Steinberg_Vst_Event_noteExpressionValue.value = 0;
    in.events[in.count++] = expr;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    for (int i = 0; i < N; i++) CHECK(outL[i] == 0, "note expression volume 0: [%d]=%f", i, outL[i]);
    events_init(&in);
    in.events[in.count++] = note_off(0, 60, 9);
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");

    struct Steinberg_Vst_INoteExpressionController* nec;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_INoteExpressionController_iid, (void**)&nec) == Steinberg_kResultOk, "QI note expression");
    CHECK(nec->lpVtbl->getNoteExpressionCount(nec, 0, 0) == 2, "2 note expressions");
    struct Steinberg_Vst_NoteExpressionTypeInfo neInfo;
    CHECK(nec->lpVtbl->getNoteExpressionInfo(nec, 0, 0, 1, &neInfo) == Steinberg_kResultOk && neInfo.typeId == Steinberg_Vst_NoteExpressionTypeIDs_kTuningTypeID && neInfo.valueDesc.defaultValue == 0.5, "tuning info");
    Steinberg_Vst_String128 str;
    CHECK(nec->lpVtbl->getNoteExpressionStringByValue(nec, 0, 0, Steinberg_Vst_NoteExpressionTypeIDs_kTuningTypeID, 0.75, str) == Steinberg_kResultOk && tchar_eq(str, "+60.00 st"), "tuning string");
    struct Steinberg_Vst_INoteExpressionPhysicalUIMapping* pui;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_INoteExpressionPhysicalUIMapping_iid, (void**)&pui) == Steinberg_kResultOk, "QI physical UI");
    struct Steinberg_Vst_PhysicalUIMap maps[2] = { { Steinberg_Vst_PhysicalUITypeIDs_kPUIPressure, 0 }, { Steinberg_Vst_PhysicalUITypeIDs_kPUIYMovement, 0 } };
    struct Steinberg_Vst_PhysicalUIMapList mapList = { 2, maps };
    CHECK(pui->lpVtbl->getPhysicalUIMapping(pui, 0, 0, &mapList) == Steinberg_kResultOk && maps[0].noteExpressionTypeID == Steinberg_Vst_NoteExpressionTypeIDs_kVolumeTypeID && maps[1].noteExpressionTypeID == Steinberg_Vst_NoteExpressionTypeIDs_kInvalidTypeID, "physical UI mapping");

    struct Steinberg_Vst_IKeyswitchController* ks;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IKeyswitchController_iid, (void**)&ks) == Steinberg_kResultOk, "QI keyswitch");
    struct Steinberg_Vst_KeyswitchInfo ksInfo;
    CHECK(ks->lpVtbl->getKeyswitchCount(ks, 0, 0) == 2 && ks->lpVtbl->getKeyswitchInfo(ks, 0, 0, 1, &ksInfo) == Steinberg_kResultOk && ksInfo.keyswitchMin == 25 && tchar_eq(ksInfo.title, "Square"), "keyswitches");

    // MIDI learn remaps a controller and asks the host to re-read mappings.
    struct Steinberg_Vst_IMidiLearn* learn;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IMidiLearn_iid, (void**)&learn) == Steinberg_kResultOk, "QI midi learn");
    CHECK(learn->lpVtbl->onLiveMIDIControllerInput(learn, 0, 0, 7) == Steinberg_kResultOk, "learn CC7");
    Steinberg_Vst_ParamID learned = 99;
    CHECK(p.midi->lpVtbl->getMidiControllerAssignment(p.midi, 0, 0, 7, &learned) == Steinberg_kResultOk && learned == 0, "CC7 learned to volume (%u)", learned);
    CHECK(handler.restarts & Steinberg_Vst_RestartFlags_kMidiCCAssignmentChanged, "learn restart flag");
    struct Steinberg_Vst_IMidiMapping2* mm2;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IMidiMapping2_iid, (void**)&mm2) == Steinberg_kResultOk, "QI midi mapping 2");
    Steinberg_uint32 n1 = mm2->lpVtbl->getNumMidi1ControllerAssignments(mm2, Steinberg_Vst_BusDirections_kInput);
    CHECK(n1 == 130, "MIDI 1 assignments %u, want 130 controllers", n1);
    struct Steinberg_Vst_Midi1ControllerParamIDAssignment* assigns = calloc(n1, sizeof(*assigns));
    struct Steinberg_Vst_Midi1ControllerParamIDAssignmentList alist = { n1, assigns };
    CHECK(mm2->lpVtbl->getMidi1ControllerAssignments(mm2, Steinberg_Vst_BusDirections_kInput, &alist) == Steinberg_kResultOk, "MIDI 1 assignments");
    int foundCC7 = 0; for (Steinberg_uint32 i = 0; i < n1; i++) if (assigns[i].controller == 7 && assigns[i].pId == 0) foundCC7 = 1;
    CHECK(foundCC7, "learned CC7 enumerated");
    free(assigns);

    // Programs (IUnitInfo).
    struct Steinberg_Vst_IUnitInfo* units;
    CHECK(p.ctrl->lpVtbl->queryInterface(p.ctrl, Steinberg_Vst_IUnitInfo_iid, (void**)&units) == Steinberg_kResultOk, "QI unit info");
    CHECK(units->lpVtbl->getUnitCount(units) == 2, "2 units");
    struct Steinberg_Vst_UnitInfo unit;
    CHECK(units->lpVtbl->getUnitInfo(units, 0, &unit) == Steinberg_kResultOk && unit.id == 0 && unit.programListId == 0, "root unit owns the program list");
    struct Steinberg_Vst_ProgramListInfo list;
    CHECK(units->lpVtbl->getProgramListCount(units) == 1 && units->lpVtbl->getProgramListInfo(units, 0, &list) == Steinberg_kResultOk && list.programCount == 4, "program list");
    CHECK(units->lpVtbl->getProgramName(units, 0, 1, str) == Steinberg_kResultOk && tchar_eq(str, "Pluck"), "program name");
    CHECK(units->lpVtbl->hasProgramPitchNames(units, 0, 3) == Steinberg_kResultOk && units->lpVtbl->getProgramPitchName(units, 0, 3, 24, str) == Steinberg_kResultOk && tchar_eq(str, "Saw"), "pitch names");
    struct Steinberg_Vst_IProgramListData* pld;
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IProgramListData_iid, (void**)&pld) == Steinberg_kResultOk && pld->lpVtbl->programDataSupported(pld, 0) == Steinberg_kResultOk, "program list data");

    // A host program change in process() loads the program sample-accurately.
    const Steinberg_Vst_ParamID program = 0x7EFFFFFF;
    Changes changes; changes_init(&changes); Steinberg_int32 idx;
    struct Steinberg_Vst_IParamValueQueue* q = changes.lpVtbl->addParameterData(&changes, &program, &idx);
    q->lpVtbl->addPoint(q, 0, 1.0 / 3.0, &idx);
    events_init(&in);
    data.inputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&changes;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    data.inputParameterChanges = NULL;
    double pluckRelease = p.ctrl->lpVtbl->plainParamToNormalized(p.ctrl, 1, 80.0);
    CHECK(fabs(p.ctrl->lpVtbl->getParamNormalized(p.ctrl, 1) - pluckRelease) < 1e-9, "program 1 release applied");
    // Selecting a program on the controller reloads values and notifies.
    handler.restarts = 0;
    CHECK(p.ctrl->lpVtbl->setParamNormalized(p.ctrl, program, 2.0 / 3.0) == Steinberg_kResultOk, "select program 2");
    CHECK(p.ctrl->lpVtbl->getParamNormalized(p.ctrl, 2) == 1.0, "program 2 selects the square shape");
    CHECK(handler.restarts & Steinberg_Vst_RestartFlags_kParamValuesChanged, "program restart flag");
    Steinberg_Vst_ParamID progMapping;
    CHECK(p.midi->lpVtbl->getMidiControllerAssignment(p.midi, 0, 0, Steinberg_Vst_ControllerNumbers_kCtrlProgramChange, &progMapping) == Steinberg_kResultFalse, "program change is not an IMidiMapping controller");
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

    // SysEx passes through (copied into plugin memory); chords are consumed.
    static const Steinberg_uint8 sysex[] = { 0xF0, 0x7D, 0x01, 0x02, 0xF7 };
    events_init(&in); events_init(&outEv); changes_init(&changes);
    struct Steinberg_Vst_Event ev; memset(&ev, 0, sizeof(ev));
    ev.type = Steinberg_Vst_Event_EventTypes_kDataEvent; ev.sampleOffset = 3;
    ev.Steinberg_Vst_Event_data.type = Steinberg_Vst_DataEvent_DataTypes_kMidiSysEx;
    ev.Steinberg_Vst_Event_data.bytes = sysex; ev.Steinberg_Vst_Event_data.size = sizeof(sysex);
    in.events[in.count++] = ev;
    static const Steinberg_char16 chordName[] = { 'C', 'm', '7' };
    memset(&ev, 0, sizeof(ev));
    ev.type = Steinberg_Vst_Event_EventTypes_kChordEvent; ev.sampleOffset = 4;
    ev.Steinberg_Vst_Event_chord.root = 0; ev.Steinberg_Vst_Event_chord.bassNote = -1; ev.Steinberg_Vst_Event_chord.mask = 0x489;
    ev.Steinberg_Vst_Event_chord.text = chordName; ev.Steinberg_Vst_Event_chord.textLen = 3;
    in.events[in.count++] = ev;
    CHECK(p.proc->lpVtbl->process(p.proc, &data) == Steinberg_kResultOk, "process");
    CHECK(outEv.count == 1, "want only the SysEx output event, got %d", outEv.count);
    struct Steinberg_Vst_Event* out0 = &outEv.events[0];
    CHECK(out0->type == Steinberg_Vst_Event_EventTypes_kDataEvent && out0->sampleOffset == 3 && out0->Steinberg_Vst_Event_data.type == Steinberg_Vst_DataEvent_DataTypes_kMidiSysEx, "SysEx output event");
    CHECK(out0->Steinberg_Vst_Event_data.size == sizeof(sysex) && out0->Steinberg_Vst_Event_data.bytes != sysex && !memcmp(out0->Steinberg_Vst_Event_data.bytes, sysex, sizeof(sysex)), "SysEx payload copied");
    printf("midifx ok\n");
}

static void test_ducker(const char* path) {
    Plugin p = load(path);
    CHECK(p.distributed, "ducker is a distributed plugin");
    CHECK(p.factory->lpVtbl->countClasses(p.factory) == 2, "processor and controller classes");
    struct Steinberg_PClassInfo ci;
    CHECK(p.factory->lpVtbl->getClassInfo(p.factory, 1, &ci) == Steinberg_kResultOk && !strcmp(ci.category, "Component Controller Class"), "controller class");
    start(&p, 512);
    Steinberg_uint32 latency = p.proc->lpVtbl->getLatencySamples(p.proc);
    CHECK(latency == 96, "latency %u, want 96 (2 ms @ 48k)", latency);
    // The processor reports the new latency through the controller, and the
    // two sides exchange plugin messages.
    CHECK(logged(&toController, "vst3go.restart"), "restart forwarded to the controller");
    CHECK(handler.restarts & Steinberg_Vst_RestartFlags_kLatencyChanged, "latency restart reached the host");
    CHECK(logged(&toController, "ducker.latency") && logged(&toProcessor, "ducker.ack"), "plugin messages");
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
