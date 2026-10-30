// Audio Unit (AUv2) front end. Each component instance hosts one instance
// of the plugin's CLAP entry (clap_bridge.c), so an Audio Unit runs the
// same runtime and threading a CLAP host does: AU properties map to CLAP
// extensions, AU parameters are the CLAP parameters (plain values), MIDI
// and parameter events become CLAP input events, and plugin edits and
// MIDI output go back to the host.
//
// The component factory is VST3GoAUFactory (named by the bundle's
// Info.plist, see scripts/build_au.sh). The Cocoa view factory class name
// comes from VST3GO_AU_VIEW_CLASS so plugins loaded into one process do
// not share Objective-C classes.
//
// AUv3 extensions (auv3_darwin.c, macOS and iOS) register the same
// component in their own process (vst3go_au_register) and wrap it in an
// AUAudioUnitV2Bridge; their view controller shows vst3go_au_open_view.

#include <TargetConditionals.h>
#if TARGET_OS_OSX
#import <Cocoa/Cocoa.h>
#include <AudioToolbox/AUCocoaUIView.h>
typedef NSView AUNativeView;
#else
#import <UIKit/UIKit.h>
typedef UIView AUNativeView;
#endif
#include <AudioToolbox/AudioToolbox.h>
#include <Block.h>
#include <CoreMIDI/CoreMIDI.h>
#include <dispatch/dispatch.h>
#include <dlfcn.h>
#include <math.h>
#include <os/lock.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

#include "clap_bridge.h"
#include "../../include/ara/ARACLAP.h"
#include "../../include/ara/ARAAudioUnit.h"

#ifndef VST3GO_AU_VIEW_CLASS
#define VST3GO_AU_VIEW_CLASS VST3GoAUView
#endif
#define AU_CAT2(a, b) a##b
#define AU_CAT(a, b) AU_CAT2(a, b)
#define AU_STR2(a) #a
#define AU_STR(a) AU_STR2(a)
#define AU_FACTORY_CLASS VST3GO_AU_VIEW_CLASS
#define AU_CONTAINER_CLASS AU_CAT(VST3GO_AU_VIEW_CLASS, Container)

extern const clap_plugin_entry_t clap_entry;
extern uint32_t GoClapPluginCount(void);
extern clap_plugin_descriptor_t* GoClapDescriptor(uint32_t index);
extern bool GoAUComponentCodes(uint32_t index, uint32_t* type, uint32_t* subtype, uint32_t* manufacturer);
#if TARGET_OS_OSX
extern void VST3GoRestoreFileAccess(void);  // editor_view_darwin.c
#endif

// The view factory reads the instance behind an AudioUnit handle through
// this private property.
enum { kVST3GoAUProperty_Instance = 64017 };

enum {
    AU_MAX_PORTS = 16,
    AU_MAX_QUEUE = 1024,      // host events (MIDI, scheduled parameters) per block
    AU_SYSEX_BYTES = 65536,   // queued SysEx payload per block
    AU_MAX_MIDI_OUT = 1024,
    AU_MAX_NOTICES = 1024,    // plugin edits waiting for the main thread
    AU_MAX_LISTENERS = 64,
    AU_MAX_RENDER_NOTIFY = 16,
    AU_DEFAULT_MAX_FRAMES = 1156,  // kAUDefaultMaxFramesPerSlice
    AU_PROGRAM_PARAM_ID = 0x7EFFFFFF,
    AU_MAX_LAYOUT_CHANNELS = 8,   // main bus channel counts offered to hosts
    AU_MAX_CHANNEL_INFOS = AU_MAX_LAYOUT_CHANNELS * AU_MAX_LAYOUT_CHANNELS + 1,
};

typedef struct {
    UInt32 channels;
    Float64 sampleRate;
    CFStringRef name;
    UInt32 shouldAllocate;
    float** data;     // CLAP channel pointers
    float* storage;   // channels * maxFrames
    AudioBufferList* pull;
    AURenderCallbackStruct callback;
    AudioUnitConnection connection;
    bool connected;
} AUElement;

enum { QUEUED_PARAM, QUEUED_MIDI, QUEUED_MIDI2, QUEUED_SYSEX };

typedef struct {
    uint32_t offset;
    uint8_t kind;
    uint8_t midi[3];
    uint32_t ump[2];  // QUEUED_MIDI2: a 32- or 64-bit Universal MIDI Packet
    clap_id param;
    double value;
    uint32_t sysexStart, sysexSize;
} AUQueued;

enum { NOTICE_VALUE, NOTICE_BEGIN, NOTICE_END };

typedef struct {
    uint8_t kind;
    clap_id param;
} AUNotice;

@class AU_CONTAINER_CLASS;

typedef struct AUInstance {
    AudioComponentPlugInInterface iface;  // first: the instance is the plug-in interface
    AudioComponentInstance ci;
    AudioComponentDescription desc;
    uint32_t index;
    uint64_t serial;
    struct AUInstance* nextLive;

    clap_host_t host;
    const clap_plugin_t* plugin;
    const clap_plugin_params_t* params;
    const clap_plugin_state_t* state;
    const clap_plugin_audio_ports_t* audioPorts;
    const clap_plugin_note_ports_t* notePorts;
    const clap_plugin_latency_t* latency;
    const clap_plugin_tail_t* tail;
    const clap_plugin_gui_t* gui;
    const clap_plugin_render_t* render;
    const clap_plugin_configurable_audio_ports_t* configPorts;
    const clap_ara_plugin_extension_t* ara; // NULL unless the plugin supports ARA
    bool noteIn, noteOut;
    // The main bus channel counts the plugin accepts (SupportedNumChannels),
    // the current ones first.
    AUChannelInfo channelInfos[AU_MAX_CHANNEL_INFOS];
    uint32_t channelInfoCount;

    AUElement ins[AU_MAX_PORTS], outs[AU_MAX_PORTS];
    clap_audio_buffer_t inBuffers[AU_MAX_PORTS], outBuffers[AU_MAX_PORTS];
    uint32_t inCount, outCount;
    UInt32 maxFrames;
    // The rate of a unit without audio elements (a MIDI processor): the
    // host's global sample rate, or the output device's at initialize
    // while the host has set none.
    Float64 globalRate;
    bool globalRateSet;
    bool initialized;
    bool processing;  // render lock
    int64_t steadyTime;
    Float64 lastSampleTime;
    bool rendered;
    UInt32 bypass, offline, inPlace;
    OSStatus lastError;

    // Parameters: the host-visible values are cached so GetParameter is
    // wait-free; host changes are flagged dirty and sent with the next
    // block (or flush while the unit is uninitialized).
    uint32_t paramCount;
    clap_param_info_t* paramInfo;
    _Atomic(double)* values;
    _Atomic(double)* pending;
    atomic_bool* dirty;
    atomic_bool anyDirty;
    int32_t programParam;  // index of the program list parameter, -1: none
    AUPreset* factoryPresets;
    uint32_t factoryPresetCount;
    AUPreset preset;

    os_unfair_lock renderLock, queueLock, noticeLock, listenerLock;
    AUQueued queue[AU_MAX_QUEUE];
    uint32_t queueCount;
    uint8_t sysexQueue[AU_SYSEX_BYTES];
    uint32_t sysexQueueUsed;
    // render-thread copies
    AUQueued work[AU_MAX_QUEUE];
    uint8_t sysexWork[AU_SYSEX_BYTES];
    clap_event_param_value_t* paramEvents;  // paramCount + AU_MAX_QUEUE
    clap_event_midi_t midiEvents[AU_MAX_QUEUE];
    clap_event_midi2_t midi2Events[AU_MAX_QUEUE];
    clap_event_midi_sysex_t sysexEvents[AU_MAX_QUEUE];
    const clap_event_header_t** events;  // paramCount + AU_MAX_QUEUE
    uint32_t eventCount;
    clap_event_transport_t transport;

    // MIDI output as MIDI 1.0 bytes and as a MIDI 2.0 channel voice message
    // (ump2[0] == 0 when it has none), for the event list callback.
    struct { uint32_t time; uint8_t data[3]; uint8_t size; uint32_t ump2[2]; } midiOut[AU_MAX_MIDI_OUT];
    uint32_t midiOutCount;
    AUMIDIOutputCallbackStruct midiOutCallback;
    AUMIDIEventListBlock midiOutList;  // copied
    SInt32 hostProtocol;               // the protocol midiOutList wants
    // SysEx arriving in MIDI 1.0 data packets (UMP type 3), host thread
    uint8_t sysexIn[AU_SYSEX_BYTES];
    uint32_t sysexInSize;

    AUNotice notices[AU_MAX_NOTICES];
    uint32_t noticeCount;
    atomic_bool noticeOverflow;

    atomic_bool mainScheduled, callbackRequested, restartRequested, flushRequested;
    atomic_bool valuesChanged, infoChanged, latencyChanged;
    atomic_uint_least64_t pendingSize;  // width << 32 | height

    struct { AudioUnitPropertyID id; AudioUnitPropertyListenerProc proc; void* data; } listeners[AU_MAX_LISTENERS];
    uint32_t listenerCount;
    struct { AURenderCallback proc; void* data; } renderNotify[AU_MAX_RENDER_NOTIFY];
    uint32_t renderNotifyCount;
    HostCallbackInfo hostCallbacks;

    AU_CONTAINER_CLASS* view;  // open editor, not retained
    bool guiCreated, guiResizing;
} AUInstance;

static _Thread_local bool inAudioThread;

// ---- live instances ----

// Main-thread work is dispatched with the instance serial and runs under
// liveLock, so it never reaches a closed instance.
static pthread_mutex_t liveLock = PTHREAD_RECURSIVE_MUTEX_INITIALIZER;
static AUInstance* live;
static uint64_t nextSerial;

static void run_main(AUInstance* a);

static void main_job(void* ctx) {
    uint64_t serial = (uint64_t)(uintptr_t)ctx;
    pthread_mutex_lock(&liveLock);
    for (AUInstance* a = live; a; a = a->nextLive) {
        if (a->serial == serial) {
            run_main(a);
            break;
        }
    }
    pthread_mutex_unlock(&liveLock);
}

static void schedule_main(AUInstance* a) {
    if (!atomic_exchange(&a->mainScheduled, true)) {
        dispatch_async_f(dispatch_get_main_queue(), (void*)(uintptr_t)a->serial, main_job);
    }
}

// ---- helpers ----

static Float64 au_sample_rate(AUInstance* a) {
    if (a->outCount) return a->outs[0].sampleRate;
    if (a->inCount) return a->ins[0].sampleRate;
    return a->globalRate;
}

// output_device_rate is the default output device's nominal rate, which
// the host renders at, or 0 (no device, iOS).
static Float64 output_device_rate(void) {
#if TARGET_OS_OSX
    AudioObjectPropertyAddress address = { kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal,
                                           kAudioObjectPropertyElementMain };
    AudioObjectID device = kAudioObjectUnknown;
    UInt32 size = sizeof(device);
    if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &address, 0, NULL, &size, &device) != noErr ||
        device == kAudioObjectUnknown) {
        return 0;
    }
    address.mSelector = kAudioDevicePropertyNominalSampleRate;
    Float64 rate = 0;
    size = sizeof(rate);
    if (AudioObjectGetPropertyData(device, &address, 0, NULL, &size, &rate) != noErr) return 0;
    return rate;
#else
    return 0;
#endif
}

static int32_t param_index(AUInstance* a, AudioUnitParameterID id) {
    for (uint32_t i = 0; i < a->paramCount; i++) {
        if (a->paramInfo[i].id == id) return (int32_t)i;
    }
    return -1;
}

static bool param_stepped(const clap_param_info_t* info) {
    return (info->flags & CLAP_PARAM_IS_STEPPED) != 0;
}

static void fire_property(AUInstance* a, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem) {
    struct { AudioUnitPropertyListenerProc proc; void* data; } calls[AU_MAX_LISTENERS];
    uint32_t n = 0;
    os_unfair_lock_lock(&a->listenerLock);
    for (uint32_t i = 0; i < a->listenerCount; i++) {
        if (a->listeners[i].id == id) {
            calls[n].proc = a->listeners[i].proc;
            calls[n].data = a->listeners[i].data;
            n++;
        }
    }
    os_unfair_lock_unlock(&a->listenerLock);
    for (uint32_t i = 0; i < n; i++) calls[i].proc(calls[i].data, a->ci, id, scope, elem);
}

static void add_notice(AUInstance* a, uint8_t kind, clap_id param) {
    os_unfair_lock_lock(&a->noticeLock);
    if (a->noticeCount < AU_MAX_NOTICES) {
        a->notices[a->noticeCount++] = (AUNotice){ kind, param };
    } else {
        atomic_store(&a->noticeOverflow, true);
    }
    os_unfair_lock_unlock(&a->noticeLock);
}

static void notify_parameter(AUInstance* a, AudioUnitEventType type, AudioUnitParameterID id) {
    AudioUnitEvent event;
    memset(&event, 0, sizeof(event));
    event.mEventType = type;
    event.mArgument.mParameter.mAudioUnit = a->ci;
    event.mArgument.mParameter.mParameterID = id;
    event.mArgument.mParameter.mScope = kAudioUnitScope_Global;
    event.mArgument.mParameter.mElement = 0;
    AUEventListenerNotify(NULL, NULL, &event);
}

static void refresh_values(AUInstance* a) {
    for (uint32_t i = 0; i < a->paramCount; i++) {
        double value;
        if (a->params->get_value(a->plugin, a->paramInfo[i].id, &value)) atomic_store(&a->values[i], value);
    }
}

// ---- host ----

static AUInstance* host_instance(const clap_host_t* h) { return (AUInstance*)h->host_data; }

static void host_log(const clap_host_t* h, clap_log_severity severity, const char* msg) {
    (void)h;
    if (severity >= CLAP_LOG_WARNING) NSLog(@"vst3go audio unit: %s", msg ? msg : "");
}
static const clap_host_log_t hostLog = { host_log };

static bool host_is_main_thread(const clap_host_t* h) { (void)h; return pthread_main_np() != 0; }
static bool host_is_audio_thread(const clap_host_t* h) { (void)h; return inAudioThread; }
static const clap_host_thread_check_t hostThreadCheck = { host_is_main_thread, host_is_audio_thread };

