#include "vst3_rt.h"
#include "rtguard.h"

#include <stdlib.h>
#include <string.h>

extern Steinberg_tresult GoAudioProcess(uintptr_t component, struct Steinberg_Vst_ProcessData* data);
extern void GoRTEnter(void);

// Capacities of a job. Events and parameter changes past them move to the
// next job; a block with more buses, channels or samples than the setup (a
// host error) runs on the host's thread.
#define RT_MAX_BUSES 32
#define RT_MAX_QUEUES 1024
#define RT_MAX_POINTS 8192
#define RT_MAX_EVENTS 2048
#define RT_ARENA 65536 // event payloads: sysex, note expression and chord text

typedef struct ParamList ParamList;

// ParamQueue is an IParamValueQueue over a range of its list's points
// (input) or the points tagged with its index (output).
typedef struct {
    const Steinberg_Vst_IParamValueQueueVtbl* lpVtbl;
    ParamList* list;
    Steinberg_Vst_ParamID id;
    int32_t first;
    int32_t count;
} ParamQueue;

typedef struct {
    int32_t queue;
    int32_t offset;
    double value;
} ParamPoint;

struct ParamList {
    const Steinberg_Vst_IParameterChangesVtbl* lpVtbl;
    int32_t numQueues;
    int32_t numPoints;
    int output;
    ParamQueue queues[RT_MAX_QUEUES];
    ParamPoint points[RT_MAX_POINTS];
};

typedef struct {
    const Steinberg_Vst_IEventListVtbl* lpVtbl;
    int32_t count;
    uint32_t used; // arena bytes
    struct Steinberg_Vst_Event events[RT_MAX_EVENTS];
    uint8_t arena[RT_ARENA];
} EventList;

// The last value of each parameter of skipped blocks.
typedef struct {
    Steinberg_Vst_ParamID id;
    double value;
} Carried;

struct VST3GoRT {
    VST3GoRTGuard* guard;
    uintptr_t goComponent;
    double sampleRate;
    int32_t maxSamples;
    int32_t sampleSize;
    int32_t channels;
    double share;
    Steinberg_tresult result;

    // The job.
    struct Steinberg_Vst_ProcessData data;
    struct Steinberg_Vst_ProcessContext context;
    struct Steinberg_Vst_AudioBusBuffers inputs[RT_MAX_BUSES];
    struct Steinberg_Vst_AudioBusBuffers outputs[RT_MAX_BUSES];
    void** channelPtrs; // channels pointers into audio
    uint8_t* audio;     // channels * maxSamples samples
    ParamList inParams, outParams;
    EventList inEvents, outEvents;

    // Input of skipped blocks and input that did not fit a job, for the
    // next job (audio thread only): set cur, and set cur ^ 1 while the next
    // job's input is copied.
    EventList carriedEvents[2];
    Carried carriedParams[2][RT_MAX_QUEUES];
    int32_t numCarriedParams[2];
    int cur;
};

// FUnknown for the job's lists: they live as long as the job.

static Steinberg_tresult SMTG_STDMETHODCALLTYPE noInterface(void* self, const Steinberg_TUID iid, void** obj) {
    (void)self;
    (void)iid;
    if (obj) *obj = NULL;
    return Steinberg_kNoInterface;
}
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE oneRef(void* self) {
    (void)self;
    return 1;
}

// IParamValueQueue

