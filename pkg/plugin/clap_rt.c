#include "clap_rt.h"
#include "rtguard.h"

#include <stdlib.h>
#include <string.h>

extern clap_process_status GoClapProcess(uintptr_t id, clap_process_t* process);
extern void GoClapParamsFlush(uintptr_t id, clap_input_events_t* in, clap_output_events_t* out);
extern void GoClapReset(uintptr_t id);
extern void GoRTEnter(void);

// Capacities of a job. Events past them move to the next job; a block with
// more ports, channels or frames than the plugin was activated for (a host
// error) runs on the host's thread.
#define RT_MAX_PORTS 32
#define RT_MAX_EVENTS 4096
#define RT_ARENA (256 * 1024) // events and sysex payloads

// EventList is a clap_input_events or clap_output_events over copies of
// events in an arena.
typedef struct {
    union {
        clap_input_events_t in;
        clap_output_events_t out;
    };
    uint32_t count;
    uint32_t used;
    const clap_event_header_t* events[RT_MAX_EVENTS];
    _Alignas(8) uint8_t arena[RT_ARENA];
} EventList;

struct VST3GoClapRT {
    VST3GoRTGuard* guard;
    VST3GoClapPlugin* plugin;
    double sampleRate;
    uint32_t maxFrames;
    uint32_t channels;
    double share;
    clap_process_status result;

    // A reset requested on the audio thread, for the next job.
    int resetPending;

    // The job: a reset first when reset is set, then a block, or a
    // parameter flush when flush is set.
    int reset;
    int flush;
    clap_process_t process;
    clap_event_transport_t transport;
    clap_audio_buffer_t inputs[RT_MAX_PORTS];
    clap_audio_buffer_t outputs[RT_MAX_PORTS];
    void** channelPtrs; // channels pointers into audio
    uint8_t* audio;     // channels * maxFrames doubles
    EventList inEvents, outEvents;

    // Input events of skipped blocks and those that did not fit a job, for
    // the next job (audio thread only): carried[cur], and carried[cur ^ 1]
    // while the next job's input is copied.
    EventList carried[2];
    int cur;
};

static uint32_t CLAP_ABI events_size(const clap_input_events_t* list) {
    return ((const EventList*)list)->count;
}
static const clap_event_header_t* CLAP_ABI events_get(const clap_input_events_t* list, uint32_t index) {
    const EventList* l = (const EventList*)list;
    return index < l->count ? l->events[index] : NULL;
}

// alloc reserves size bytes, 8-aligned, in the list's arena.
static uint8_t* alloc(EventList* l, uint32_t size) {
    uint32_t aligned = (size + 7) & ~7u;
    if (aligned > RT_ARENA - l->used) return NULL;
    uint8_t* p = l->arena + l->used;
    l->used += aligned;
    return p;
}

// append copies an event and its sysex payload into l; zeroTime moves it to
// the start of the block.
static int append(EventList* l, const clap_event_header_t* e, int zeroTime) {
    if (!e || e->size < sizeof(clap_event_header_t) || l->count == RT_MAX_EVENTS) return 0;
    uint32_t mark = l->used;
    clap_event_header_t* copy = (clap_event_header_t*)alloc(l, e->size);
    if (!copy) return 0;
    memcpy(copy, e, e->size);
    if (zeroTime) copy->time = 0;
    if (copy->space_id == CLAP_CORE_EVENT_SPACE_ID && copy->type == CLAP_EVENT_MIDI_SYSEX && copy->size >= sizeof(clap_event_midi_sysex_t)) {
        clap_event_midi_sysex_t* sysex = (clap_event_midi_sysex_t*)copy;
        if (sysex->buffer && sysex->size > 0) {
            uint8_t* payload = alloc(l, sysex->size);
            if (!payload) {
                l->used = mark;
                return 0;
            }
            memcpy(payload, sysex->buffer, sysex->size);
            sysex->buffer = payload;
        }
    }
    l->events[l->count++] = copy;
    return 1;
}

static bool CLAP_ABI events_try_push(const clap_output_events_t* list, const clap_event_header_t* e) {
    return append((EventList*)list, e, 0);
}

static void resetIn(EventList* l) {
    l->in.ctx = l;
    l->in.size = events_size;
    l->in.get = events_get;
    l->count = 0;
    l->used = 0;
}

static void resetOut(EventList* l) {
    l->out.ctx = l;
    l->out.try_push = events_try_push;
    l->count = 0;
    l->used = 0;
}

// job runs the job on the calling thread.
static void job(VST3GoClapRT* rt) {
    uintptr_t id = rt->plugin->id;
    if (rt->reset) GoClapReset(id);
    if (rt->flush) {
        GoClapParamsFlush(id, &rt->inEvents.in, &rt->outEvents.out);
        return;
    }
    rt->plugin->sysexUsed = 0;
    rt->result = GoClapProcess(id, &rt->process);
}