static void host_params_rescan(const clap_host_t* h, clap_param_rescan_flags flags) {
    AUInstance* a = host_instance(h);
    if (flags & CLAP_PARAM_RESCAN_VALUES) atomic_store(&a->valuesChanged, true);
    if (flags & (CLAP_PARAM_RESCAN_INFO | CLAP_PARAM_RESCAN_TEXT | CLAP_PARAM_RESCAN_ALL)) atomic_store(&a->infoChanged, true);
    schedule_main(a);
}
static void host_params_clear(const clap_host_t* h, clap_id id, clap_param_clear_flags flags) { (void)h; (void)id; (void)flags; }
static void host_params_request_flush(const clap_host_t* h) {
    AUInstance* a = host_instance(h);
    atomic_store(&a->flushRequested, true);
    schedule_main(a);
}
static const clap_host_params_t hostParams = { host_params_rescan, host_params_clear, host_params_request_flush };

static void host_latency_changed(const clap_host_t* h) {
    AUInstance* a = host_instance(h);
    atomic_store(&a->latencyChanged, true);
    schedule_main(a);
}
static const clap_host_latency_t hostLatency = { host_latency_changed };

static void host_state_mark_dirty(const clap_host_t* h) { (void)h; }
static const clap_host_state_t hostState = { host_state_mark_dirty };

static bool host_ports_rescan_supported(const clap_host_t* h, uint32_t flag) { (void)h; (void)flag; return false; }
static void host_ports_rescan(const clap_host_t* h, uint32_t flags) { (void)h; (void)flags; }
static const clap_host_audio_ports_t hostAudioPorts = { host_ports_rescan_supported, host_ports_rescan };

static uint32_t host_note_dialects(const clap_host_t* h) { (void)h; return CLAP_NOTE_DIALECT_MIDI | CLAP_NOTE_DIALECT_CLAP; }
static void host_note_rescan(const clap_host_t* h, uint32_t flags) { (void)h; (void)flags; }
static const clap_host_note_ports_t hostNotePorts = { host_note_dialects, host_note_rescan };

static void resize_view(AUInstance* a, uint32_t width, uint32_t height);

static void host_gui_resize_hints_changed(const clap_host_t* h) { (void)h; }
static bool host_gui_request_resize(const clap_host_t* h, uint32_t width, uint32_t height) {
    AUInstance* a = host_instance(h);
    if (!width || !height) return false;
    if (pthread_main_np()) {
        resize_view(a, width, height);
    } else {
        atomic_store(&a->pendingSize, (uint64_t)width << 32 | height);
        schedule_main(a);
    }
    return true;
}
static bool host_gui_request_show(const clap_host_t* h) { (void)h; return false; }
static bool host_gui_request_hide(const clap_host_t* h) { (void)h; return false; }
static void host_gui_closed(const clap_host_t* h, bool destroyed) { (void)h; (void)destroyed; }
static const clap_host_gui_t hostGui = {
    host_gui_resize_hints_changed, host_gui_request_resize, host_gui_request_show, host_gui_request_hide, host_gui_closed
};

static const void* host_get_extension(const clap_host_t* h, const char* id) {
    (void)h;
    if (!id) return NULL;
    if (!strcmp(id, CLAP_EXT_LOG)) return &hostLog;
    if (!strcmp(id, CLAP_EXT_THREAD_CHECK)) return &hostThreadCheck;
    if (!strcmp(id, CLAP_EXT_PARAMS)) return &hostParams;
    if (!strcmp(id, CLAP_EXT_LATENCY)) return &hostLatency;
    if (!strcmp(id, CLAP_EXT_STATE)) return &hostState;
    if (!strcmp(id, CLAP_EXT_AUDIO_PORTS)) return &hostAudioPorts;
    if (!strcmp(id, CLAP_EXT_NOTE_PORTS)) return &hostNotePorts;
    if (!strcmp(id, CLAP_EXT_GUI)) return &hostGui;
    return NULL;
}

static void host_request_restart(const clap_host_t* h) {
    AUInstance* a = host_instance(h);
    atomic_store(&a->restartRequested, true);
    schedule_main(a);
}
static void host_request_process(const clap_host_t* h) { (void)h; }
static void host_request_callback(const clap_host_t* h) {
    AUInstance* a = host_instance(h);
    atomic_store(&a->callbackRequested, true);
    schedule_main(a);
}

// ---- events ----

static uint32_t in_size(const clap_input_events_t* list) { return ((AUInstance*)list->ctx)->eventCount; }
static const clap_event_header_t* in_get(const clap_input_events_t* list, uint32_t index) {
    AUInstance* a = (AUInstance*)list->ctx;
    return index < a->eventCount ? a->events[index] : NULL;
}

// midi2_scale converts a MIDI 1.0 value of srcBits to dstBits the way the
// MIDI 2.0 translation rules do: zero, center and full scale map exactly.
static uint32_t midi2_scale(uint32_t value, int srcBits, int dstBits) {
    int scaleBits = dstBits - srcBits;
    uint32_t shifted = value << scaleBits;
    if (value <= 1u << (srcBits - 1)) return shifted;
    int repeatBits = srcBits - 1;
    uint32_t repeat = value & ((1u << repeatBits) - 1);
    repeat = scaleBits > repeatBits ? repeat << (scaleBits - repeatBits) : repeat >> (repeatBits - scaleBits);
    while (repeat) {
        shifted |= repeat;
        repeat >>= repeatBits;
    }
    return shifted;
}

// midi2_from_midi1 translates a MIDI 1.0 channel voice message to MIDI 2.0
// (group 0); false for other messages.
static bool midi2_from_midi1(const uint8_t* data, uint8_t size, uint32_t ump[2]) {
    uint8_t status = data[0] & 0xF0, d1 = size > 1 ? data[1] & 0x7F : 0, d2 = size > 2 ? data[2] & 0x7F : 0;
    if (status < 0x80 || status == 0xF0) return false;
    if (status == 0x90 && d2 == 0) status = 0x80;
    uint32_t head = 0x40000000u | (uint32_t)(status | (data[0] & 0x0F)) << 16;
    switch (status) {
    case 0x80:
    case 0x90:
        ump[0] = head | (uint32_t)d1 << 8;
        ump[1] = midi2_scale(d2, 7, 16) << 16;
        return true;
    case 0xA0:
    case 0xB0:
        ump[0] = head | (uint32_t)d1 << 8;
        ump[1] = midi2_scale(d2, 7, 32);
        return true;
    case 0xC0:
        ump[0] = head;
        ump[1] = (uint32_t)d1 << 24;
        return true;
    case 0xD0:
        ump[0] = head;
        ump[1] = midi2_scale(d1, 7, 32);
        return true;
    case 0xE0:
        ump[0] = head;
        ump[1] = midi2_scale((uint32_t)d2 << 7 | d1, 14, 32);
        return true;
    }
    return false;
}

static void push_midi_out(AUInstance* a, uint32_t time, const uint8_t* data, uint8_t size, const uint32_t* ump2) {
    if (!inAudioThread || a->midiOutCount >= AU_MAX_MIDI_OUT) return;
    typeof(a->midiOut[0])* m = &a->midiOut[a->midiOutCount];
    m->time = time;
    memcpy(m->data, data, size);
    m->size = size;
    m->ump2[0] = m->ump2[1] = 0;
    if (ump2) {
        m->ump2[0] = ump2[0];
        m->ump2[1] = ump2[1];
    } else {
        midi2_from_midi1(data, size, m->ump2);
    }
    a->midiOutCount++;
}

static uint8_t midi_size(uint8_t status) {
    switch (status & 0xF0) {
    case 0xC0:
    case 0xD0:
        return 2;
    case 0xF0:
        return status == 0xF1 || status == 0xF3 ? 2 : status == 0xF2 ? 3 : 1;
    default:
        return 3;
    }
}

static bool out_try_push(const clap_output_events_t* list, const clap_event_header_t* e) {
    AUInstance* a = (AUInstance*)list->ctx;
    if (e->space_id != CLAP_CORE_EVENT_SPACE_ID) return true;
    switch (e->type) {
    case CLAP_EVENT_PARAM_VALUE: {
        const clap_event_param_value_t* v = (const clap_event_param_value_t*)e;
        int32_t i = param_index(a, v->param_id);
        if (i >= 0) {
            atomic_store(&a->values[i], v->value);
            add_notice(a, NOTICE_VALUE, v->param_id);
        }
        break;
    }
    case CLAP_EVENT_PARAM_GESTURE_BEGIN:
    case CLAP_EVENT_PARAM_GESTURE_END: {
        const clap_event_param_gesture_t* g = (const clap_event_param_gesture_t*)e;
        add_notice(a, e->type == CLAP_EVENT_PARAM_GESTURE_BEGIN ? NOTICE_BEGIN : NOTICE_END, g->param_id);
        break;
    }
    case CLAP_EVENT_MIDI: {
        const clap_event_midi_t* m = (const clap_event_midi_t*)e;
        push_midi_out(a, e->time, m->data, midi_size(m->data[0]), NULL);
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
        // MIDI 2.0 keeps the velocity at 16 bits.
        uint32_t velocity16 = (uint32_t)lround(fmin(fmax(n->velocity, 0), 1) * 65535);
        if (e->type == CLAP_EVENT_NOTE_ON && velocity16 < 1) velocity16 = 1;
        uint32_t ump2[2] = { 0x40000000u | (uint32_t)data[0] << 16 | (uint32_t)data[1] << 8, velocity16 << 16 };
        push_midi_out(a, e->time, data, 3, ump2);
        break;
    }
    }
    return true;
}

// collect_dirty adds the parameters the host changed since the last block
// as events at time 0.
static void collect_dirty(AUInstance* a) {
    if (!atomic_exchange(&a->anyDirty, false)) return;
    for (uint32_t i = 0; i < a->paramCount; i++) {
        if (!atomic_exchange(&a->dirty[i], false)) continue;
        clap_event_param_value_t* ev = &a->paramEvents[a->eventCount];
        *ev = (clap_event_param_value_t){ { sizeof(*ev), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 },
                                          a->paramInfo[i].id, NULL, -1, -1, -1, -1, atomic_load(&a->pending[i]) };
        a->events[a->eventCount++] = &ev->header;
    }
}

// flush_params sends dirty parameters through params.flush. Render lock held.
static void flush_params(AUInstance* a) {
    a->eventCount = 0;
    collect_dirty(a);
    clap_input_events_t in = { a, in_size, in_get };
    clap_output_events_t out = { a, out_try_push };
    a->params->flush(a->plugin, &in, &out);
    a->eventCount = 0;
}

static void queue_event(AUInstance* a, AUQueued event, const uint8_t* sysex) {
    os_unfair_lock_lock(&a->queueLock);
    if (a->queueCount < AU_MAX_QUEUE) {
        if (event.kind == QUEUED_SYSEX) {
            if (event.sysexSize > AU_SYSEX_BYTES - a->sysexQueueUsed) {
                os_unfair_lock_unlock(&a->queueLock);
                return;
            }
            memcpy(a->sysexQueue + a->sysexQueueUsed, sysex, event.sysexSize);
            event.sysexStart = a->sysexQueueUsed;
            a->sysexQueueUsed += event.sysexSize;
        }
        a->queue[a->queueCount++] = event;
    }
    os_unfair_lock_unlock(&a->queueLock);
}