static Steinberg_Vst_ParamID SMTG_STDMETHODCALLTYPE queue_getParameterId(void* self) {
    return ((ParamQueue*)self)->id;
}
static Steinberg_int32 SMTG_STDMETHODCALLTYPE queue_getPointCount(void* self) {
    return ((ParamQueue*)self)->count;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE queue_getPoint(void* self, Steinberg_int32 index, Steinberg_int32* offset, Steinberg_Vst_ParamValue* value) {
    ParamQueue* q = (ParamQueue*)self;
    if (index < 0 || index >= q->count || !offset || !value) return Steinberg_kInvalidArgument;
    ParamList* l = q->list;
    const ParamPoint* p = NULL;
    if (!l->output) {
        p = &l->points[q->first + index];
    } else {
        int32_t self_index = (int32_t)(q - l->queues);
        for (int32_t i = 0, seen = 0; i < l->numPoints; i++) {
            if (l->points[i].queue == self_index && seen++ == index) {
                p = &l->points[i];
                break;
            }
        }
        if (!p) return Steinberg_kResultFalse;
    }
    *offset = p->offset;
    *value = p->value;
    return Steinberg_kResultOk;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE queue_addPoint(void* self, Steinberg_int32 offset, Steinberg_Vst_ParamValue value, Steinberg_int32* index) {
    ParamQueue* q = (ParamQueue*)self;
    ParamList* l = q->list;
    if (!l->output || l->numPoints == RT_MAX_POINTS) return Steinberg_kResultFalse;
    l->points[l->numPoints++] = (ParamPoint){ (int32_t)(q - l->queues), offset, value };
    if (index) *index = q->count;
    q->count++;
    return Steinberg_kResultOk;
}
static const Steinberg_Vst_IParamValueQueueVtbl queueVtbl = {
    noInterface, oneRef, oneRef, queue_getParameterId, queue_getPointCount, queue_getPoint, queue_addPoint
};

// IParameterChanges

static Steinberg_int32 SMTG_STDMETHODCALLTYPE params_getParameterCount(void* self) {
    return ((ParamList*)self)->numQueues;
}
static struct Steinberg_Vst_IParamValueQueue* SMTG_STDMETHODCALLTYPE params_getParameterData(void* self, Steinberg_int32 index) {
    ParamList* l = (ParamList*)self;
    if (index < 0 || index >= l->numQueues) return NULL;
    return (struct Steinberg_Vst_IParamValueQueue*)&l->queues[index];
}
static ParamQueue* params_newQueue(ParamList* l, Steinberg_Vst_ParamID id) {
    if (l->numQueues == RT_MAX_QUEUES) return NULL;
    ParamQueue* q = &l->queues[l->numQueues++];
    q->lpVtbl = &queueVtbl;
    q->list = l;
    q->id = id;
    q->first = l->numPoints;
    q->count = 0;
    return q;
}
static struct Steinberg_Vst_IParamValueQueue* SMTG_STDMETHODCALLTYPE params_addParameterData(void* self, const Steinberg_Vst_ParamID* id, Steinberg_int32* index) {
    ParamList* l = (ParamList*)self;
    if (!l->output || !id) return NULL;
    for (int32_t i = 0; i < l->numQueues; i++) {
        if (l->queues[i].id == *id) {
            if (index) *index = i;
            return (struct Steinberg_Vst_IParamValueQueue*)&l->queues[i];
        }
    }
    ParamQueue* q = params_newQueue(l, *id);
    if (!q) return NULL;
    if (index) *index = l->numQueues - 1;
    return (struct Steinberg_Vst_IParamValueQueue*)q;
}
static const Steinberg_Vst_IParameterChangesVtbl paramsVtbl = {
    noInterface, oneRef, oneRef, params_getParameterCount, params_getParameterData, params_addParameterData
};

// IEventList

static Steinberg_int32 SMTG_STDMETHODCALLTYPE events_getEventCount(void* self) {
    return ((EventList*)self)->count;
}
static Steinberg_tresult SMTG_STDMETHODCALLTYPE events_getEvent(void* self, Steinberg_int32 index, struct Steinberg_Vst_Event* e) {
    EventList* l = (EventList*)self;
    if (index < 0 || index >= l->count || !e) return Steinberg_kInvalidArgument;
    *e = l->events[index];
    return Steinberg_kResultOk;
}

// copyPayload copies size bytes into the list's arena.
static const void* copyPayload(EventList* l, const void* src, uint32_t size) {
    if (size == 0 || !src) return src;
    if (size > RT_ARENA - l->used) return NULL;
    uint8_t* dst = l->arena + l->used;
    memcpy(dst, src, size);
    l->used += (size + 7) & ~7u;
    if (l->used > RT_ARENA) l->used = RT_ARENA;
    return dst;
}

// copyText copies a TChar string of len characters and its terminator.
static const Steinberg_Vst_TChar* copyText(EventList* l, const Steinberg_Vst_TChar* text, uint32_t len) {
    if (!text) return NULL;
    uint32_t size = (len + 1) * (uint32_t)sizeof(Steinberg_Vst_TChar);
    if (size > RT_ARENA - l->used) return NULL;
    Steinberg_Vst_TChar* dst = (Steinberg_Vst_TChar*)(l->arena + l->used);
    memcpy(dst, text, len * sizeof(Steinberg_Vst_TChar));
    dst[len] = 0;
    l->used += (size + 7) & ~7u;
    if (l->used > RT_ARENA) l->used = RT_ARENA;
    return dst;
}

// appendEvent copies an event and its payload into l; offset < 0 keeps
// the event's sample offset.
static int appendEvent(EventList* l, const struct Steinberg_Vst_Event* src, int32_t offset) {
    if (l->count == RT_MAX_EVENTS) return 0;
    struct Steinberg_Vst_Event e = *src;
    if (offset >= 0) e.sampleOffset = offset;
    switch (e.type) {
    case Steinberg_Vst_Event_EventTypes_kDataEvent: {
        const void* bytes = copyPayload(l, e.Steinberg_Vst_Event_data.bytes, e.Steinberg_Vst_Event_data.size);
        if (!bytes && e.Steinberg_Vst_Event_data.size) return 0;
        e.Steinberg_Vst_Event_data.bytes = (const Steinberg_uint8*)bytes;
        break;
    }
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionTextEvent: {
        struct Steinberg_Vst_NoteExpressionTextEvent* t = &e.Steinberg_Vst_Event_noteExpressionText;
        const Steinberg_Vst_TChar* text = copyText(l, t->text, t->textLen);
        if (!text && t->text) return 0;
        t->text = text;
        break;
    }
    case Steinberg_Vst_Event_EventTypes_kChordEvent: {
        struct Steinberg_Vst_ChordEvent* c = &e.Steinberg_Vst_Event_chord;
        const Steinberg_Vst_TChar* text = copyText(l, c->text, c->textLen);
        if (!text && c->text) return 0;
        c->text = text;
        break;
    }
    case Steinberg_Vst_Event_EventTypes_kScaleEvent: {
        struct Steinberg_Vst_ScaleEvent* s = &e.Steinberg_Vst_Event_scale;
        const Steinberg_Vst_TChar* text = copyText(l, s->text, s->textLen);
        if (!text && s->text) return 0;
        s->text = text;
        break;
    }
    default:
        break;
    }
    l->events[l->count++] = e;
    return 1;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE events_addEvent(void* self, struct Steinberg_Vst_Event* e) {
    if (!e) return Steinberg_kInvalidArgument;
    return appendEvent((EventList*)self, e, -1) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}
static const Steinberg_Vst_IEventListVtbl eventsVtbl = {
    noInterface, oneRef, oneRef, events_getEventCount, events_getEvent, events_addEvent
};

static void resetEvents(EventList* l) {
    l->lpVtbl = &eventsVtbl;
    l->count = 0;
    l->used = 0;
}

static void resetParams(ParamList* l, int output) {
    l->lpVtbl = &paramsVtbl;
    l->numQueues = 0;
    l->numPoints = 0;
    l->output = output;
}

// The worker's job.
static void runJob(void* arg) {
    VST3GoRT* rt = (VST3GoRT*)arg;
    GoRTEnter();
    rtguard_entered(rt->guard);
    rt->result = GoAudioProcess(rt->goComponent, &rt->data);
}

VST3GoRT* vst3go_rt_new(uintptr_t goComponent, double sampleRate, int32_t maxSamples, int32_t sampleSize, int32_t channels, double share) {
    if (!(sampleRate > 0) || maxSamples <= 0 || channels < 0 || !(share > 0)) return NULL;
    VST3GoRT* rt = (VST3GoRT*)calloc(1, sizeof(VST3GoRT));
    if (!rt) return NULL;
    rt->goComponent = goComponent;
    rt->sampleRate = sampleRate;
    rt->maxSamples = maxSamples;
    rt->sampleSize = sampleSize;
    rt->channels = channels;
    rt->share = share > 1 ? 1 : share;
    size_t bytes = sampleSize == Steinberg_Vst_SymbolicSampleSizes_kSample64 ? sizeof(double) : sizeof(float);
    rt->channelPtrs = (void**)calloc(channels > 0 ? (size_t)channels : 1, sizeof(void*));
    rt->audio = (uint8_t*)calloc(channels > 0 ? (size_t)channels * (size_t)maxSamples : 1, bytes);
    if (!rt->channelPtrs || !rt->audio) {
        vst3go_rt_free(rt);
        return NULL;
    }
    resetEvents(&rt->carriedEvents[0]);
    resetEvents(&rt->carriedEvents[1]);
    int64_t period = (int64_t)((double)maxSamples / sampleRate * 1e9);
    rt->guard = rtguard_new(runJob, rt, GoRTEnter, period);
    if (!rt->guard) {
        vst3go_rt_free(rt);
        return NULL;
    }
    return rt;
}

void vst3go_rt_free(VST3GoRT* rt) {
    if (!rt) return;
    rtguard_free(rt->guard);
    free(rt->channelPtrs);
    free(rt->audio);
    free(rt);
}

// carryParam keeps the last value of a parameter in carried set.
static void carryParam(VST3GoRT* rt, int set, Steinberg_Vst_ParamID id, double value) {
    int32_t j = 0;
    while (j < rt->numCarriedParams[set] && rt->carriedParams[set][j].id != id) j++;
    if (j == rt->numCarriedParams[set]) {
        if (j == RT_MAX_QUEUES) return;
        rt->numCarriedParams[set]++;
    }
    rt->carriedParams[set][j] = (Carried){ id, value };
}

// lastPoint reads the last value of a host queue.
static int lastPoint(struct Steinberg_Vst_IParamValueQueue* q, double* value) {
    int32_t points = q->lpVtbl->getPointCount(q);
    Steinberg_int32 offset;
    return points > 0 && q->lpVtbl->getPoint(q, points - 1, &offset, value) == Steinberg_kResultOk;
}

// carry keeps the input of a block that cannot run for the next one.
static void carry(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data) {
    struct Steinberg_Vst_IEventList* events = data->inputEvents;
    if (events && events->lpVtbl) {
        int32_t n = events->lpVtbl->getEventCount(events);
        for (int32_t i = 0; i < n; i++) {
            struct Steinberg_Vst_Event e;
            memset(&e, 0, sizeof(e));
            if (events->lpVtbl->getEvent(events, i, &e) == Steinberg_kResultOk) {
                appendEvent(&rt->carriedEvents[rt->cur], &e, 0);
            }
        }
    }
    struct Steinberg_Vst_IParameterChanges* params = data->inputParameterChanges;
    if (params && params->lpVtbl) {
        int32_t n = params->lpVtbl->getParameterCount(params);
        for (int32_t i = 0; i < n; i++) {
            struct Steinberg_Vst_IParamValueQueue* q = params->lpVtbl->getParameterData(params, i);
            double value;
            if (q && q->lpVtbl && lastPoint(q, &value)) carryParam(rt, rt->cur, q->lpVtbl->getParameterId(q), value);
        }
    }
}

// carriedIndex returns the index of a carried parameter, or -1.
static int32_t carriedIndex(VST3GoRT* rt, Steinberg_Vst_ParamID id) {
    for (int32_t i = 0; i < rt->numCarriedParams[rt->cur]; i++) {
        if (rt->carriedParams[rt->cur][i].id == id) return i;
    }
    return -1;
}

// copyBuses points the job's buses at its own audio memory, copying the
// input audio. It returns 0 when the block does not fit.
static int copyBuses(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data, size_t bytes, int32_t* used) {
    int32_t n = data->numSamples;
    for (int dir = 0; dir < 2; dir++) {
        int32_t count = dir == 0 ? data->numInputs : data->numOutputs;
        struct Steinberg_Vst_AudioBusBuffers* host = dir == 0 ? data->inputs : data->outputs;
        struct Steinberg_Vst_AudioBusBuffers* own = dir == 0 ? rt->inputs : rt->outputs;
        if (count < 0 || count > RT_MAX_BUSES || (count > 0 && !host)) return 0;
        for (int32_t b = 0; b < count; b++) {
            int32_t channels = host[b].numChannels;
            if (channels < 0 || *used + channels > rt->channels) return 0;
            own[b].numChannels = channels;
            own[b].silenceFlags = host[b].silenceFlags;
            void** ptrs = rt->channelPtrs + *used;
            own[b].Steinberg_Vst_AudioBusBuffers_channelBuffers32 = (Steinberg_Vst_Sample32**)ptrs;
            void** src = (void**)host[b].Steinberg_Vst_AudioBusBuffers_channelBuffers32;
            for (int32_t ch = 0; ch < channels; ch++) {
                ptrs[ch] = rt->audio + (size_t)(*used + ch) * (size_t)rt->maxSamples * bytes;
                if (dir == 0) {
                    if (src && src[ch]) {
                        memcpy(ptrs[ch], src[ch], (size_t)n * bytes);
                    } else {
                        memset(ptrs[ch], 0, (size_t)n * bytes);
                    }
                }
            }
            *used += channels;
        }
    }
    return 1;
}

// copyInput fills the job's parameter changes and events: the carried
// input first, then the block's. Events from the first one that does not
// fit on move to the start of the next job, in order; a parameter whose
// changes do not fit moves there as its last value.
static void copyInput(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data) {
    resetEvents(&rt->inEvents);
    resetEvents(&rt->outEvents);
    resetParams(&rt->inParams, 0);
    resetParams(&rt->outParams, 1);
    int cur = rt->cur, next = cur ^ 1;
    EventList* carried = &rt->carriedEvents[cur];
    resetEvents(&rt->carriedEvents[next]);
    rt->numCarriedParams[next] = 0;

    int full = 0;
    for (int32_t i = 0; i < carried->count; i++) {
        full = full || !appendEvent(&rt->inEvents, &carried->events[i], -1);
        if (full) appendEvent(&rt->carriedEvents[next], &carried->events[i], 0);
    }
    struct Steinberg_Vst_IEventList* events = data->inputEvents;
    if (events && events->lpVtbl) {
        int32_t n = events->lpVtbl->getEventCount(events);
        for (int32_t i = 0; i < n; i++) {
            struct Steinberg_Vst_Event e;
            memset(&e, 0, sizeof(e));
            if (events->lpVtbl->getEvent(events, i, &e) != Steinberg_kResultOk) continue;
            full = full || !appendEvent(&rt->inEvents, &e, -1);
            if (full) appendEvent(&rt->carriedEvents[next], &e, 0);
        }
    }

    // Carried values not overridden at offset 0 lead their queue.
    uint8_t taken[RT_MAX_QUEUES];
    memset(taken, 0, (size_t)rt->numCarriedParams[cur]);
    ParamList* in = &rt->inParams;
    struct Steinberg_Vst_IParameterChanges* params = data->inputParameterChanges;
    if (params && params->lpVtbl) {
        int32_t n = params->lpVtbl->getParameterCount(params);
        for (int32_t i = 0; i < n; i++) {
            struct Steinberg_Vst_IParamValueQueue* hq = params->lpVtbl->getParameterData(params, i);
            if (!hq || !hq->lpVtbl) continue;
            Steinberg_Vst_ParamID id = hq->lpVtbl->getParameterId(hq);
            int32_t c = carriedIndex(rt, id);
            if (c >= 0) taken[c] = 1;
            double last = c >= 0 ? rt->carriedParams[cur][c].value : 0;
            int has = lastPoint(hq, &last) || c >= 0;
            ParamQueue* q = params_newQueue(in, id);
            if (!q) {
                if (has) carryParam(rt, next, id, last);
                continue;
            }
            int32_t points = hq->lpVtbl->getPointCount(hq);
            Steinberg_int32 offset = 0;
            Steinberg_Vst_ParamValue value = 0;
            int first = points > 0 && hq->lpVtbl->getPoint(hq, 0, &offset, &value) == Steinberg_kResultOk;
            if (c >= 0 && (!first || offset > 0)) {
                if (in->numPoints == RT_MAX_POINTS) {
                    carryParam(rt, next, id, last);
                    continue;
                }
                in->points[in->numPoints++] = (ParamPoint){ 0, 0, rt->carriedParams[cur][c].value };
                q->count++;
            }
            for (int32_t p = 0; p < points; p++) {
                if (hq->lpVtbl->getPoint(hq, p, &offset, &value) != Steinberg_kResultOk) continue;
                if (in->numPoints == RT_MAX_POINTS) {
                    carryParam(rt, next, id, last);
                    break;
                }
                in->points[in->numPoints++] = (ParamPoint){ 0, offset, value };
                q->count++;
            }
        }
    }
    for (int32_t c = 0; c < rt->numCarriedParams[cur]; c++) {
        if (taken[c]) continue;
        const Carried* cp = &rt->carriedParams[cur][c];
        ParamQueue* q = in->numPoints < RT_MAX_POINTS ? params_newQueue(in, cp->id) : NULL;
        if (!q) {
            carryParam(rt, next, cp->id, cp->value);
            continue;
        }
        in->points[in->numPoints++] = (ParamPoint){ 0, 0, cp->value };
        q->count = 1;
    }
    rt->cur = next;
}

// silence outputs zeros for a block that did not run.
static void silence(struct Steinberg_Vst_ProcessData* data, size_t bytes) {
    for (int32_t b = 0; b < data->numOutputs && data->outputs; b++) {
        struct Steinberg_Vst_AudioBusBuffers* bus = &data->outputs[b];
        void** ptrs = (void**)bus->Steinberg_Vst_AudioBusBuffers_channelBuffers32;
        for (int32_t ch = 0; ch < bus->numChannels && ptrs; ch++) {
            if (ptrs[ch]) memset(ptrs[ch], 0, (size_t)data->numSamples * bytes);
        }
        bus->silenceFlags = bus->numChannels >= 64 ? ~(Steinberg_uint64)0 : (((Steinberg_uint64)1 << bus->numChannels) - 1);
    }
}

// copyAudio hands the job's output audio to the host.
static void copyAudio(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data, size_t bytes) {
    for (int32_t b = 0; b < data->numOutputs; b++) {
        struct Steinberg_Vst_AudioBusBuffers* bus = &data->outputs[b];
        void** dst = (void**)bus->Steinberg_Vst_AudioBusBuffers_channelBuffers32;
        void** src = (void**)rt->outputs[b].Steinberg_Vst_AudioBusBuffers_channelBuffers32;
        for (int32_t ch = 0; ch < bus->numChannels && dst; ch++) {
            if (dst[ch]) memcpy(dst[ch], src[ch], (size_t)data->numSamples * bytes);
        }
        bus->silenceFlags = rt->outputs[b].silenceFlags;
    }
}

// copyLists hands the job's output parameter changes and events to the
// host. The event payloads stay in the job until the next block.
static void copyLists(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data) {
    struct Steinberg_Vst_IParameterChanges* params = data->outputParameterChanges;
    if (params && params->lpVtbl) {
        for (int32_t i = 0; i < rt->outParams.numPoints; i++) {
            const ParamPoint* p = &rt->outParams.points[i];
            Steinberg_Vst_ParamID id = rt->outParams.queues[p->queue].id;
            Steinberg_int32 index = 0;
            struct Steinberg_Vst_IParamValueQueue* q = params->lpVtbl->addParameterData(params, &id, &index);
            if (q && q->lpVtbl) q->lpVtbl->addPoint(q, p->offset, p->value, &index);
        }
    }
    struct Steinberg_Vst_IEventList* events = data->outputEvents;
    if (events && events->lpVtbl) {
        for (int32_t i = 0; i < rt->outEvents.count; i++) {
            events->lpVtbl->addEvent(events, &rt->outEvents.events[i]);
        }
    }
}

Steinberg_tresult vst3go_rt_process(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data) {
    size_t bytes = data->symbolicSampleSize == Steinberg_Vst_SymbolicSampleSizes_kSample64 ? sizeof(double) : sizeof(float);
    // Offline rendering has no deadline: it runs here once the worker is
    // free. A parameter flush (no samples) is a job like a block.
    int offline = data->processMode == Steinberg_Vst_ProcessModes_kOffline;
    if (offline) {
        rtguard_quiesce(rt->guard);
    } else if (!rtguard_idle(rt->guard)) {
        carry(rt, data);
        silence(data, bytes);
        return Steinberg_kResultOk;
    }

    copyInput(rt, data);
    int32_t used = 0;
    int here = offline || data->numSamples < 0 || data->numSamples > rt->maxSamples || data->symbolicSampleSize != rt->sampleSize ||
        !copyBuses(rt, data, bytes, &used);

    rt->data = *data;
    rt->data.inputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&rt->inParams;
    rt->data.outputParameterChanges = (struct Steinberg_Vst_IParameterChanges*)&rt->outParams;
    rt->data.inputEvents = (struct Steinberg_Vst_IEventList*)&rt->inEvents;
    rt->data.outputEvents = (struct Steinberg_Vst_IEventList*)&rt->outEvents;
    if (here) {
        // On the host's buffers.
        rtguard_fp fp = rtguard_flush_denormals();
        Steinberg_tresult result = GoAudioProcess(rt->goComponent, &rt->data);
        rtguard_restore_fp(fp);
        if (result == Steinberg_kResultOk) copyLists(rt, data);
        return result;
    }

    rt->data.inputs = data->numInputs > 0 ? rt->inputs : NULL;
    rt->data.outputs = data->numOutputs > 0 ? rt->outputs : NULL;
    if (data->processContext) {
        rt->context = *data->processContext;
        rt->data.processContext = &rt->context;
    }
    int32_t samples = data->numSamples > 0 ? data->numSamples : rt->maxSamples;
    int64_t timeout = (int64_t)(rt->share * (double)samples / rt->sampleRate * 1e9);
    if (rtguard_run(rt->guard, timeout) != RTGUARD_DONE) {
        silence(data, bytes);
        return Steinberg_kResultOk;
    }
    if (rt->result == Steinberg_kResultOk) {
        copyAudio(rt, data, bytes);
        copyLists(rt, data);
    }
    return rt->result;
}