// hostJob runs the job on the host's thread.
static void hostJob(VST3GoClapRT* rt) {
    rtguard_fp fp = rtguard_flush_denormals();
    job(rt);
    rtguard_restore_fp(fp);
}

// runJob is the worker's job.
static void runJob(void* arg) {
    VST3GoClapRT* rt = (VST3GoClapRT*)arg;
    GoRTEnter();
    rtguard_entered(rt->guard);
    job(rt);
}

// takeReset moves a requested reset into the job.
static void takeReset(VST3GoClapRT* rt) {
    rt->reset = rt->resetPending;
    rt->resetPending = 0;
}

VST3GoClapRT* vst3go_clap_rt_new(VST3GoClapPlugin* plugin, double sampleRate, uint32_t maxFrames, uint32_t channels, double share) {
    if (!(sampleRate > 0) || maxFrames == 0 || !(share > 0)) return NULL;
    VST3GoClapRT* rt = (VST3GoClapRT*)calloc(1, sizeof(VST3GoClapRT));
    if (!rt) return NULL;
    rt->plugin = plugin;
    rt->sampleRate = sampleRate;
    rt->maxFrames = maxFrames;
    rt->channels = channels;
    rt->share = share > 1 ? 1 : share;
    rt->channelPtrs = (void**)calloc(channels > 0 ? channels : 1, sizeof(void*));
    rt->audio = (uint8_t*)calloc(channels > 0 ? (size_t)channels * maxFrames : 1, sizeof(double));
    if (!rt->channelPtrs || !rt->audio) {
        vst3go_clap_rt_free(rt);
        return NULL;
    }
    resetIn(&rt->carried[0]);
    resetIn(&rt->carried[1]);
    rt->guard = rtguard_new(runJob, rt, GoRTEnter, (int64_t)((double)maxFrames / sampleRate * 1e9));
    if (!rt->guard) {
        vst3go_clap_rt_free(rt);
        return NULL;
    }
    return rt;
}

void vst3go_clap_rt_free(VST3GoClapRT* rt) {
    if (!rt) return;
    if (rt->guard) {
        rtguard_free(rt->guard);
        if (rt->resetPending) GoClapReset(rt->plugin->id);
    }
    free(rt->channelPtrs);
    free(rt->audio);
    free(rt);
}

void vst3go_clap_rt_reset(VST3GoClapRT* rt) {
    rt->resetPending = 1;
}

// copyPorts points the job's ports at its own audio memory, copying the
// input audio. It returns 0 when the block does not fit.
static int copyPorts(VST3GoClapRT* rt, const clap_process_t* proc) {
    uint32_t n = proc->frames_count;
    uint32_t used = 0;
    for (int dir = 0; dir < 2; dir++) {
        uint32_t count = dir == 0 ? proc->audio_inputs_count : proc->audio_outputs_count;
        const clap_audio_buffer_t* host = dir == 0 ? proc->audio_inputs : proc->audio_outputs;
        clap_audio_buffer_t* own = dir == 0 ? rt->inputs : rt->outputs;
        if (count > RT_MAX_PORTS || (count > 0 && !host)) return 0;
        for (uint32_t b = 0; b < count; b++) {
            uint32_t channels = host[b].channel_count;
            if (used + channels > rt->channels) return 0;
            int wide = host[b].data32 == NULL && host[b].data64 != NULL;
            size_t bytes = wide ? sizeof(double) : sizeof(float);
            void** src = wide ? (void**)host[b].data64 : (void**)host[b].data32;
            void** ptrs = rt->channelPtrs + used;
            own[b] = host[b];
            own[b].data32 = wide ? NULL : (float**)ptrs;
            own[b].data64 = wide ? (double**)ptrs : NULL;
            for (uint32_t ch = 0; ch < channels; ch++) {
                ptrs[ch] = rt->audio + (size_t)(used + ch) * rt->maxFrames * sizeof(double);
                if (dir == 0) {
                    if (src && src[ch]) {
                        memcpy(ptrs[ch], src[ch], n * bytes);
                    } else {
                        memset(ptrs[ch], 0, n * bytes);
                    }
                }
            }
            used += channels;
        }
    }
    return 1;
}

static void silence(const clap_process_t* proc) {
    for (uint32_t b = 0; b < proc->audio_outputs_count && proc->audio_outputs; b++) {
        clap_audio_buffer_t* port = &proc->audio_outputs[b];
        for (uint32_t ch = 0; ch < port->channel_count; ch++) {
            if (port->data32 && port->data32[ch]) memset(port->data32[ch], 0, proc->frames_count * sizeof(float));
            if (port->data64 && port->data64[ch]) memset(port->data64[ch], 0, proc->frames_count * sizeof(double));
        }
        port->constant_mask = port->channel_count >= 64 ? ~(uint64_t)0 : (((uint64_t)1 << port->channel_count) - 1);
    }
}