// collect_queue moves the queued host events into the block, at offsets
// inside it, in time order.
static void collect_queue(AUInstance* a, UInt32 frames) {
    os_unfair_lock_lock(&a->queueLock);
    uint32_t count = a->queueCount;
    memcpy(a->work, a->queue, sizeof(AUQueued) * count);
    memcpy(a->sysexWork, a->sysexQueue, a->sysexQueueUsed);
    a->queueCount = 0;
    a->sysexQueueUsed = 0;
    os_unfair_lock_unlock(&a->queueLock);

    uint32_t params = a->eventCount;  // dirty values sit at time 0
    for (uint32_t i = 0; i < count; i++) {
        const AUQueued* q = &a->work[i];
        uint32_t time = q->offset < frames ? q->offset : frames - 1;
        const clap_event_header_t* header = NULL;
        switch (q->kind) {
        case QUEUED_PARAM: {
            clap_event_param_value_t* ev = &a->paramEvents[params++];
            *ev = (clap_event_param_value_t){ { sizeof(*ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0 },
                                              q->param, NULL, -1, -1, -1, -1, q->value };
            header = &ev->header;
            break;
        }
        case QUEUED_MIDI: {
            if (!a->noteIn) continue;
            clap_event_midi_t* ev = &a->midiEvents[i];
            *ev = (clap_event_midi_t){ { sizeof(*ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0 }, 0, { q->midi[0], q->midi[1], q->midi[2] } };
            header = &ev->header;
            break;
        }
        case QUEUED_MIDI2: {
            if (!a->noteIn) continue;
            clap_event_midi2_t* ev = &a->midi2Events[i];
            *ev = (clap_event_midi2_t){ { sizeof(*ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI2, 0 }, 0, { q->ump[0], q->ump[1], 0, 0 } };
            header = &ev->header;
            break;
        }
        case QUEUED_SYSEX: {
            if (!a->noteIn) continue;
            clap_event_midi_sysex_t* ev = &a->sysexEvents[i];
            *ev = (clap_event_midi_sysex_t){ { sizeof(*ev), time, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI_SYSEX, 0 }, 0,
                                             a->sysexWork + q->sysexStart, q->sysexSize };
            header = &ev->header;
            break;
        }
        }
        // Insertion keeps events of equal time in arrival order.
        uint32_t j = a->eventCount;
        while (j > 0 && a->events[j - 1]->time > time) {
            a->events[j] = a->events[j - 1];
            j--;
        }
        a->events[j] = header;
        a->eventCount++;
    }
}

static const clap_event_transport_t* collect_transport(AUInstance* a) {
    HostCallbackInfo* hc = &a->hostCallbacks;
    if (!hc->beatAndTempoProc && !hc->transportStateProc && !hc->transportStateProc2 && !hc->musicalTimeLocationProc) return NULL;
    clap_event_transport_t* t = &a->transport;
    memset(t, 0, sizeof(*t));
    t->header = (clap_event_header_t){ sizeof(*t), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_TRANSPORT, 0 };
    Float64 beat = 0, tempo = 0;
    if (hc->beatAndTempoProc && hc->beatAndTempoProc(hc->hostUserData, &beat, &tempo) == noErr) {
        t->flags |= CLAP_TRANSPORT_HAS_TEMPO | CLAP_TRANSPORT_HAS_BEATS_TIMELINE;
        t->tempo = tempo;
        t->song_pos_beats = (clap_beattime)llround(beat * CLAP_BEATTIME_FACTOR);
    }
    UInt32 toNextBeat = 0, denominator = 0;
    Float32 numerator = 0;
    Float64 downBeat = 0;
    if (hc->musicalTimeLocationProc &&
        hc->musicalTimeLocationProc(hc->hostUserData, &toNextBeat, &numerator, &denominator, &downBeat) == noErr && numerator > 0 && denominator > 0) {
        t->flags |= CLAP_TRANSPORT_HAS_TIME_SIGNATURE;
        t->tsig_num = (uint16_t)numerator;
        t->tsig_denom = (uint16_t)denominator;
        t->bar_start = (clap_beattime)llround(downBeat * CLAP_BEATTIME_FACTOR);
        t->bar_number = (int32_t)floor(downBeat / (numerator * 4.0 / denominator) + 0.5);
    }
    Boolean playing = false, recording = false, changed = false, cycling = false;
    Float64 sample = 0, cycleStart = 0, cycleEnd = 0;
    OSStatus status = kAudioUnitErr_CannotDoInCurrentContext;
    if (hc->transportStateProc2) {
        status = hc->transportStateProc2(hc->hostUserData, &playing, &recording, &changed, &sample, &cycling, &cycleStart, &cycleEnd);
    } else if (hc->transportStateProc) {
        status = hc->transportStateProc(hc->hostUserData, &playing, &changed, &sample, &cycling, &cycleStart, &cycleEnd);
    }
    if (status == noErr) {
        if (playing) t->flags |= CLAP_TRANSPORT_IS_PLAYING;
        if (recording) t->flags |= CLAP_TRANSPORT_IS_RECORDING;
        if (cycling) {
            t->flags |= CLAP_TRANSPORT_IS_LOOP_ACTIVE;
            t->loop_start_beats = (clap_beattime)llround(cycleStart * CLAP_BEATTIME_FACTOR);
            t->loop_end_beats = (clap_beattime)llround(cycleEnd * CLAP_BEATTIME_FACTOR);
        }
        t->flags |= CLAP_TRANSPORT_HAS_SECONDS_TIMELINE;
        t->song_pos_seconds = (clap_sectime)llround(sample / au_sample_rate(a) * CLAP_SECTIME_FACTOR);
    }
    return t;
}

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static void send_midi_out(AUInstance* a, const AudioTimeStamp* ts) {
    if (!a->midiOutCount) return;
    if (a->midiOutList) {
        // One list per message: a list holds packets of one timestamp
        // order, and the host's buffer is sized for single events.
        bool midi2 = a->hostProtocol == kMIDIProtocol_2_0;
        for (uint32_t i = 0; i < a->midiOutCount; i++) {
            Byte buffer[sizeof(MIDIEventList) + 16];
            MIDIEventList* list = (MIDIEventList*)buffer;
            MIDIEventPacket* packet = MIDIEventListInit(list, midi2 ? kMIDIProtocol_2_0 : kMIDIProtocol_1_0);
            const typeof(a->midiOut[0])* m = &a->midiOut[i];
            if (midi2 && m->ump2[0]) {
                packet = MIDIEventListAdd(list, sizeof(buffer), packet, m->time, 2, m->ump2);
            } else if (m->size) {
                uint32_t word = 0x20000000u | (uint32_t)m->data[0] << 16 | (uint32_t)(m->size > 1 ? m->data[1] : 0) << 8 | (m->size > 2 ? m->data[2] : 0);
                packet = MIDIEventListAdd(list, sizeof(buffer), packet, m->time, 1, &word);
            }
            if (packet) a->midiOutList((AUEventSampleTime)ts->mSampleTime, 0, list);
        }
        return;
    }
    if (!a->midiOutCallback.midiOutputCallback) return;
    Byte buffer[8192];
    MIDIPacketList* list = (MIDIPacketList*)buffer;
    MIDIPacket* packet = MIDIPacketListInit(list);
    for (uint32_t i = 0; i < a->midiOutCount && packet; i++) {
        packet = MIDIPacketListAdd(list, sizeof(buffer), packet, a->midiOut[i].time, a->midiOut[i].size, a->midiOut[i].data);
    }
    a->midiOutCallback.midiOutputCallback(a->midiOutCallback.userData, ts, 0, list);
}
#pragma clang diagnostic pop

// ---- buffers ----

static void free_element(AUElement* e) {
    free(e->data);
    free(e->storage);
    free(e->pull);
    e->data = NULL;
    e->storage = NULL;
    e->pull = NULL;
}

static bool alloc_element(AUElement* e, UInt32 maxFrames) {
    UInt32 channels = e->channels ? e->channels : 1;
    e->data = (float**)calloc(channels, sizeof(float*));
    e->storage = (float*)calloc((size_t)channels * maxFrames, sizeof(float));
    e->pull = (AudioBufferList*)calloc(1, offsetof(AudioBufferList, mBuffers) + sizeof(AudioBuffer) * channels);
    if (!e->data || !e->storage || !e->pull) return false;
    for (UInt32 ch = 0; ch < channels; ch++) e->data[ch] = e->storage + (size_t)ch * maxFrames;
    return true;
}

static void free_buffers(AUInstance* a) {
    for (uint32_t i = 0; i < a->inCount; i++) free_element(&a->ins[i]);
    for (uint32_t i = 0; i < a->outCount; i++) free_element(&a->outs[i]);
}

static bool alloc_buffers(AUInstance* a) {
    for (uint32_t i = 0; i < a->inCount; i++) {
        if (!alloc_element(&a->ins[i], a->maxFrames)) return false;
        a->inBuffers[i] = (clap_audio_buffer_t){ a->ins[i].data, NULL, a->ins[i].channels, 0, 0 };
    }
    for (uint32_t i = 0; i < a->outCount; i++) {
        if (!alloc_element(&a->outs[i], a->maxFrames)) return false;
        a->outBuffers[i] = (clap_audio_buffer_t){ a->outs[i].data, NULL, a->outs[i].channels, 0, 0 };
    }
    return true;
}

// ---- render ----

static OSStatus pull_inputs(AUInstance* a, const AudioTimeStamp* ts, UInt32 frames) {
    for (uint32_t i = 0; i < a->inCount; i++) {
        AUElement* in = &a->ins[i];
        AudioBufferList* list = in->pull;
        list->mNumberBuffers = in->channels;
        for (UInt32 ch = 0; ch < in->channels; ch++) {
            float* storage = in->storage + (size_t)ch * a->maxFrames;
            list->mBuffers[ch] = (AudioBuffer){ 1, frames * (UInt32)sizeof(float), storage };
            in->data[ch] = storage;
        }
        OSStatus status = noErr;
        AudioUnitRenderActionFlags flags = 0;
        if (in->callback.inputProc) {
            status = in->callback.inputProc(in->callback.inputProcRefCon, &flags, ts, i, frames, list);
        } else if (in->connected) {
            status = AudioUnitRender(in->connection.sourceAudioUnit, &flags, ts, in->connection.sourceOutputNumber, frames, list);
        } else if (i == 0) {
            return kAudioUnitErr_NoConnection;
        } else {
            status = 1;  // side chains without a source are silent
        }
        if (status != noErr) {
            if (i == 0) return status;
            for (UInt32 ch = 0; ch < in->channels; ch++) memset(in->data[ch], 0, sizeof(float) * frames);
            continue;
        }
        for (UInt32 ch = 0; ch < in->channels && ch < list->mNumberBuffers; ch++) {
            if (list->mBuffers[ch].mData) in->data[ch] = (float*)list->mBuffers[ch].mData;
        }
    }
    return noErr;
}

static void bypass_block(AUInstance* a, UInt32 frames) {
    for (uint32_t o = 0; o < a->outCount; o++) {
        AUElement* out = &a->outs[o];
        for (UInt32 ch = 0; ch < out->channels; ch++) {
            if (o == 0 && a->inCount && a->ins[0].channels) {
                AUElement* in = &a->ins[0];
                memcpy(out->data[ch], in->data[ch < in->channels ? ch : in->channels - 1], sizeof(float) * frames);
            } else {
                memset(out->data[ch], 0, sizeof(float) * frames);
            }
        }
    }
}

static OSStatus run_block(AUInstance* a, const AudioTimeStamp* ts, UInt32 frames) {
    OSStatus status = pull_inputs(a, ts, frames);
    if (status != noErr) return status;
    if (a->bypass) {
        bypass_block(a, frames);
        return noErr;
    }
    if (!a->processing) {
        if (!a->plugin->start_processing(a->plugin)) return kAudioUnitErr_CannotDoInCurrentContext;
        a->processing = true;
    }
    a->eventCount = 0;
    collect_dirty(a);
    collect_queue(a, frames);
    a->midiOutCount = 0;
    clap_input_events_t in = { a, in_size, in_get };
    clap_output_events_t out = { a, out_try_push };
    clap_process_t process = {
        a->steadyTime, frames, collect_transport(a), a->inBuffers, a->outBuffers, a->inCount, a->outCount, &in, &out,
    };
    clap_process_status result = a->plugin->process(a->plugin, &process);
    a->eventCount = 0;
    a->steadyTime += frames;
    if (result == CLAP_PROCESS_ERROR) return kAudioUnitErr_CannotDoInCurrentContext;
    send_midi_out(a, ts);
    if (a->noticeCount || atomic_load(&a->noticeOverflow)) schedule_main(a);
    return noErr;
}

static void call_render_notify(AUInstance* a, AudioUnitRenderActionFlags flags, const AudioTimeStamp* ts, UInt32 bus, UInt32 frames, AudioBufferList* io) {
    struct { AURenderCallback proc; void* data; } calls[AU_MAX_RENDER_NOTIFY];
    uint32_t n;
    os_unfair_lock_lock(&a->listenerLock);
    n = a->renderNotifyCount;
    memcpy(calls, a->renderNotify, sizeof(calls[0]) * n);
    os_unfair_lock_unlock(&a->listenerLock);
    for (uint32_t i = 0; i < n; i++) {
        AudioUnitRenderActionFlags f = flags;
        calls[i].proc(calls[i].data, &f, ts, bus, frames, io);
    }
}

static OSStatus au_render(void* self, AudioUnitRenderActionFlags* ioFlags, const AudioTimeStamp* ts, UInt32 bus, UInt32 frames, AudioBufferList* io) {
    AUInstance* a = (AUInstance*)self;
    if (!a->initialized) return kAudioUnitErr_Uninitialized;
    if (!ts || !io) return kAudio_ParamError;
    if (frames > a->maxFrames) return kAudioUnitErr_TooManyFramesToProcess;
    if (a->outCount ? bus >= a->outCount : bus != 0) return kAudioUnitErr_InvalidElement;
    AudioUnitRenderActionFlags flags = ioFlags ? *ioFlags : 0;
    call_render_notify(a, flags | kAudioUnitRenderAction_PreRender, ts, bus, frames, io);

    OSStatus status = noErr;
    AUElement* out = a->outCount ? &a->outs[bus] : NULL;
    // The main thread holds the render lock while it reconfigures the
    // plugin or flushes parameters through Go, which a garbage collection
    // can stall: rather than wait for it, render silence. The block's
    // events stay queued for the next one.
    if (!os_unfair_lock_trylock(&a->renderLock)) {
        for (UInt32 b = 0; b < io->mNumberBuffers; b++) {
            AudioBuffer* buffer = &io->mBuffers[b];
            if (!buffer->mData) {
                if (!out || b >= out->channels) {
                    status = kAudio_ParamError;
                    break;
                }
                buffer->mData = out->data[b];
            }
            memset(buffer->mData, 0, sizeof(float) * frames);
            buffer->mDataByteSize = frames * (UInt32)sizeof(float);
        }
        if (status == noErr) flags |= kAudioUnitRenderAction_OutputIsSilence;
        else a->lastError = status;
        if (ioFlags) *ioFlags = flags;
        call_render_notify(a, flags | kAudioUnitRenderAction_PostRender | (status != noErr ? kAudioUnitRenderAction_PostRenderError : 0), ts, bus, frames, io);
        return status;
    }
    inAudioThread = true;
    bool fresh = bus == 0 || !a->rendered || !(ts->mFlags & kAudioTimeStampSampleTimeValid) || ts->mSampleTime != a->lastSampleTime;
    if (fresh && frames > 0) {
        status = run_block(a, ts, frames);
        a->rendered = status == noErr;
        a->lastSampleTime = ts->mSampleTime;
    }
    if (status == noErr) {
        for (UInt32 b = 0; b < io->mNumberBuffers; b++) {
            AudioBuffer* buffer = &io->mBuffers[b];
            float* src = out && b < out->channels ? out->data[b] : NULL;
            if (!buffer->mData) {
                if (!src) {
                    status = kAudio_ParamError;
                    break;
                }
                buffer->mData = src;
            } else if (src) {
                memcpy(buffer->mData, src, sizeof(float) * frames);
            } else {
                memset(buffer->mData, 0, sizeof(float) * frames);
            }
            buffer->mDataByteSize = frames * (UInt32)sizeof(float);
        }
    }
    inAudioThread = false;
    os_unfair_lock_unlock(&a->renderLock);

    if (status != noErr) a->lastError = status;
    call_render_notify(a, flags | kAudioUnitRenderAction_PostRender | (status != noErr ? kAudioUnitRenderAction_PostRenderError : 0), ts, bus, frames, io);
    return status;
}

// ---- main thread ----

static void restart_plugin(AUInstance* a) {
    if (!a->initialized) return;
    os_unfair_lock_lock(&a->renderLock);
    if (a->processing) {
        a->plugin->stop_processing(a->plugin);
        a->processing = false;
    }
    a->plugin->deactivate(a->plugin);
    if (!a->plugin->activate(a->plugin, au_sample_rate(a), 1, a->maxFrames)) {
        a->initialized = false;
    }
    os_unfair_lock_unlock(&a->renderLock);
}

static void run_main(AUInstance* a) {
    atomic_store(&a->mainScheduled, false);
    if (atomic_exchange(&a->callbackRequested, false)) a->plugin->on_main_thread(a->plugin);
    if (atomic_exchange(&a->flushRequested, false)) {
        os_unfair_lock_lock(&a->renderLock);
        flush_params(a);
        os_unfair_lock_unlock(&a->renderLock);
    }
    if (atomic_exchange(&a->restartRequested, false)) {
        restart_plugin(a);
        atomic_store(&a->latencyChanged, true);
    }
    if (atomic_exchange(&a->valuesChanged, false)) {
        refresh_values(a);
        notify_parameter(a, kAudioUnitEvent_ParameterValueChange, kAUParameterListener_AnyParameter);
    }
    if (atomic_exchange(&a->infoChanged, false)) {
        fire_property(a, kAudioUnitProperty_ParameterList, kAudioUnitScope_Global, 0);
        fire_property(a, kAudioUnitProperty_ParameterInfo, kAudioUnitScope_Global, 0);
    }
    if (atomic_exchange(&a->latencyChanged, false)) {
        fire_property(a, kAudioUnitProperty_Latency, kAudioUnitScope_Global, 0);
        fire_property(a, kAudioUnitProperty_TailTime, kAudioUnitScope_Global, 0);
    }
    uint64_t size = atomic_exchange(&a->pendingSize, 0);
    if (size) resize_view(a, (uint32_t)(size >> 32), (uint32_t)size);

    AUNotice notices[AU_MAX_NOTICES];
    os_unfair_lock_lock(&a->noticeLock);
    uint32_t n = a->noticeCount;
    memcpy(notices, a->notices, sizeof(AUNotice) * n);
    a->noticeCount = 0;
    os_unfair_lock_unlock(&a->noticeLock);
    for (uint32_t i = 0; i < n; i++) {
        static const AudioUnitEventType types[] = {
            kAudioUnitEvent_ParameterValueChange, kAudioUnitEvent_BeginParameterChangeGesture, kAudioUnitEvent_EndParameterChangeGesture
        };
        notify_parameter(a, types[notices[i].kind], notices[i].param);
    }
    if (atomic_exchange(&a->noticeOverflow, false)) {
        notify_parameter(a, kAudioUnitEvent_ParameterValueChange, kAUParameterListener_AnyParameter);
    }
}

// ---- editor ----

static void close_gui(AUInstance* a);

@interface AU_CONTAINER_CLASS : AUNativeView {
@public
    AUInstance* instance;
}
@end

// host_size gives the editor the size the host asks for, or keeps the
// editor's size when it cannot take it.
static CGSize host_size(AUInstance* a, CGSize size) {
    if (!a || !a->guiCreated || a->guiResizing) return size;
    uint32_t width = (uint32_t)size.width, height = (uint32_t)size.height;
    if (a->gui->can_resize(a->plugin) && a->gui->adjust_size(a->plugin, &width, &height)) {
        a->gui->set_size(a->plugin, width, height);
    } else {
        a->gui->get_size(a->plugin, &width, &height);
    }
    return CGSizeMake(width, height);
}

@implementation AU_CONTAINER_CLASS
#if TARGET_OS_OSX
- (BOOL)isFlipped {
    return YES;
}

- (void)setFrameSize:(NSSize)size {
    [super setFrameSize:host_size(instance, size)];
}
#else
- (void)setFrame:(CGRect)frame {
    frame.size = host_size(instance, frame.size);
    [super setFrame:frame];
}
#endif

- (void)dealloc {
    if (instance) close_gui(instance);
    [super dealloc];
}
@end

// VST3GoAUEditorResized is posted, with the editor view as the object,
// when the plugin resizes its editor (for the AUv3 view controller).
NSString* const VST3GoAUEditorResized = @"VST3GoAUEditorResized";

static void resize_view(AUInstance* a, uint32_t width, uint32_t height) {
    if (!a->view) return;
    a->guiResizing = true;
    CGRect frame = a->view.frame;
    frame.size = CGSizeMake(width, height);
    a->view.frame = frame;
    a->guiResizing = false;
    [[NSNotificationCenter defaultCenter] postNotificationName:VST3GoAUEditorResized object:a->view];
}

static void close_gui(AUInstance* a) {
    if (a->view) {
        a->view->instance = NULL;
        a->view = nil;
    }
    if (a->guiCreated) {
        a->guiCreated = false;
        a->gui->hide(a->plugin);
        a->gui->destroy(a->plugin);
    }
}

static AUNativeView* open_gui(AUInstance* a) {
    if (!a->gui) return nil;
    close_gui(a);
    if (!a->gui->is_api_supported(a->plugin, CLAP_WINDOW_API_COCOA, false) || !a->gui->create(a->plugin, CLAP_WINDOW_API_COCOA, false)) {
        return nil;
    }
    a->guiCreated = true;
    uint32_t width = 0, height = 0;
    if (!a->gui->get_size(a->plugin, &width, &height) || !width || !height) {
        close_gui(a);
        return nil;
    }
    AU_CONTAINER_CLASS* view = [[AU_CONTAINER_CLASS alloc] initWithFrame:CGRectMake(0, 0, width, height)];
    view->instance = a;
    a->view = view;
    clap_window_t window = { CLAP_WINDOW_API_COCOA, { .cocoa = (void*)view } };
    if (!a->gui->set_parent(a->plugin, &window)) {
        close_gui(a);
        [view release];
        return nil;
    }
    a->gui->show(a->plugin);
    return [view autorelease];
}

#if TARGET_OS_OSX
@interface AU_FACTORY_CLASS : NSObject <AUCocoaUIBase>
@end

@implementation AU_FACTORY_CLASS
- (unsigned)interfaceVersion {
    return 0;
}

- (NSString*)description {
    return @"vst3go editor";
}

- (NSView*)uiViewForAudioUnit:(AudioUnit)unit withSize:(NSSize)size {
    (void)size;
    AUInstance* a = NULL;
    UInt32 dataSize = sizeof(a);
    if (AudioUnitGetProperty(unit, kVST3GoAUProperty_Instance, kAudioUnitScope_Global, 0, &a, &dataSize) != noErr || !a) {
        return nil;
    }
    return open_gui(a);
}
@end

static CFURLRef copy_bundle_url(void) {
    Dl_info info;
    if (!dladdr((const void*)&clap_entry, &info) || !info.dli_fname) return NULL;
    // <bundle>/Contents/MacOS/<binary>
    NSString* path = [[[[NSString stringWithUTF8String:info.dli_fname] stringByDeletingLastPathComponent]
        stringByDeletingLastPathComponent] stringByDeletingLastPathComponent];
    return (CFURLRef)CFBridgingRetain([NSURL fileURLWithPath:path isDirectory:YES]);
}
#endif

// ---- properties ----

static AUElement* element(AUInstance* a, AudioUnitScope scope, AudioUnitElement elem, OSStatus* status) {
    if (scope == kAudioUnitScope_Input) {
        if (elem < a->inCount) return &a->ins[elem];
    } else if (scope == kAudioUnitScope_Output) {
        if (elem < a->outCount) return &a->outs[elem];
    } else {
        *status = kAudioUnitErr_InvalidScope;
        return NULL;
    }
    *status = kAudioUnitErr_InvalidElement;
    return NULL;
}

// rate_element is the element whose sample rate a scope's SampleRate
// property is: the global scope's is output 0's (input 0's for a unit
// without outputs), as in AUBase; NULL with status noErr for a unit
// without audio elements, whose rate is globalRate.
static AUElement* rate_element(AUInstance* a, AudioUnitScope scope, AudioUnitElement elem, OSStatus* status) {
    if (scope != kAudioUnitScope_Global) return element(a, scope, elem, status);
    if (elem != 0) {
        *status = kAudioUnitErr_InvalidElement;
        return NULL;
    }
    *status = noErr;
    return a->outCount ? &a->outs[0] : a->inCount ? &a->ins[0] : NULL;
}

static bool is_effect(AUInstance* a) { return a->inCount > 0 && a->outCount > 0; }

static AudioStreamBasicDescription stream_format(const AUElement* e) {
    AudioStreamBasicDescription f;
    memset(&f, 0, sizeof(f));
    f.mSampleRate = e->sampleRate;
    f.mFormatID = kAudioFormatLinearPCM;
    f.mFormatFlags = kAudioFormatFlagsNativeFloatPacked | kAudioFormatFlagIsNonInterleaved;
    f.mBytesPerPacket = sizeof(float);
    f.mFramesPerPacket = 1;
    f.mBytesPerFrame = sizeof(float);
    f.mChannelsPerFrame = e->channels;
    f.mBitsPerChannel = 32;
    return f;
}

// ---- channel layouts ----

// request_main fills configurable-audio-ports requests for main bus
// channel counts (no input request for instruments); counts without a port
// type let the plugin pick the conventional speakers.
static uint32_t request_main(AUInstance* a, UInt32 in, UInt32 out, clap_audio_port_configuration_request_t* requests) {
    uint32_t n = 0;
    if (a->inCount) requests[n++] = (clap_audio_port_configuration_request_t){ true, 0, in, NULL, NULL };
    requests[n++] = (clap_audio_port_configuration_request_t){ false, 0, out, NULL, NULL };
    return n;
}

// probe_channel_infos asks the plugin which main bus channel counts (1 to
// AU_MAX_LAYOUT_CHANNELS per side) it accepts.
static void probe_channel_infos(AUInstance* a) {
    UInt32 in = a->inCount ? a->ins[0].channels : 0, out = a->outs[0].channels;
    a->channelInfos[0] = (AUChannelInfo){ (SInt16)in, (SInt16)out };
    a->channelInfoCount = 1;
    if (!a->configPorts) return;
    for (UInt32 i = a->inCount ? 1 : 0; i <= (a->inCount ? AU_MAX_LAYOUT_CHANNELS : 0); i++) {
        for (UInt32 o = 1; o <= AU_MAX_LAYOUT_CHANNELS; o++) {
            if (i == in && o == out) continue;
            clap_audio_port_configuration_request_t requests[2];
            uint32_t n = request_main(a, i, o, requests);
            if (a->configPorts->can_apply_configuration(a->plugin, requests, n)) {
                a->channelInfos[a->channelInfoCount++] = (AUChannelInfo){ (SInt16)i, (SInt16)o };
            }
        }
    }
}

// channels_supported reports whether some accepted configuration has
// channels on the main bus of one side.
static bool channels_supported(AUInstance* a, bool input, UInt32 channels) {
    for (uint32_t i = 0; i < a->channelInfoCount; i++) {
        if ((UInt32)(input ? a->channelInfos[i].inChannels : a->channelInfos[i].outChannels) == channels) return true;
    }
    return false;
}

// layout_tag is the Core Audio layout whose channel order is the plugin's
// for a channel count (VST3 speaker order), 0 when there is none.
static AudioChannelLayoutTag layout_tag(UInt32 channels) {
    switch (channels) {
    case 1: return kAudioChannelLayoutTag_Mono;
    case 2: return kAudioChannelLayoutTag_Stereo;
    case 3: return kAudioChannelLayoutTag_MPEG_3_0_A;     // L R C
    case 4: return kAudioChannelLayoutTag_Quadraphonic;   // L R Ls Rs
    case 5: return kAudioChannelLayoutTag_MPEG_5_0_A;     // L R C Ls Rs
    case 6: return kAudioChannelLayoutTag_MPEG_5_1_A;     // L R C LFE Ls Rs
    case 8: return kAudioChannelLayoutTag_MPEG_7_1_A;     // L R C LFE Ls Rs Lc Rc
    }
    return 0;
}

// layout_tags writes the layouts of the main bus channel counts of one
// side (other buses: their current count) and returns how many there are.
static UInt32 layout_tags(AUInstance* a, bool input, AudioUnitElement elem, AudioChannelLayoutTag* tags) {
    UInt32 n = 0;
    for (UInt32 channels = 1; channels <= AU_MAX_LAYOUT_CHANNELS; channels++) {
        bool ok = elem == 0 ? channels_supported(a, input, channels) : (input ? a->ins : a->outs)[elem].channels == channels;
        AudioChannelLayoutTag tag = layout_tag(channels);
        if (ok && tag) {
            if (tags) tags[n] = tag;
            n++;
        }
    }
    return n;
}

// set_channels changes the channel count of a bus before initialization:
// any count of the main buses the plugin accepts on that side (the pair is
// applied at initialization), the current one of the others.
static OSStatus set_channels(AUInstance* a, AudioUnitScope scope, AudioUnitElement elem, AUElement* e, UInt32 channels) {
    if (channels == e->channels) return noErr;
    if (a->initialized) return kAudioUnitErr_Initialized;
    if (elem != 0 || !channels_supported(a, scope == kAudioUnitScope_Input, channels)) return kAudioUnitErr_FormatNotSupported;
    e->channels = channels;
    return noErr;
}

// apply_channels gives the plugin the main bus channel counts the host set.
static bool apply_channels(AUInstance* a) {
    clap_audio_port_info_t info;
    bool same = a->audioPorts->get(a->plugin, 0, false, &info) && info.channel_count == a->outs[0].channels;
    if (same && a->inCount) same = a->audioPorts->get(a->plugin, 0, true, &info) && info.channel_count == a->ins[0].channels;
    if (same) return true;
    if (!a->configPorts) return false;
    clap_audio_port_configuration_request_t requests[2];
    uint32_t n = request_main(a, a->inCount ? a->ins[0].channels : 0, a->outs[0].channels, requests);
    return a->configPorts->apply_configuration(a->plugin, requests, n);
}

static OSStatus property_info(AUInstance* a, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, UInt32* size, Boolean* writable) {
    OSStatus status = noErr;
    bool global = scope == kAudioUnitScope_Global;
    *writable = false;
    switch (id) {
    case kAudioUnitProperty_ClassInfo:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(CFPropertyListRef);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_MakeConnection:
        if (scope != kAudioUnitScope_Input) return kAudioUnitErr_InvalidScope;
        if (elem >= a->inCount) return kAudioUnitErr_InvalidElement;
        *size = sizeof(AudioUnitConnection);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_SetRenderCallback:
        if (scope != kAudioUnitScope_Input) return kAudioUnitErr_InvalidScope;
        if (elem >= a->inCount) return kAudioUnitErr_InvalidElement;
        *size = sizeof(AURenderCallbackStruct);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_SampleRate:
        if (!rate_element(a, scope, elem, &status) && status != noErr) return status;
        *size = sizeof(Float64);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_StreamFormat:
        if (!element(a, scope, elem, &status)) return status;
        *size = sizeof(AudioStreamBasicDescription);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_ShouldAllocateBuffer:
        if (!element(a, scope, elem, &status)) return status;
        *size = sizeof(UInt32);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_ElementName:
        if (!element(a, scope, elem, &status)) return status;
        *size = sizeof(CFStringRef);
        return noErr;
    case kAudioUnitProperty_ParameterList:
        *size = global ? a->paramCount * (UInt32)sizeof(AudioUnitParameterID) : 0;
        return noErr;
    case kAudioUnitProperty_ParameterInfo:
        if (!global || param_index(a, elem) < 0) return kAudioUnitErr_InvalidParameter;
        *size = sizeof(AudioUnitParameterInfo);
        return noErr;
    case kAudioUnitProperty_ParameterValueStrings: {
        int32_t i = global ? param_index(a, elem) : -1;
        if (i < 0) return kAudioUnitErr_InvalidParameter;
        if (!param_stepped(&a->paramInfo[i])) return kAudioUnitErr_InvalidProperty;
        *size = sizeof(CFArrayRef);
        return noErr;
    }
    case kAudioUnitProperty_ParameterStringFromValue:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AudioUnitParameterStringFromValue);
        return noErr;
    case kAudioUnitProperty_ParameterValueFromString:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AudioUnitParameterValueFromString);
        return noErr;
    case kAudioUnitProperty_ElementCount:
        if (scope != kAudioUnitScope_Global && scope != kAudioUnitScope_Input && scope != kAudioUnitScope_Output) return kAudioUnitErr_InvalidScope;
        *size = sizeof(UInt32);
        return noErr;
    case kAudioUnitProperty_Latency:
    case kAudioUnitProperty_TailTime:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(Float64);
        return noErr;
    case kAudioUnitProperty_ARAFactory:
        if (!a->ara) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(ARAAudioUnitFactory);
        return noErr;
    case kAudioUnitProperty_ARAPlugInExtensionBindingWithRoles:
        if (!a->ara) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(ARAAudioUnitPlugInExtensionBinding);
        return noErr;
    case kAudioUnitProperty_SupportedNumChannels:
        if (!global) return kAudioUnitErr_InvalidScope;
        if (!a->outCount) return kAudioUnitErr_InvalidProperty;
        *size = a->channelInfoCount * (UInt32)sizeof(AUChannelInfo);
        return noErr;
    case kAudioUnitProperty_SupportedChannelLayoutTags:
        if (!element(a, scope, elem, &status)) return status;
        *size = layout_tags(a, scope == kAudioUnitScope_Input, elem, NULL) * (UInt32)sizeof(AudioChannelLayoutTag);
        return *size ? noErr : kAudioUnitErr_InvalidProperty;
    case kAudioUnitProperty_AudioChannelLayout: {
        AUElement* e = element(a, scope, elem, &status);
        if (!e) return status;
        if (!layout_tag(e->channels)) return kAudioUnitErr_InvalidProperty;
        *size = offsetof(AudioChannelLayout, mChannelDescriptions);  // a tag, no descriptions
        *writable = true;
        return noErr;
    }
    case kAudioUnitProperty_MaximumFramesPerSlice:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(UInt32);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_BypassEffect:
        if (!is_effect(a)) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(UInt32);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_InPlaceProcessing:
    case kAudioUnitProperty_OfflineRender:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(UInt32);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_LastRenderError:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(OSStatus);
        return noErr;
    case kAudioUnitProperty_HostCallbacks:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(HostCallbackInfo);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_FactoryPresets:
        if (!a->factoryPresetCount) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(CFArrayRef);
        return noErr;
    case kAudioUnitProperty_PresentPreset:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AUPreset);
        *writable = true;
        return noErr;
#if TARGET_OS_OSX
    case kAudioUnitProperty_CocoaUI:
        if (!a->gui) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AudioUnitCocoaViewInfo);
        return noErr;
#endif
    case kAudioUnitProperty_MIDIOutputCallbackInfo:
        if (!a->noteOut) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(CFArrayRef);
        return noErr;
    case kAudioUnitProperty_MIDIOutputCallback:
        if (!a->noteOut) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AUMIDIOutputCallbackStruct);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_MIDIOutputEventListCallback:
        if (!a->noteOut) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AUMIDIEventListBlock);
        *writable = true;
        return noErr;
    case kAudioUnitProperty_AudioUnitMIDIProtocol:
        if (!a->noteIn && !a->noteOut) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(SInt32);
        return noErr;
    case kAudioUnitProperty_HostMIDIProtocol:
        if (!a->noteOut) return kAudioUnitErr_InvalidProperty;
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(SInt32);
        *writable = true;
        return noErr;
    case kVST3GoAUProperty_Instance:
        if (!global) return kAudioUnitErr_InvalidScope;
        *size = sizeof(AUInstance*);
        return noErr;
    }
    return kAudioUnitErr_InvalidProperty;
}

static CFStringRef copy_value_text(AUInstance* a, uint32_t index, double value) {
    char text[256] = { 0 };
    if (!a->params->value_to_text(a->plugin, a->paramInfo[index].id, value, text, sizeof(text))) {
        snprintf(text, sizeof(text), "%g", value);
    }
    return CFStringCreateWithCString(NULL, text, kCFStringEncodingUTF8);
}

static void set_number(CFMutableDictionaryRef dict, CFStringRef key, SInt32 value) {
    CFNumberRef number = CFNumberCreate(NULL, kCFNumberSInt32Type, &value);
    CFDictionarySetValue(dict, key, number);
    CFRelease(number);
}

static bool get_number(CFDictionaryRef dict, CFStringRef key, SInt32* value) {
    CFTypeRef number = CFDictionaryGetValue(dict, key);
    return number && CFGetTypeID(number) == CFNumberGetTypeID() && CFNumberGetValue((CFNumberRef)number, kCFNumberSInt32Type, value);
}

typedef struct {
    uint8_t* data;
    size_t size, capacity, read;
} StateBuffer;

static int64_t state_write(const clap_ostream_t* stream, const void* data, uint64_t size) {
    StateBuffer* b = (StateBuffer*)stream->ctx;
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

static int64_t state_read(const clap_istream_t* stream, void* data, uint64_t size) {
    StateBuffer* b = (StateBuffer*)stream->ctx;
    size_t n = b->size - b->read < size ? b->size - b->read : size;
    memcpy(data, b->data + b->read, n);
    b->read += n;
    return (int64_t)n;
}

static OSStatus get_class_info(AUInstance* a, CFPropertyListRef* out) {
    if (atomic_load(&a->anyDirty)) {
        os_unfair_lock_lock(&a->renderLock);
        flush_params(a);
        os_unfair_lock_unlock(&a->renderLock);
    }
    StateBuffer b = { 0 };
    clap_ostream_t stream = { &b, state_write };
    if (a->state && !a->state->save(a->plugin, &stream)) {
        free(b.data);
        return kAudioUnitErr_CannotDoInCurrentContext;
    }
    CFMutableDictionaryRef dict = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    set_number(dict, CFSTR(kAUPresetVersionKey), 0);
    set_number(dict, CFSTR(kAUPresetTypeKey), (SInt32)a->desc.componentType);
    set_number(dict, CFSTR(kAUPresetSubtypeKey), (SInt32)a->desc.componentSubType);
    set_number(dict, CFSTR(kAUPresetManufacturerKey), (SInt32)a->desc.componentManufacturer);
    CFDictionarySetValue(dict, CFSTR(kAUPresetNameKey), a->preset.presetName ? a->preset.presetName : CFSTR("Untitled"));
    CFDataRef data = CFDataCreate(NULL, b.data, (CFIndex)b.size);
    CFDictionarySetValue(dict, CFSTR(kAUPresetDataKey), data);
    CFRelease(data);
    free(b.data);
    *out = dict;
    return noErr;
}

static void set_preset(AUInstance* a, SInt32 number, CFStringRef name) {
    if (name) CFRetain(name);
    if (a->preset.presetName) CFRelease(a->preset.presetName);
    a->preset.presetNumber = number;
    a->preset.presetName = name;
}

static OSStatus set_class_info(AUInstance* a, CFPropertyListRef plist) {
    if (!plist || CFGetTypeID(plist) != CFDictionaryGetTypeID()) return kAudioUnitErr_InvalidPropertyValue;
    CFDictionaryRef dict = (CFDictionaryRef)plist;
    SInt32 version, type, subtype, manufacturer;
    if (!get_number(dict, CFSTR(kAUPresetVersionKey), &version) || version != 0 ||
        !get_number(dict, CFSTR(kAUPresetTypeKey), &type) || (OSType)type != a->desc.componentType ||
        !get_number(dict, CFSTR(kAUPresetSubtypeKey), &subtype) || (OSType)subtype != a->desc.componentSubType ||
        !get_number(dict, CFSTR(kAUPresetManufacturerKey), &manufacturer) || (OSType)manufacturer != a->desc.componentManufacturer) {
        return kAudioUnitErr_InvalidPropertyValue;
    }
    CFTypeRef data = CFDictionaryGetValue(dict, CFSTR(kAUPresetDataKey));
    if (data && CFGetTypeID(data) == CFDataGetTypeID() && a->state) {
        StateBuffer b = { (uint8_t*)CFDataGetBytePtr((CFDataRef)data), (size_t)CFDataGetLength((CFDataRef)data), 0, 0 };
        b.capacity = b.size;
        clap_istream_t stream = { &b, state_read };
        if (!a->state->load(a->plugin, &stream)) return kAudioUnitErr_InvalidPropertyValue;
        // Values the host set before the load are replaced by the state.
        atomic_store(&a->anyDirty, false);
        for (uint32_t i = 0; i < a->paramCount; i++) atomic_store(&a->dirty[i], false);
        refresh_values(a);
        notify_parameter(a, kAudioUnitEvent_ParameterValueChange, kAUParameterListener_AnyParameter);
    }
    CFTypeRef name = CFDictionaryGetValue(dict, CFSTR(kAUPresetNameKey));
    set_preset(a, -1, name && CFGetTypeID(name) == CFStringGetTypeID() ? (CFStringRef)name : NULL);
    fire_property(a, kAudioUnitProperty_PresentPreset, kAudioUnitScope_Global, 0);
    return noErr;
}

static OSStatus get_property(AUInstance* a, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, void* out, UInt32* ioSize) {
    UInt32 size = 0;
    Boolean writable;
    OSStatus status = property_info(a, id, scope, elem, &size, &writable);
    if (status != noErr) return status;
    if (!ioSize) return kAudio_ParamError;
    if (!out) {
        *ioSize = size;
        return noErr;
    }
    if (*ioSize < size) return kAudioUnitErr_InvalidPropertyValue;
    *ioSize = size;
    switch (id) {
    case kAudioUnitProperty_ClassInfo:
        return get_class_info(a, (CFPropertyListRef*)out);
    case kAudioUnitProperty_MakeConnection:
        *(AudioUnitConnection*)out = a->ins[elem].connection;
        return noErr;
    case kAudioUnitProperty_SetRenderCallback:
        *(AURenderCallbackStruct*)out = a->ins[elem].callback;
        return noErr;
    case kAudioUnitProperty_SampleRate: {
        AUElement* e = rate_element(a, scope, elem, &status);
        *(Float64*)out = e ? e->sampleRate : a->globalRate;
        return noErr;
    }
    case kAudioUnitProperty_StreamFormat:
        *(AudioStreamBasicDescription*)out = stream_format(element(a, scope, elem, &status));
        return noErr;
    case kAudioUnitProperty_ShouldAllocateBuffer:
        *(UInt32*)out = element(a, scope, elem, &status)->shouldAllocate;
        return noErr;
    case kAudioUnitProperty_ElementName: {
        CFStringRef name = element(a, scope, elem, &status)->name;
        *(CFStringRef*)out = name ? (CFStringRef)CFRetain(name) : CFSTR("");
        return noErr;
    }
    case kAudioUnitProperty_ParameterList: {
        AudioUnitParameterID* ids = (AudioUnitParameterID*)out;
        if (scope == kAudioUnitScope_Global) {
            for (uint32_t i = 0; i < a->paramCount; i++) ids[i] = a->paramInfo[i].id;
        }
        return noErr;
    }
    case kAudioUnitProperty_ParameterInfo: {
        const clap_param_info_t* p = &a->paramInfo[param_index(a, elem)];
        AudioUnitParameterInfo* info = (AudioUnitParameterInfo*)out;
        memset(info, 0, sizeof(*info));
        strlcpy(info->name, p->name, sizeof(info->name));
        info->cfNameString = CFStringCreateWithCString(NULL, p->name, kCFStringEncodingUTF8);
        info->minValue = (AudioUnitParameterValue)p->min_value;
        info->maxValue = (AudioUnitParameterValue)p->max_value;
        info->defaultValue = (AudioUnitParameterValue)p->default_value;
        info->flags = kAudioUnitParameterFlag_IsReadable | kAudioUnitParameterFlag_IsWritable | kAudioUnitParameterFlag_HasCFNameString |
                      kAudioUnitParameterFlag_CFNameRelease | kAudioUnitParameterFlag_ValuesHaveStrings;
        if (!(p->flags & CLAP_PARAM_IS_AUTOMATABLE)) info->flags |= kAudioUnitParameterFlag_NonRealTime;
        // Selecting a program sets the other parameters.
        if (param_index(a, elem) == a->programParam) info->flags |= kAudioUnitParameterFlag_IsGlobalMeta;
        if (param_stepped(p)) {
            info->unit = p->min_value == 0 && p->max_value == 1 ? kAudioUnitParameterUnit_Boolean : kAudioUnitParameterUnit_Indexed;
        } else {
            info->unit = kAudioUnitParameterUnit_Generic;
            info->flags |= kAudioUnitParameterFlag_IsHighResolution;
        }
        return noErr;
    }
    case kAudioUnitProperty_ParameterValueStrings: {
        uint32_t i = (uint32_t)param_index(a, elem);
        const clap_param_info_t* p = &a->paramInfo[i];
        CFIndex count = (CFIndex)(p->max_value - p->min_value) + 1;
        if (count < 1 || count > 1024) return kAudioUnitErr_InvalidProperty;
        CFMutableArrayRef strings = CFArrayCreateMutable(NULL, count, &kCFTypeArrayCallBacks);
        for (CFIndex v = 0; v < count; v++) {
            CFStringRef text = copy_value_text(a, i, p->min_value + (double)v);
            CFArrayAppendValue(strings, text);
            CFRelease(text);
        }
        *(CFArrayRef*)out = strings;
        return noErr;
    }
    case kAudioUnitProperty_ParameterStringFromValue: {
        AudioUnitParameterStringFromValue* v = (AudioUnitParameterStringFromValue*)out;
        int32_t i = param_index(a, v->inParamID);
        if (i < 0) return kAudioUnitErr_InvalidParameter;
        double value = v->inValue ? *v->inValue : atomic_load(&a->values[i]);
        v->outString = copy_value_text(a, (uint32_t)i, value);
        return noErr;
    }
    case kAudioUnitProperty_ParameterValueFromString: {
        AudioUnitParameterValueFromString* v = (AudioUnitParameterValueFromString*)out;
        int32_t i = param_index(a, v->inParamID);
        if (i < 0) return kAudioUnitErr_InvalidParameter;
        char text[256];
        double value;
        if (!v->inString || !CFStringGetCString(v->inString, text, sizeof(text), kCFStringEncodingUTF8) ||
            !a->params->text_to_value(a->plugin, a->paramInfo[i].id, text, &value)) {
            return kAudioUnitErr_InvalidPropertyValue;
        }
        v->outValue = (AudioUnitParameterValue)value;
        return noErr;
    }
    case kAudioUnitProperty_ElementCount:
        *(UInt32*)out = scope == kAudioUnitScope_Input ? a->inCount : scope == kAudioUnitScope_Output ? a->outCount : 1;
        return noErr;
    case kAudioUnitProperty_Latency:
        *(Float64*)out = a->latency ? a->latency->get(a->plugin) / au_sample_rate(a) : 0;
        return noErr;
    case kAudioUnitProperty_TailTime: {
        double samples = a->tail ? (double)a->tail->get(a->plugin) : 0;
        Float64 rate = au_sample_rate(a);
        *(Float64*)out = fmin(samples, rate * 3600) / rate;  // "infinite" tails: one hour
        return noErr;
    }
    case kAudioUnitProperty_ARAFactory: {
        // Read-only properties whose data carries input: the host fills
        // the magic number (and the binding's document controller).
        ARAAudioUnitFactory* f = (ARAAudioUnitFactory*)out;
        if (f->inOutMagicNumber != kARAAudioUnitMagic) return kAudioUnitErr_InvalidPropertyValue;
        f->outFactory = a->ara->get_factory(a->plugin);
        return f->outFactory ? noErr : kAudioUnitErr_InvalidProperty;
    }
    case kAudioUnitProperty_ARAPlugInExtensionBindingWithRoles: {
        ARAAudioUnitPlugInExtensionBinding* b = (ARAAudioUnitPlugInExtensionBinding*)out;
        if (b->inOutMagicNumber != kARAAudioUnitMagic) return kAudioUnitErr_InvalidPropertyValue;
        b->outPlugInExtension = a->ara->bind_to_document_controller(a->plugin, b->inDocumentControllerRef, b->knownRoles, b->assignedRoles);
        return b->outPlugInExtension ? noErr : kAudioUnitErr_InvalidPropertyValue;
    }
    case kAudioUnitProperty_SupportedNumChannels:
        memcpy(out, a->channelInfos, a->channelInfoCount * sizeof(AUChannelInfo));
        return noErr;
    case kAudioUnitProperty_SupportedChannelLayoutTags:
        layout_tags(a, scope == kAudioUnitScope_Input, elem, (AudioChannelLayoutTag*)out);
        return noErr;
    case kAudioUnitProperty_AudioChannelLayout: {
        AudioChannelLayout* layout = (AudioChannelLayout*)out;
        memset(layout, 0, offsetof(AudioChannelLayout, mChannelDescriptions));
        layout->mChannelLayoutTag = layout_tag(element(a, scope, elem, &status)->channels);
        return noErr;
    }
    case kAudioUnitProperty_MaximumFramesPerSlice:
        *(UInt32*)out = a->maxFrames;
        return noErr;
    case kAudioUnitProperty_BypassEffect:
        *(UInt32*)out = a->bypass;
        return noErr;
    case kAudioUnitProperty_InPlaceProcessing:
        *(UInt32*)out = a->inPlace;
        return noErr;
    case kAudioUnitProperty_OfflineRender:
        *(UInt32*)out = a->offline;
        return noErr;
    case kAudioUnitProperty_LastRenderError:
        *(OSStatus*)out = a->lastError;
        a->lastError = noErr;
        return noErr;
    case kAudioUnitProperty_HostCallbacks:
        *(HostCallbackInfo*)out = a->hostCallbacks;
        return noErr;
    case kAudioUnitProperty_FactoryPresets: {
        // The array holds pointers to presets the instance owns; the caller
        // releases the array.
        CFMutableArrayRef presets = CFArrayCreateMutable(NULL, a->factoryPresetCount, NULL);
        for (uint32_t i = 0; i < a->factoryPresetCount; i++) CFArrayAppendValue(presets, &a->factoryPresets[i]);
        *(CFArrayRef*)out = presets;
        return noErr;
    }
    case kAudioUnitProperty_PresentPreset: {
        AUPreset* preset = (AUPreset*)out;
        preset->presetNumber = a->preset.presetNumber;
        preset->presetName = a->preset.presetName ? (CFStringRef)CFRetain(a->preset.presetName) : CFSTR("Untitled");
        return noErr;
    }
#if TARGET_OS_OSX
    case kAudioUnitProperty_CocoaUI: {
        AudioUnitCocoaViewInfo* info = (AudioUnitCocoaViewInfo*)out;
        info->mCocoaAUViewBundleLocation = copy_bundle_url();
        info->mCocoaAUViewClass[0] = CFStringCreateWithCString(NULL, AU_STR(AU_FACTORY_CLASS), kCFStringEncodingUTF8);
        return info->mCocoaAUViewBundleLocation ? noErr : kAudioUnitErr_InvalidProperty;
    }
#endif
    case kAudioUnitProperty_MIDIOutputCallbackInfo: {
        CFStringRef name = CFSTR("MIDI Out");
        *(CFArrayRef*)out = CFArrayCreate(NULL, (const void**)&name, 1, &kCFTypeArrayCallBacks);
        return noErr;
    }
    case kAudioUnitProperty_MIDIOutputCallback:
        *(AUMIDIOutputCallbackStruct*)out = a->midiOutCallback;
        return noErr;
    case kAudioUnitProperty_MIDIOutputEventListCallback:
        *(AUMIDIEventListBlock*)out = a->midiOutList;
        return noErr;
    case kAudioUnitProperty_AudioUnitMIDIProtocol:
        *(SInt32*)out = kMIDIProtocol_2_0;
        return noErr;
    case kAudioUnitProperty_HostMIDIProtocol:
        *(SInt32*)out = a->hostProtocol;
        return noErr;
    case kVST3GoAUProperty_Instance:
        *(AUInstance**)out = a;
        return noErr;
    }
    return kAudioUnitErr_InvalidProperty;
}

static OSStatus set_parameter(AUInstance* a, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitParameterValue value);

static OSStatus set_property(AUInstance* a, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, const void* in, UInt32 size) {
    UInt32 expected = 0;
    Boolean writable = false;
    OSStatus status = property_info(a, id, scope, elem, &expected, &writable);
    if (status != noErr) return status;
    if (!writable) return kAudioUnitErr_PropertyNotWritable;
    if (!in && id != kAudioUnitProperty_MakeConnection && id != kAudioUnitProperty_SetRenderCallback) return kAudio_ParamError;
    if (in && size < expected && id != kAudioUnitProperty_HostCallbacks) return kAudioUnitErr_InvalidPropertyValue;
    switch (id) {
    case kAudioUnitProperty_ClassInfo:
        status = set_class_info(a, *(const CFPropertyListRef*)in);
        break;
    case kAudioUnitProperty_MakeConnection: {
        AUElement* e = &a->ins[elem];
        memset(&e->callback, 0, sizeof(e->callback));
        e->connected = false;
        if (in) {
            e->connection = *(const AudioUnitConnection*)in;
            e->connected = e->connection.sourceAudioUnit != NULL;
        }
        break;
    }
    case kAudioUnitProperty_SetRenderCallback: {
        AUElement* e = &a->ins[elem];
        e->connected = false;
        e->callback = in ? *(const AURenderCallbackStruct*)in : (AURenderCallbackStruct){ NULL, NULL };
        break;
    }
    case kAudioUnitProperty_SampleRate:
    case kAudioUnitProperty_StreamFormat: {
        AUElement* e = id == kAudioUnitProperty_SampleRate ? rate_element(a, scope, elem, &status) : element(a, scope, elem, &status);
        Float64 rate;
        if (id == kAudioUnitProperty_SampleRate) {
            rate = *(const Float64*)in;
            if (!e) {
                if (!(rate > 0)) return kAudioUnitErr_FormatNotSupported;
                if (rate == a->globalRate) {
                    a->globalRateSet = true;
                    break;
                }
                if (a->initialized) return kAudioUnitErr_Initialized;
                a->globalRate = rate;
                a->globalRateSet = true;
                break;
            }
        } else {
            const AudioStreamBasicDescription* f = (const AudioStreamBasicDescription*)in;
            if (f->mFormatID != kAudioFormatLinearPCM || !(f->mFormatFlags & kAudioFormatFlagIsFloat) ||
                !(f->mFormatFlags & kAudioFormatFlagIsNonInterleaved) || f->mBitsPerChannel != 32 || f->mFramesPerPacket != 1) {
                return kAudioUnitErr_FormatNotSupported;
            }
            rate = f->mSampleRate;
            if (!(rate > 0)) return kAudioUnitErr_FormatNotSupported;
            if ((status = set_channels(a, scope, elem, e, f->mChannelsPerFrame)) != noErr) return status;
        }
        if (!(rate > 0)) return kAudioUnitErr_FormatNotSupported;
        if (rate == e->sampleRate) break;
        if (a->initialized) return kAudioUnitErr_Initialized;
        e->sampleRate = rate;
        break;
    }
    case kAudioUnitProperty_ShouldAllocateBuffer:
        element(a, scope, elem, &status)->shouldAllocate = *(const UInt32*)in;
        break;
    case kAudioUnitProperty_AudioChannelLayout: {
        const AudioChannelLayout* layout = (const AudioChannelLayout*)in;
        // A layout names the speakers of the stream format's channels; the
        // channel count changes with the stream format only.
        UInt32 channels = AudioChannelLayoutTag_GetNumberOfChannels(layout->mChannelLayoutTag);
        if (channels != element(a, scope, elem, &status)->channels || layout_tag(channels) != layout->mChannelLayoutTag) {
            return kAudioUnitErr_InvalidPropertyValue;
        }
        break;
    }
    case kAudioUnitProperty_MaximumFramesPerSlice: {
        UInt32 frames = *(const UInt32*)in;
        if (!frames) return kAudioUnitErr_InvalidPropertyValue;
        if (frames == a->maxFrames) break;
        if (a->initialized) return kAudioUnitErr_Initialized;
        a->maxFrames = frames;
        break;
    }
    case kAudioUnitProperty_BypassEffect: {
        UInt32 bypass = *(const UInt32*)in ? 1 : 0;
        os_unfair_lock_lock(&a->renderLock);
        if (bypass && !a->bypass && a->processing) a->plugin->reset(a->plugin);
        a->bypass = bypass;
        os_unfair_lock_unlock(&a->renderLock);
        break;
    }
    case kAudioUnitProperty_InPlaceProcessing:
        a->inPlace = *(const UInt32*)in ? 1 : 0;
        break;
    case kAudioUnitProperty_OfflineRender:
        a->offline = *(const UInt32*)in ? 1 : 0;
        if (a->render) a->render->set(a->plugin, a->offline ? CLAP_RENDER_OFFLINE : CLAP_RENDER_REALTIME);
        break;
    case kAudioUnitProperty_HostCallbacks:
        memset(&a->hostCallbacks, 0, sizeof(a->hostCallbacks));
        memcpy(&a->hostCallbacks, in, size < sizeof(HostCallbackInfo) ? size : sizeof(HostCallbackInfo));
        break;
    case kAudioUnitProperty_PresentPreset: {
        const AUPreset* preset = (const AUPreset*)in;
        if (preset->presetNumber >= 0) {
            if (a->programParam < 0 || (uint32_t)preset->presetNumber >= a->factoryPresetCount) return kAudioUnitErr_InvalidPropertyValue;
            set_parameter(a, a->paramInfo[a->programParam].id, kAudioUnitScope_Global, (AudioUnitParameterValue)preset->presetNumber);
            set_preset(a, preset->presetNumber, a->factoryPresets[preset->presetNumber].presetName);
        } else {
            set_preset(a, preset->presetNumber, preset->presetName);
        }
        break;
    }
    case kAudioUnitProperty_MIDIOutputCallback:
        a->midiOutCallback = *(const AUMIDIOutputCallbackStruct*)in;
        break;
    case kAudioUnitProperty_MIDIOutputEventListCallback: {
        AUMIDIEventListBlock block = *(const AUMIDIEventListBlock*)in;
        os_unfair_lock_lock(&a->renderLock);
        AUMIDIEventListBlock old = a->midiOutList;
        a->midiOutList = block ? Block_copy(block) : NULL;
        os_unfair_lock_unlock(&a->renderLock);
        if (old) Block_release(old);
        break;
    }
    case kAudioUnitProperty_HostMIDIProtocol: {
        SInt32 protocol = *(const SInt32*)in;
        if (protocol != kMIDIProtocol_1_0 && protocol != kMIDIProtocol_2_0) return kAudioUnitErr_InvalidPropertyValue;
        if (a->initialized) return kAudioUnitErr_Initialized;
        a->hostProtocol = protocol;
        break;
    }
    default:
        return kAudioUnitErr_PropertyNotWritable;
    }
    if (status == noErr) fire_property(a, id, scope, elem);
    return status;
}

// ---- parameters ----

static OSStatus set_parameter(AUInstance* a, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitParameterValue value) {
    if (scope != kAudioUnitScope_Global) return kAudioUnitErr_InvalidScope;
    int32_t i = param_index(a, id);
    if (i < 0) return kAudioUnitErr_InvalidParameter;
    const clap_param_info_t* p = &a->paramInfo[i];
    double v = fmin(fmax((double)value, p->min_value), p->max_value);
    if (param_stepped(p)) v = round(v);
    atomic_store(&a->values[i], v);
    atomic_store(&a->pending[i], v);
    atomic_store(&a->dirty[i], true);
    atomic_store(&a->anyDirty, true);
    if (!a->initialized && !inAudioThread) {
        os_unfair_lock_lock(&a->renderLock);
        flush_params(a);
        os_unfair_lock_unlock(&a->renderLock);
    }
    return noErr;
}

static OSStatus au_get_parameter(void* self, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitElement elem, AudioUnitParameterValue* value) {
    AUInstance* a = (AUInstance*)self;
    (void)elem;
    if (!value) return kAudio_ParamError;
    if (scope != kAudioUnitScope_Global) return kAudioUnitErr_InvalidScope;
    int32_t i = param_index(a, id);
    if (i < 0) return kAudioUnitErr_InvalidParameter;
    *value = (AudioUnitParameterValue)atomic_load(&a->values[i]);
    return noErr;
}

static OSStatus au_set_parameter(void* self, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitElement elem, AudioUnitParameterValue value, UInt32 offset) {
    (void)elem;
    (void)offset;
    return set_parameter((AUInstance*)self, id, scope, value);
}

static OSStatus au_schedule_parameters(void* self, const AudioUnitParameterEvent* events, UInt32 count) {
    AUInstance* a = (AUInstance*)self;
    if (!events && count) return kAudio_ParamError;
    for (UInt32 n = 0; n < count; n++) {
        const AudioUnitParameterEvent* e = &events[n];
        if (e->scope != kAudioUnitScope_Global) return kAudioUnitErr_InvalidScope;
        int32_t i = param_index(a, e->parameter);
        if (i < 0) return kAudioUnitErr_InvalidParameter;
        const clap_param_info_t* p = &a->paramInfo[i];
        if (!a->initialized) {
            AudioUnitParameterValue v = e->eventType == kParameterEvent_Immediate ? e->eventValues.immediate.value : e->eventValues.ramp.endValue;
            set_parameter(a, e->parameter, e->scope, v);
            continue;
        }
        if (e->eventType == kParameterEvent_Immediate) {
            double v = fmin(fmax(e->eventValues.immediate.value, p->min_value), p->max_value);
            atomic_store(&a->values[i], v);
            queue_event(a, (AUQueued){ .offset = e->eventValues.immediate.bufferOffset, .kind = QUEUED_PARAM, .param = p->id, .value = v }, NULL);
        } else {
            // A ramp becomes its start and end values.
            double start = fmin(fmax(e->eventValues.ramp.startValue, p->min_value), p->max_value);
            double end = fmin(fmax(e->eventValues.ramp.endValue, p->min_value), p->max_value);
            UInt32 offset = (UInt32)(e->eventValues.ramp.startBufferOffset > 0 ? e->eventValues.ramp.startBufferOffset : 0);
            UInt32 duration = e->eventValues.ramp.durationInFrames;
            atomic_store(&a->values[i], end);
            queue_event(a, (AUQueued){ .offset = offset, .kind = QUEUED_PARAM, .param = p->id, .value = start }, NULL);
            queue_event(a, (AUQueued){ .offset = offset + (duration ? duration - 1 : 0), .kind = QUEUED_PARAM, .param = p->id, .value = end }, NULL);
        }
    }
    return noErr;
}

// ---- MIDI ----

static OSStatus au_midi_event(void* self, UInt32 status, UInt32 data1, UInt32 data2, UInt32 offset) {
    AUInstance* a = (AUInstance*)self;
    if (!a->noteIn) return kAudioUnitErr_InvalidProperty;
    queue_event(a, (AUQueued){ .offset = offset, .kind = QUEUED_MIDI, .midi = { (uint8_t)status, (uint8_t)(data1 & 0x7F), (uint8_t)(data2 & 0x7F) } }, NULL);
    return noErr;
}

static OSStatus au_sysex(void* self, const UInt8* data, UInt32 length) {
    AUInstance* a = (AUInstance*)self;
    if (!a->noteIn) return kAudioUnitErr_InvalidProperty;
    if (!data || !length) return kAudio_ParamError;
    queue_event(a, (AUQueued){ .offset = 0, .kind = QUEUED_SYSEX, .sysexSize = length }, data);
    return noErr;
}

// queue_sysex7 collects the bytes of a MIDI 1.0 data packet (UMP type 3)
// and queues the message when it is complete.
static void queue_sysex7(AUInstance* a, uint32_t offset, const uint32_t* words) {
    uint8_t kind = (words[0] >> 20) & 0xF, count = (words[0] >> 16) & 0xF;
    uint8_t bytes[6] = { (uint8_t)(words[0] >> 8), (uint8_t)words[0], (uint8_t)(words[1] >> 24),
                         (uint8_t)(words[1] >> 16), (uint8_t)(words[1] >> 8), (uint8_t)words[1] };
    if (count > 6) return;
    if (kind == 0 || kind == 1) {  // complete or start
        a->sysexIn[0] = 0xF0;
        a->sysexInSize = 1;
    } else if (!a->sysexInSize) {
        return;  // a continuation without a start
    }
    if (a->sysexInSize + count + 1 > AU_SYSEX_BYTES) {
        a->sysexInSize = 0;  // too long: dropped
        return;
    }
    memcpy(a->sysexIn + a->sysexInSize, bytes, count);
    a->sysexInSize += count;
    if (kind == 0 || kind == 3) {  // complete or end
        a->sysexIn[a->sysexInSize++] = 0xF7;
        queue_event(a, (AUQueued){ .offset = offset, .kind = QUEUED_SYSEX, .sysexSize = a->sysexInSize }, a->sysexIn);
        a->sysexInSize = 0;
    }
}

// au_midi_event_list takes Universal MIDI Packets: MIDI 1.0 and 2.0
// channel voice messages go to the plugin as CLAP MIDI 2 events, SysEx
// data packets as SysEx; utility, system and stream messages are skipped.
static OSStatus au_midi_event_list(void* self, UInt32 offset, const MIDIEventList* list) {
    AUInstance* a = (AUInstance*)self;
    if (!a->noteIn) return kAudioUnitErr_InvalidProperty;
    if (!list) return kAudio_ParamError;
    const MIDIEventPacket* packet = &list->packet[0];
    for (UInt32 p = 0; p < list->numPackets; p++) {
        for (UInt32 w = 0; w < packet->wordCount;) {
            const uint32_t* words = &packet->words[w];
            uint32_t type = words[0] >> 28;
            // Message sizes in words by type (UMP: 0-2 and 6-7 one, 3-4 and
            // 8-A two, B-C three, 5 and D-F four).
            static const uint8_t sizes[16] = { 1, 1, 1, 2, 2, 4, 1, 1, 2, 2, 2, 3, 3, 4, 4, 4 };
            UInt32 n = sizes[type];
            if (w + n > packet->wordCount) break;
            if (type == 2 || type == 4) {
                queue_event(a, (AUQueued){ .offset = offset, .kind = QUEUED_MIDI2, .ump = { words[0], n > 1 ? words[1] : 0 } }, NULL);
            } else if (type == 3) {
                queue_sysex7(a, offset, words);
            }
            w += n;
        }
        packet = MIDIEventPacketNext(packet);
    }
    return noErr;
}

// ---- lifecycle ----

static OSStatus au_initialize(void* self) {
    AUInstance* a = (AUInstance*)self;
    if (a->initialized) return noErr;
    if (a->outCount && !apply_channels(a)) return kAudioUnitErr_FormatNotSupported;
    if (!a->inCount && !a->outCount && !a->globalRateSet) {
        Float64 rate = output_device_rate();
        if (rate > 0) a->globalRate = rate;
    }
    if (!alloc_buffers(a)) {
        free_buffers(a);
        return kAudioUnitErr_FailedInitialization;
    }
    os_unfair_lock_lock(&a->renderLock);
    bool ok = a->plugin->activate(a->plugin, au_sample_rate(a), 1, a->maxFrames);
    if (ok) {
        a->initialized = true;
        a->processing = false;
        a->rendered = false;
        a->steadyTime = 0;
    }
    os_unfair_lock_unlock(&a->renderLock);
    if (!ok) {
        free_buffers(a);
        return kAudioUnitErr_FailedInitialization;
    }
    return noErr;
}

static OSStatus au_uninitialize(void* self) {
    AUInstance* a = (AUInstance*)self;
    if (!a->initialized) return noErr;
    os_unfair_lock_lock(&a->renderLock);
    if (a->processing) {
        a->plugin->stop_processing(a->plugin);
        a->processing = false;
    }
    a->plugin->deactivate(a->plugin);
    a->initialized = false;
    if (atomic_load(&a->anyDirty)) flush_params(a);
    os_unfair_lock_unlock(&a->renderLock);
    free_buffers(a);
    return noErr;
}

static OSStatus au_reset(void* self, AudioUnitScope scope, AudioUnitElement elem) {
    AUInstance* a = (AUInstance*)self;
    (void)elem;
    if (scope != kAudioUnitScope_Global) return noErr;
    os_unfair_lock_lock(&a->renderLock);
    if (a->processing) a->plugin->reset(a->plugin);
    a->rendered = false;
    os_unfair_lock_unlock(&a->renderLock);
    os_unfair_lock_lock(&a->queueLock);
    a->queueCount = 0;
    a->sysexQueueUsed = 0;
    os_unfair_lock_unlock(&a->queueLock);
    return noErr;
}

static OSStatus au_get_property_info(void* self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, UInt32* size, Boolean* writable) {
    UInt32 s = 0;
    Boolean w = false;
    OSStatus status = property_info((AUInstance*)self, id, scope, elem, &s, &w);
    if (status == noErr) {
        if (size) *size = s;
        if (writable) *writable = w;
    }
    return status;
}

static OSStatus au_get_property(void* self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, void* out, UInt32* ioSize) {
    return get_property((AUInstance*)self, id, scope, elem, out, ioSize);
}

static OSStatus au_set_property(void* self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, const void* in, UInt32 size) {
    return set_property((AUInstance*)self, id, scope, elem, in, size);
}

static OSStatus au_add_property_listener(void* self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, void* data) {
    AUInstance* a = (AUInstance*)self;
    if (!proc) return kAudio_ParamError;
    OSStatus status = noErr;
    os_unfair_lock_lock(&a->listenerLock);
    if (a->listenerCount < AU_MAX_LISTENERS) {
        a->listeners[a->listenerCount].id = id;
        a->listeners[a->listenerCount].proc = proc;
        a->listeners[a->listenerCount].data = data;
        a->listenerCount++;
    } else {
        status = kAudio_MemFullError;
    }
    os_unfair_lock_unlock(&a->listenerLock);
    return status;
}

static void remove_listeners(AUInstance* a, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, bool matchData, void* data) {
    os_unfair_lock_lock(&a->listenerLock);
    uint32_t kept = 0;
    for (uint32_t i = 0; i < a->listenerCount; i++) {
        bool match = a->listeners[i].id == id && a->listeners[i].proc == proc && (!matchData || a->listeners[i].data == data);
        if (!match) a->listeners[kept++] = a->listeners[i];
    }
    a->listenerCount = kept;
    os_unfair_lock_unlock(&a->listenerLock);
}

static OSStatus au_remove_property_listener(void* self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc) {
    remove_listeners((AUInstance*)self, id, proc, false, NULL);
    return noErr;
}

static OSStatus au_remove_property_listener_with_data(void* self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, void* data) {
    remove_listeners((AUInstance*)self, id, proc, true, data);
    return noErr;
}

static OSStatus au_add_render_notify(void* self, AURenderCallback proc, void* data) {
    AUInstance* a = (AUInstance*)self;
    if (!proc) return kAudio_ParamError;
    OSStatus status = noErr;
    os_unfair_lock_lock(&a->listenerLock);
    if (a->renderNotifyCount < AU_MAX_RENDER_NOTIFY) {
        a->renderNotify[a->renderNotifyCount].proc = proc;
        a->renderNotify[a->renderNotifyCount].data = data;
        a->renderNotifyCount++;
    } else {
        status = kAudio_MemFullError;
    }
    os_unfair_lock_unlock(&a->listenerLock);
    return status;
}

static OSStatus au_remove_render_notify(void* self, AURenderCallback proc, void* data) {
    AUInstance* a = (AUInstance*)self;
    os_unfair_lock_lock(&a->listenerLock);
    uint32_t kept = 0;
    for (uint32_t i = 0; i < a->renderNotifyCount; i++) {
        if (a->renderNotify[i].proc != proc || a->renderNotify[i].data != data) a->renderNotify[kept++] = a->renderNotify[i];
    }
    a->renderNotifyCount = kept;
    os_unfair_lock_unlock(&a->listenerLock);
    return noErr;
}

static AudioComponentMethod au_lookup(SInt16 selector) {
    switch (selector) {
    case kAudioUnitInitializeSelect: return (AudioComponentMethod)au_initialize;
    case kAudioUnitUninitializeSelect: return (AudioComponentMethod)au_uninitialize;
    case kAudioUnitGetPropertyInfoSelect: return (AudioComponentMethod)au_get_property_info;
    case kAudioUnitGetPropertySelect: return (AudioComponentMethod)au_get_property;
    case kAudioUnitSetPropertySelect: return (AudioComponentMethod)au_set_property;
    case kAudioUnitAddPropertyListenerSelect: return (AudioComponentMethod)au_add_property_listener;
    case kAudioUnitRemovePropertyListenerSelect: return (AudioComponentMethod)au_remove_property_listener;
    case kAudioUnitRemovePropertyListenerWithUserDataSelect: return (AudioComponentMethod)au_remove_property_listener_with_data;
    case kAudioUnitAddRenderNotifySelect: return (AudioComponentMethod)au_add_render_notify;
    case kAudioUnitRemoveRenderNotifySelect: return (AudioComponentMethod)au_remove_render_notify;
    case kAudioUnitGetParameterSelect: return (AudioComponentMethod)au_get_parameter;
    case kAudioUnitSetParameterSelect: return (AudioComponentMethod)au_set_parameter;
    case kAudioUnitScheduleParametersSelect: return (AudioComponentMethod)au_schedule_parameters;
    case kAudioUnitRenderSelect: return (AudioComponentMethod)au_render;
    case kAudioUnitResetSelect: return (AudioComponentMethod)au_reset;
    case kMusicDeviceMIDIEventSelect: return (AudioComponentMethod)au_midi_event;
    case kMusicDeviceSysExSelect: return (AudioComponentMethod)au_sysex;
    case kMusicDeviceMIDIEventListSelect: return (AudioComponentMethod)au_midi_event_list;
    }
    return NULL;
}

static void destroy_instance(AUInstance* a) {
    if (a->plugin) {
        close_gui(a);
        au_uninitialize(a);
        a->plugin->destroy(a->plugin);
        a->plugin = NULL;
    }
    for (uint32_t i = 0; i < AU_MAX_PORTS; i++) {
        if (a->ins[i].name) CFRelease(a->ins[i].name);
        if (a->outs[i].name) CFRelease(a->outs[i].name);
    }
    for (uint32_t i = 0; i < a->factoryPresetCount; i++) CFRelease(a->factoryPresets[i].presetName);
    if (a->preset.presetName) CFRelease(a->preset.presetName);
    free(a->factoryPresets);
    free(a->paramInfo);
    free((void*)a->values);
    free((void*)a->pending);
    free((void*)a->dirty);
    free(a->paramEvents);
    free(a->events);
    if (a->midiOutList) Block_release(a->midiOutList);
    free(a);
}

static uint32_t read_ports(AUInstance* a, bool input, AUElement* elements) {
    uint32_t count = a->audioPorts ? a->audioPorts->count(a->plugin, input) : 0;
    if (count > AU_MAX_PORTS) count = AU_MAX_PORTS;
    for (uint32_t i = 0; i < count; i++) {
        clap_audio_port_info_t info;
        memset(&info, 0, sizeof(info));
        if (!a->audioPorts->get(a->plugin, i, input, &info)) return i;
        elements[i].channels = info.channel_count;
        elements[i].sampleRate = 44100;
        elements[i].shouldAllocate = 1;
        elements[i].name = CFStringCreateWithCString(NULL, info.name, kCFStringEncodingUTF8);
    }
    return count;
}

static OSStatus au_open(void* self, AudioComponentInstance ci) {
    AUInstance* a = (AUInstance*)self;
    a->ci = ci;
    static atomic_bool entryInit;
    if (!atomic_exchange(&entryInit, true)) {
#if TARGET_OS_OSX
        // In a sandbox, before any plugin reads the files of its saved
        // state.
        VST3GoRestoreFileAccess();
#endif
        clap_entry.init("");
    }
    const clap_plugin_factory_t* factory = (const clap_plugin_factory_t*)clap_entry.get_factory(CLAP_PLUGIN_FACTORY_ID);
    const clap_plugin_descriptor_t* desc = GoClapDescriptor(a->index);
    if (!factory || !desc) return kAudioUnitErr_FailedInitialization;
    a->host = (clap_host_t){ CLAP_VERSION_INIT, a, "vst3go Audio Unit", "vst3go", "https://github.com/meteuix/vst3go", "1.0",
                             host_get_extension, host_request_restart, host_request_process, host_request_callback };
    a->plugin = factory->create_plugin(factory, &a->host, desc->id);
    if (!a->plugin) return kAudioUnitErr_FailedInitialization;
    if (!a->plugin->init(a->plugin)) {
        a->plugin->destroy(a->plugin);
        a->plugin = NULL;
        return kAudioUnitErr_FailedInitialization;
    }
#define EXT(field, type, id) a->field = (const type*)a->plugin->get_extension(a->plugin, id)
    EXT(params, clap_plugin_params_t, CLAP_EXT_PARAMS);
    EXT(state, clap_plugin_state_t, CLAP_EXT_STATE);
    EXT(audioPorts, clap_plugin_audio_ports_t, CLAP_EXT_AUDIO_PORTS);
    EXT(notePorts, clap_plugin_note_ports_t, CLAP_EXT_NOTE_PORTS);
    EXT(latency, clap_plugin_latency_t, CLAP_EXT_LATENCY);
    EXT(tail, clap_plugin_tail_t, CLAP_EXT_TAIL);
    EXT(gui, clap_plugin_gui_t, CLAP_EXT_GUI);
    EXT(render, clap_plugin_render_t, CLAP_EXT_RENDER);
    EXT(configPorts, clap_plugin_configurable_audio_ports_t, CLAP_EXT_CONFIGURABLE_AUDIO_PORTS);
    EXT(ara, clap_ara_plugin_extension_t, CLAP_EXT_ARA_PLUGINEXTENSION);
#undef EXT
    if (a->gui && !a->gui->is_api_supported(a->plugin, CLAP_WINDOW_API_COCOA, false)) a->gui = NULL;
    a->inCount = read_ports(a, true, a->ins);
    a->outCount = read_ports(a, false, a->outs);
    if (a->outCount) probe_channel_infos(a);
    a->noteIn = a->notePorts && a->notePorts->count(a->plugin, true) > 0;
    a->noteOut = a->notePorts && a->notePorts->count(a->plugin, false) > 0;
    a->maxFrames = AU_DEFAULT_MAX_FRAMES;
    a->preset.presetNumber = -1;
    a->programParam = -1;
    a->hostProtocol = kMIDIProtocol_1_0;

    a->paramCount = a->params ? a->params->count(a->plugin) : 0;
    a->paramInfo = (clap_param_info_t*)calloc(a->paramCount + 1, sizeof(clap_param_info_t));
    a->values = (_Atomic(double)*)calloc(a->paramCount + 1, sizeof(_Atomic(double)));
    a->pending = (_Atomic(double)*)calloc(a->paramCount + 1, sizeof(_Atomic(double)));
    a->dirty = (atomic_bool*)calloc(a->paramCount + 1, sizeof(atomic_bool));
    a->paramEvents = (clap_event_param_value_t*)calloc(a->paramCount + AU_MAX_QUEUE, sizeof(clap_event_param_value_t));
    a->events = (const clap_event_header_t**)calloc(a->paramCount + AU_MAX_QUEUE, sizeof(clap_event_header_t*));
    if (!a->paramInfo || !a->values || !a->pending || !a->dirty || !a->paramEvents || !a->events) return kAudio_MemFullError;
    uint32_t count = 0;
    for (uint32_t i = 0; i < a->paramCount; i++) {
        if (a->params->get_info(a->plugin, i, &a->paramInfo[count])) count++;
    }
    a->paramCount = count;
    refresh_values(a);
    for (uint32_t i = 0; i < a->paramCount; i++) {
        if (a->paramInfo[i].id == AU_PROGRAM_PARAM_ID && param_stepped(&a->paramInfo[i])) a->programParam = (int32_t)i;
    }
    if (a->programParam >= 0) {
        // Factory programs are the factory presets.
        const clap_param_info_t* p = &a->paramInfo[a->programParam];
        uint32_t presets = (uint32_t)(p->max_value - p->min_value) + 1;
        a->factoryPresets = (AUPreset*)calloc(presets, sizeof(AUPreset));
        if (!a->factoryPresets) return kAudio_MemFullError;
        for (uint32_t i = 0; i < presets; i++) {
            a->factoryPresets[i].presetNumber = (SInt32)i;
            a->factoryPresets[i].presetName = copy_value_text(a, (uint32_t)a->programParam, p->min_value + i);
        }
        a->factoryPresetCount = presets;
    }

    pthread_mutex_lock(&liveLock);
    a->serial = ++nextSerial;
    a->nextLive = live;
    live = a;
    pthread_mutex_unlock(&liveLock);
    return noErr;
}

static OSStatus au_close(void* self) {
    AUInstance* a = (AUInstance*)self;
    pthread_mutex_lock(&liveLock);
    for (AUInstance** p = &live; *p; p = &(*p)->nextLive) {
        if (*p == a) {
            *p = a->nextLive;
            break;
        }
    }
    pthread_mutex_unlock(&liveLock);
    destroy_instance(a);
    return noErr;
}

static void* new_instance(const AudioComponentDescription* desc, uint32_t index) {
    AUInstance* a = (AUInstance*)calloc(1, sizeof(AUInstance));
    if (!a) return NULL;
    a->iface.Open = au_open;
    a->iface.Close = au_close;
    a->iface.Lookup = au_lookup;
    a->desc = *desc;
    a->index = index;
    a->globalRate = 44100;
    a->renderLock = a->queueLock = a->noticeLock = a->listenerLock = OS_UNFAIR_LOCK_INIT;
    return a;
}

// plugin_index finds the registered plugin with the component codes of
// desc.
static bool plugin_index(const AudioComponentDescription* desc, uint32_t* index) {
    uint32_t count = GoClapPluginCount();
    for (uint32_t i = 0; i < count; i++) {
        uint32_t type, subtype, manufacturer;
        if (!GoAUComponentCodes(i, &type, &subtype, &manufacturer)) return false;
        if (type == desc->componentType && subtype == desc->componentSubType && manufacturer == desc->componentManufacturer) {
            *index = i;
            return true;
        }
    }
    return false;
}

// VST3GoAUFactory is the component factory function of every Audio Unit
// in the bundle: it picks the registered plugin by the component codes.
__attribute__((visibility("default"))) void* VST3GoAUFactory(const AudioComponentDescription* desc) {
    uint32_t index;
    if (!desc || !plugin_index(desc, &index)) return NULL;
    return new_instance(desc, index);
}

// ---- in-process registration for AUv3 extensions ----

// The extension's own component (its Info.plist codes) is the AUv3 one, so
// the AUv2 implementation is registered in the process under private
// codes: the plugin's type, its index as the subtype and this
// manufacturer. Instances still report the public codes.
enum { AU_PRIVATE_MANUFACTURER = 'VGo3', AU_MAX_PRIVATE = 64 };

static AudioComponentPlugInInterface* private_factory(const AudioComponentDescription* desc) {
    uint32_t type, subtype, manufacturer;
    if (!desc || !GoAUComponentCodes(desc->componentSubType, &type, &subtype, &manufacturer)) return NULL;
    AudioComponentDescription pub = { type, subtype, manufacturer, 0, 0 };
    return (AudioComponentPlugInInterface*)new_instance(&pub, desc->componentSubType);
}

bool vst3go_au_register(const AudioComponentDescription* desc, AudioComponentDescription* private) {
    static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
    static bool registered[AU_MAX_PRIVATE];
    uint32_t index;
    if (!plugin_index(desc, &index) || index >= AU_MAX_PRIVATE) return false;
    *private = (AudioComponentDescription){ desc->componentType, index, AU_PRIVATE_MANUFACTURER, kAudioComponentFlag_SandboxSafe, 0 };
    pthread_mutex_lock(&lock);
    bool ok = registered[index];
    if (!ok) {
        const clap_plugin_descriptor_t* d = GoClapDescriptor(index);
        CFStringRef name = CFStringCreateWithCString(NULL, d && d->name ? d->name : "vst3go", kCFStringEncodingUTF8);
        ok = registered[index] = AudioComponentRegister(private, name, 0, private_factory) != NULL;
        if (name) CFRelease(name);
    }
    pthread_mutex_unlock(&lock);
    return ok;
}

static AUInstance* unit_instance(AudioUnit unit) {
    AUInstance* a = NULL;
    UInt32 size = sizeof(a);
    if (AudioUnitGetProperty(unit, kVST3GoAUProperty_Instance, kAudioUnitScope_Global, 0, &a, &size) != noErr) return NULL;
    return a;
}

// vst3go_au_open_view opens the editor of a component instance, NULL
// without one; the view is autoreleased.
AUNativeView* vst3go_au_open_view(AudioUnit unit) {
    AUInstance* a = unit_instance(unit);
    return a ? open_gui(a) : nil;
}

// vst3go_au_view_fits reports whether the open editor shows in an area of
// width x height (0 x 0: any size), at that size or, when its limits or
// fixed size keep it smaller, inside it: 1 yes, 0 no, -1 no editor is
// open. Main thread.
int vst3go_au_view_fits(AudioUnit unit, double width, double height) {
    AUInstance* a = unit_instance(unit);
    if (!a || !a->guiCreated) return -1;
    if (width <= 0 && height <= 0) return 1;
    if (width < 1 || height < 1) return 0;
    uint32_t w = (uint32_t)width, h = (uint32_t)height;
    if (!a->gui->can_resize(a->plugin) || !a->gui->adjust_size(a->plugin, &w, &h)) {
        if (!a->gui->get_size(a->plugin, &w, &h)) return 0;
    }
    return w <= width && h <= height;
}

// vst3go_au_select_view resizes the open editor to the host's view
// configuration, as far as its limits let it, and posts
// VST3GoAUEditorResized. Main thread.
void vst3go_au_select_view(AudioUnit unit, double width, double height) {
    AUInstance* a = unit_instance(unit);
    if (!a || !a->view || width < 1 || height < 1) return;
    CGSize size = host_size(a, CGSizeMake(width, height));
    resize_view(a, (uint32_t)size.width, (uint32_t)size.height);
}