static void carry(VST3GoClapRT* rt, const clap_input_events_t* in) {
    uint32_t n = in ? in->size(in) : 0;
    for (uint32_t i = 0; i < n; i++) {
        append(&rt->carried[rt->cur], in->get(in, i), 1);
    }
}

// copyInput fills the job's input events, the carried ones first. Once one
// does not fit, it and the rest move to the start of the next job, in
// order.
static void copyInput(VST3GoClapRT* rt, const clap_input_events_t* in) {
    resetIn(&rt->inEvents);
    resetOut(&rt->outEvents);
    EventList* carried = &rt->carried[rt->cur];
    EventList* next = &rt->carried[rt->cur ^ 1];
    resetIn(next);
    int full = 0;
    for (uint32_t i = 0; i < carried->count; i++) {
        full = full || !append(&rt->inEvents, carried->events[i], 0);
        if (full) append(next, carried->events[i], 1);
    }
    uint32_t n = in ? in->size(in) : 0;
    for (uint32_t i = 0; i < n; i++) {
        const clap_event_header_t* e = in->get(in, i);
        full = full || !append(&rt->inEvents, e, 0);
        if (full) append(next, e, 1);
    }
    rt->cur ^= 1;
}

static void copyEvents(VST3GoClapRT* rt, const clap_output_events_t* out) {
    if (!out) return;
    // Sysex payloads stay in the job until the next block.
    for (uint32_t i = 0; i < rt->outEvents.count; i++) {
        out->try_push(out, rt->outEvents.events[i]);
    }
}

// timeoutFor is the deadline of a job for frames frames.
static int64_t timeoutFor(VST3GoClapRT* rt, uint32_t frames) {
    return (int64_t)(rt->share * (double)frames / rt->sampleRate * 1e9);
}

clap_process_status vst3go_clap_rt_process(VST3GoClapRT* rt, const clap_process_t* proc) {
    uint32_t n = proc->frames_count;
    int offline = __atomic_load_n(&rt->plugin->offline, __ATOMIC_ACQUIRE);
    if (offline) {
        // Offline rendering has no deadline: it runs here once the worker
        // is free.
        rtguard_quiesce(rt->guard);
    } else if (!rtguard_idle(rt->guard)) {
        carry(rt, proc->in_events);
        silence(proc);
        return CLAP_PROCESS_CONTINUE;
    }

    copyInput(rt, proc->in_events);
    takeReset(rt);
    rt->flush = 0;
    rt->process = *proc;
    rt->process.in_events = &rt->inEvents.in;
    rt->process.out_events = &rt->outEvents.out;
    if (offline || n > rt->maxFrames || !copyPorts(rt, proc)) {
        // On the host's buffers.
        hostJob(rt);
        copyEvents(rt, proc->out_events);
        return rt->result;
    }

    rt->process.audio_inputs = proc->audio_inputs_count > 0 ? rt->inputs : NULL;
    rt->process.audio_outputs = proc->audio_outputs_count > 0 ? rt->outputs : NULL;
    if (proc->transport) {
        rt->transport = *proc->transport;
        rt->process.transport = &rt->transport;
    }
    if (rtguard_run(rt->guard, timeoutFor(rt, n)) != RTGUARD_DONE) {
        silence(proc);
        return CLAP_PROCESS_CONTINUE;
    }
    for (uint32_t b = 0; b < proc->audio_outputs_count; b++) {
        clap_audio_buffer_t* port = &proc->audio_outputs[b];
        const clap_audio_buffer_t* own = &rt->outputs[b];
        for (uint32_t ch = 0; ch < port->channel_count; ch++) {
            if (port->data32 && port->data32[ch]) memcpy(port->data32[ch], own->data32[ch], n * sizeof(float));
            if (port->data64 && port->data64[ch]) memcpy(port->data64[ch], own->data64[ch], n * sizeof(double));
        }
        port->constant_mask = own->constant_mask;
    }
    copyEvents(rt, proc->out_events);
    return rt->result;
}

void vst3go_clap_rt_flush(VST3GoClapRT* rt, const clap_input_events_t* in, const clap_output_events_t* out) {
    int offline = __atomic_load_n(&rt->plugin->offline, __ATOMIC_ACQUIRE);
    if (offline) {
        rtguard_quiesce(rt->guard);
    } else if (!rtguard_idle(rt->guard)) {
        carry(rt, in);
        return;
    }
    copyInput(rt, in);
    takeReset(rt);
    rt->flush = 1;
    if (offline) {
        hostJob(rt);
    } else if (rtguard_run(rt->guard, timeoutFor(rt, rt->maxFrames)) != RTGUARD_DONE) {
        return;
    }
    copyEvents(rt, out);
}
