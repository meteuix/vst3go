#include "vst3_bridge.h"
#include "vst3_component.h"
#include <string.h>
#include <stdlib.h>

// Plugin factory implementing IPluginFactory and IPluginFactory2. The factory
// is a process-wide singleton; reference counting only guards against
// releasing it more often than it was handed out.
typedef struct {
    struct Steinberg_IPluginFactory2Vtbl* lpVtbl;
    int32_t refCount;
} PluginFactory;

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE factory_addRef(void* thisInterface);
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE factory_release(void* thisInterface);
static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getFactoryInfo(void* thisInterface, struct Steinberg_PFactoryInfo* info);
static Steinberg_int32 SMTG_STDMETHODCALLTYPE factory_countClasses(void* thisInterface);
static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getClassInfo(void* thisInterface, Steinberg_int32 index, struct Steinberg_PClassInfo* info);
static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_createInstance(void* thisInterface, Steinberg_FIDString cid, Steinberg_FIDString iid, void** obj);
static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getClassInfo2(void* thisInterface, Steinberg_int32 index, struct Steinberg_PClassInfo2* info);

static struct Steinberg_IPluginFactory2Vtbl factoryVtbl = {
    factory_queryInterface,
    factory_addRef,
    factory_release,
    factory_getFactoryInfo,
    factory_countClasses,
    factory_getClassInfo,
    factory_createInstance,
    factory_getClassInfo2
};

static PluginFactory globalFactory = { &factoryVtbl, 0 };

// VST3 entry point.
VST3GO_EXPORT
struct Steinberg_IPluginFactory* GetPluginFactory(void) {
    factory_addRef(&globalFactory);
    return (struct Steinberg_IPluginFactory*)&globalFactory;
}

// Platform module entry points. The Go runtime is initialised when the shared
// library is loaded, so these only report success.
#if defined(__APPLE__)
VST3GO_EXPORT int bundleEntry(void* bundleRef) {
    (void)bundleRef;
    return 1;
}

VST3GO_EXPORT int bundleExit(void) {
    return 1;
}
#elif defined(__linux__)
VST3GO_EXPORT int ModuleEntry(void* sharedLibraryHandle) {
    (void)sharedLibraryHandle;
    return 1;
}

VST3GO_EXPORT int ModuleExit(void) {
    return 1;
}
#elif defined(_WIN32)
VST3GO_EXPORT int InitDll(void) {
    return 1;
}

VST3GO_EXPORT int ExitDll(void) {
    return 1;
}
#endif

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    if (memcmp(iid, Steinberg_FUnknown_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_IPluginFactory_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_IPluginFactory2_iid, sizeof(Steinberg_TUID)) == 0) {
        *obj = thisInterface;
        factory_addRef(thisInterface);
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE factory_addRef(void* thisInterface) {
    PluginFactory* factory = (PluginFactory*)thisInterface;
    return (Steinberg_uint32)__atomic_add_fetch(&factory->refCount, 1, __ATOMIC_ACQ_REL);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE factory_release(void* thisInterface) {
    PluginFactory* factory = (PluginFactory*)thisInterface;
    int32_t count = __atomic_sub_fetch(&factory->refCount, 1, __ATOMIC_ACQ_REL);
    if (count < 0) {
        __atomic_store_n(&factory->refCount, 0, __ATOMIC_RELEASE);
        count = 0;
    }
    return (Steinberg_uint32)count;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getFactoryInfo(void* thisInterface, struct Steinberg_PFactoryInfo* info) {
    (void)thisInterface;
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    memset(info, 0, sizeof(*info));
    GoGetFactoryInfo(info);
    return Steinberg_kResultOk;
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE factory_countClasses(void* thisInterface) {
    (void)thisInterface;
    return GoCountClasses();
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getClassInfo2(void* thisInterface, Steinberg_int32 index, struct Steinberg_PClassInfo2* info) {
    (void)thisInterface;
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    memset(info, 0, sizeof(*info));
    return GoGetClassInfo2(index, info);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_getClassInfo(void* thisInterface, Steinberg_int32 index, struct Steinberg_PClassInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    struct Steinberg_PClassInfo2 info2;
    Steinberg_tresult result = factory_getClassInfo2(thisInterface, index, &info2);
    if (result != Steinberg_kResultOk) {
        return result;
    }
    memset(info, 0, sizeof(*info));
    memcpy(info->cid, info2.cid, sizeof(Steinberg_TUID));
    info->cardinality = info2.cardinality;
    memcpy(info->category, info2.category, sizeof(info->category));
    memcpy(info->name, info2.name, sizeof(info->name));
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE factory_createInstance(void* thisInterface, Steinberg_FIDString cid, Steinberg_FIDString iid, void** obj) {
    (void)thisInterface;
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    *obj = NULL;
    if (!cid || !iid) {
        return Steinberg_kInvalidArgument;
    }

    struct Steinberg_FUnknown* instance = (struct Steinberg_FUnknown*)GoCreateInstance((char*)cid, (char*)iid);
    if (!instance) {
        return Steinberg_kNoInterface;
    }

    // Hand out the interface the host asked for; drop the creation reference.
    Steinberg_tresult result = instance->lpVtbl->queryInterface(instance, iid, obj);
    instance->lpVtbl->release(instance);
    return result;
}

// ARA main factory class: one static IMainFactory object per ARA factory.

typedef struct {
    Steinberg_tresult (SMTG_STDMETHODCALLTYPE* queryInterface)(void*, const Steinberg_TUID, void**);
    Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* addRef)(void*);
    Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* release)(void*);
    const ARAFactory* (SMTG_STDMETHODCALLTYPE* getFactory)(void*);
} MainFactoryVtbl;

typedef struct {
    const MainFactoryVtbl* lpVtbl;
    int32_t slot;
} MainFactory;

static Steinberg_tresult SMTG_STDMETHODCALLTYPE mainFactory_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    if (iid && (memcmp(iid, Steinberg_FUnknown_iid, sizeof(Steinberg_TUID)) == 0 || memcmp(iid, ARA_IMainFactory_iid, sizeof(Steinberg_TUID)) == 0)) {
        *obj = thisInterface;
        return Steinberg_kResultOk;
    }
    *obj = NULL;
    return Steinberg_kNoInterface;
}

// The objects live as long as the module: counting is not needed.
static Steinberg_uint32 SMTG_STDMETHODCALLTYPE mainFactory_addRef(void* thisInterface) {
    (void)thisInterface;
    return 1;
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE mainFactory_release(void* thisInterface) {
    (void)thisInterface;
    return 1;
}

static const ARAFactory* SMTG_STDMETHODCALLTYPE mainFactory_getFactory(void* thisInterface) {
    return vst3go_ara_factory(((MainFactory*)thisInterface)->slot);
}

static const MainFactoryVtbl mainFactoryVtbl = {
    mainFactory_queryInterface, mainFactory_addRef, mainFactory_release, mainFactory_getFactory
};

static MainFactory mainFactories[VST3GO_ARA_MAX_FACTORIES];

void* vst3go_ara_main_factory(int32_t slot) {
    if (slot < 0 || slot >= VST3GO_ARA_MAX_FACTORIES || !vst3go_ara_factory(slot)) {
        return NULL;
    }
    MainFactory* f = &mainFactories[slot];
    f->lpVtbl = &mainFactoryVtbl;
    f->slot = slot;
    return f;
}

// Parameter changes

int32_t vst3go_paramchanges_count(void* changes) {
    struct Steinberg_Vst_IParameterChanges* c = (struct Steinberg_Vst_IParameterChanges*)changes;
    if (!c || !c->lpVtbl) {
        return 0;
    }
    return c->lpVtbl->getParameterCount(c);
}

void* vst3go_paramchanges_queue(void* changes, int32_t index) {
    struct Steinberg_Vst_IParameterChanges* c = (struct Steinberg_Vst_IParameterChanges*)changes;
    if (!c || !c->lpVtbl) {
        return NULL;
    }
    return c->lpVtbl->getParameterData(c, index);
}

uint32_t vst3go_paramqueue_id(void* queue) {
    struct Steinberg_Vst_IParamValueQueue* q = (struct Steinberg_Vst_IParamValueQueue*)queue;
    if (!q || !q->lpVtbl) {
        return 0xFFFFFFFFu;
    }
    return q->lpVtbl->getParameterId(q);
}

int32_t vst3go_paramqueue_count(void* queue) {
    struct Steinberg_Vst_IParamValueQueue* q = (struct Steinberg_Vst_IParamValueQueue*)queue;
    if (!q || !q->lpVtbl) {
        return 0;
    }
    return q->lpVtbl->getPointCount(q);
}

Steinberg_tresult vst3go_paramqueue_point(void* queue, int32_t index, int32_t* sampleOffset, double* value) {
    struct Steinberg_Vst_IParamValueQueue* q = (struct Steinberg_Vst_IParamValueQueue*)queue;
    if (!q || !q->lpVtbl || !sampleOffset || !value) {
        return Steinberg_kInvalidArgument;
    }
    return q->lpVtbl->getPoint(q, index, sampleOffset, value);
}

Steinberg_tresult vst3go_paramchanges_add_point(void* changes, uint32_t id, int32_t sampleOffset, double value) {
    struct Steinberg_Vst_IParameterChanges* c = (struct Steinberg_Vst_IParameterChanges*)changes;
    if (!c || !c->lpVtbl) {
        return Steinberg_kInvalidArgument;
    }
    Steinberg_int32 queueIndex = 0;
    Steinberg_Vst_ParamID paramId = id;
    struct Steinberg_Vst_IParamValueQueue* q = c->lpVtbl->addParameterData(c, &paramId, &queueIndex);
    if (!q || !q->lpVtbl) {
        return Steinberg_kResultFalse;
    }
    Steinberg_int32 pointIndex = 0;
    return q->lpVtbl->addPoint(q, sampleOffset, value, &pointIndex);
}

// Events

int32_t vst3go_events_count(void* list) {
    struct Steinberg_Vst_IEventList* l = (struct Steinberg_Vst_IEventList*)list;
    if (!l || !l->lpVtbl) {
        return 0;
    }
    return l->lpVtbl->getEventCount(l);
}

Steinberg_tresult vst3go_events_get(void* list, int32_t index, VST3GoEvent* out) {
    struct Steinberg_Vst_IEventList* l = (struct Steinberg_Vst_IEventList*)list;
    if (!l || !l->lpVtbl || !out) {
        return Steinberg_kInvalidArgument;
    }

    struct Steinberg_Vst_Event e;
    memset(&e, 0, sizeof(e));
    Steinberg_tresult result = l->lpVtbl->getEvent(l, index, &e);
    if (result != Steinberg_kResultOk) {
        return result;
    }

    memset(out, 0, sizeof(*out));
    out->type = e.type;
    out->busIndex = e.busIndex;
    out->sampleOffset = e.sampleOffset;
    out->flags = e.flags;
    out->noteId = -1;

    switch (e.type) {
    case Steinberg_Vst_Event_EventTypes_kNoteOnEvent:
        out->channel = e.Steinberg_Vst_Event_noteOn.channel;
        out->pitch = e.Steinberg_Vst_Event_noteOn.pitch;
        out->tuning = e.Steinberg_Vst_Event_noteOn.tuning;
        out->value = e.Steinberg_Vst_Event_noteOn.velocity;
        out->length = e.Steinberg_Vst_Event_noteOn.length;
        out->noteId = e.Steinberg_Vst_Event_noteOn.noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteOffEvent:
        out->channel = e.Steinberg_Vst_Event_noteOff.channel;
        out->pitch = e.Steinberg_Vst_Event_noteOff.pitch;
        out->tuning = e.Steinberg_Vst_Event_noteOff.tuning;
        out->value = e.Steinberg_Vst_Event_noteOff.velocity;
        out->noteId = e.Steinberg_Vst_Event_noteOff.noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kPolyPressureEvent:
        out->channel = e.Steinberg_Vst_Event_polyPressure.channel;
        out->pitch = e.Steinberg_Vst_Event_polyPressure.pitch;
        out->value = e.Steinberg_Vst_Event_polyPressure.pressure;
        out->noteId = e.Steinberg_Vst_Event_polyPressure.noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionValueEvent:
        out->expressionType = e.Steinberg_Vst_Event_noteExpressionValue.typeId;
        out->noteId = e.Steinberg_Vst_Event_noteExpressionValue.noteId;
        out->expressionValue = e.Steinberg_Vst_Event_noteExpressionValue.value;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionIntValueEvent:
        out->expressionType = e.Steinberg_Vst_Event_noteExpressionIntValue.typeId;
        out->noteId = e.Steinberg_Vst_Event_noteExpressionIntValue.noteId;
        out->expressionIntValue = e.Steinberg_Vst_Event_noteExpressionIntValue.value;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionTextEvent:
        out->expressionType = e.Steinberg_Vst_Event_noteExpressionText.typeId;
        out->noteId = e.Steinberg_Vst_Event_noteExpressionText.noteId;
        out->text = e.Steinberg_Vst_Event_noteExpressionText.text;
        out->textLen = e.Steinberg_Vst_Event_noteExpressionText.textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kChordEvent:
        out->root = e.Steinberg_Vst_Event_chord.root;
        out->bassNote = e.Steinberg_Vst_Event_chord.bassNote;
        out->mask = e.Steinberg_Vst_Event_chord.mask;
        out->text = e.Steinberg_Vst_Event_chord.text;
        out->textLen = e.Steinberg_Vst_Event_chord.textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kScaleEvent:
        out->root = e.Steinberg_Vst_Event_scale.root;
        out->mask = e.Steinberg_Vst_Event_scale.mask;
        out->text = e.Steinberg_Vst_Event_scale.text;
        out->textLen = e.Steinberg_Vst_Event_scale.textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kDataEvent:
        out->data = e.Steinberg_Vst_Event_data.bytes;
        out->dataSize = e.Steinberg_Vst_Event_data.size;
        out->dataType = e.Steinberg_Vst_Event_data.type;
        break;
    case Steinberg_Vst_Event_EventTypes_kLegacyMIDICCOutEvent:
        out->controlNumber = e.Steinberg_Vst_Event_midiCCOut.controlNumber;
        out->channel = e.Steinberg_Vst_Event_midiCCOut.channel;
        out->controlValue = e.Steinberg_Vst_Event_midiCCOut.value;
        out->controlValue2 = e.Steinberg_Vst_Event_midiCCOut.value2;
        break;
    default:
        break;
    }
    return Steinberg_kResultOk;
}

Steinberg_tresult vst3go_events_add(void* list, const VST3GoEvent* in) {
    struct Steinberg_Vst_IEventList* l = (struct Steinberg_Vst_IEventList*)list;
    if (!l || !l->lpVtbl || !in) {
        return Steinberg_kInvalidArgument;
    }

    struct Steinberg_Vst_Event e;
    memset(&e, 0, sizeof(e));
    e.busIndex = in->busIndex;
    e.sampleOffset = in->sampleOffset;
    e.flags = (Steinberg_uint16)in->flags;
    e.type = (Steinberg_uint16)in->type;

    switch (in->type) {
    case Steinberg_Vst_Event_EventTypes_kNoteOnEvent:
        e.Steinberg_Vst_Event_noteOn.channel = (Steinberg_int16)in->channel;
        e.Steinberg_Vst_Event_noteOn.pitch = (Steinberg_int16)in->pitch;
        e.Steinberg_Vst_Event_noteOn.tuning = in->tuning;
        e.Steinberg_Vst_Event_noteOn.velocity = in->value;
        e.Steinberg_Vst_Event_noteOn.length = in->length;
        e.Steinberg_Vst_Event_noteOn.noteId = in->noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteOffEvent:
        e.Steinberg_Vst_Event_noteOff.channel = (Steinberg_int16)in->channel;
        e.Steinberg_Vst_Event_noteOff.pitch = (Steinberg_int16)in->pitch;
        e.Steinberg_Vst_Event_noteOff.tuning = in->tuning;
        e.Steinberg_Vst_Event_noteOff.velocity = in->value;
        e.Steinberg_Vst_Event_noteOff.noteId = in->noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kPolyPressureEvent:
        e.Steinberg_Vst_Event_polyPressure.channel = (Steinberg_int16)in->channel;
        e.Steinberg_Vst_Event_polyPressure.pitch = (Steinberg_int16)in->pitch;
        e.Steinberg_Vst_Event_polyPressure.pressure = in->value;
        e.Steinberg_Vst_Event_polyPressure.noteId = in->noteId;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionValueEvent:
        e.Steinberg_Vst_Event_noteExpressionValue.typeId = in->expressionType;
        e.Steinberg_Vst_Event_noteExpressionValue.noteId = in->noteId;
        e.Steinberg_Vst_Event_noteExpressionValue.value = in->expressionValue;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionIntValueEvent:
        e.Steinberg_Vst_Event_noteExpressionIntValue.typeId = in->expressionType;
        e.Steinberg_Vst_Event_noteExpressionIntValue.noteId = in->noteId;
        e.Steinberg_Vst_Event_noteExpressionIntValue.value = in->expressionIntValue;
        break;
    case Steinberg_Vst_Event_EventTypes_kNoteExpressionTextEvent:
        e.Steinberg_Vst_Event_noteExpressionText.typeId = in->expressionType;
        e.Steinberg_Vst_Event_noteExpressionText.noteId = in->noteId;
        e.Steinberg_Vst_Event_noteExpressionText.text = in->text;
        e.Steinberg_Vst_Event_noteExpressionText.textLen = in->textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kChordEvent:
        e.Steinberg_Vst_Event_chord.root = in->root;
        e.Steinberg_Vst_Event_chord.bassNote = in->bassNote;
        e.Steinberg_Vst_Event_chord.mask = in->mask;
        e.Steinberg_Vst_Event_chord.text = in->text;
        e.Steinberg_Vst_Event_chord.textLen = (Steinberg_uint16)in->textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kScaleEvent:
        e.Steinberg_Vst_Event_scale.root = in->root;
        e.Steinberg_Vst_Event_scale.mask = in->mask;
        e.Steinberg_Vst_Event_scale.text = in->text;
        e.Steinberg_Vst_Event_scale.textLen = (Steinberg_uint16)in->textLen;
        break;
    case Steinberg_Vst_Event_EventTypes_kDataEvent:
        e.Steinberg_Vst_Event_data.bytes = in->data;
        e.Steinberg_Vst_Event_data.size = in->dataSize;
        e.Steinberg_Vst_Event_data.type = in->dataType;
        break;
    case Steinberg_Vst_Event_EventTypes_kLegacyMIDICCOutEvent:
        e.Steinberg_Vst_Event_midiCCOut.controlNumber = (Steinberg_uint8)in->controlNumber;
        e.Steinberg_Vst_Event_midiCCOut.channel = (Steinberg_int8)in->channel;
        e.Steinberg_Vst_Event_midiCCOut.value = (Steinberg_int8)in->controlValue;
        e.Steinberg_Vst_Event_midiCCOut.value2 = (Steinberg_int8)in->controlValue2;
        break;
    default:
        return Steinberg_kInvalidArgument;
    }
    return l->lpVtbl->addEvent(l, &e);
}

float** vst3go_bus_channels32(struct Steinberg_Vst_AudioBusBuffers* bus) {
    return bus ? bus->Steinberg_Vst_AudioBusBuffers_channelBuffers32 : NULL;
}

double** vst3go_bus_channels64(struct Steinberg_Vst_AudioBusBuffers* bus) {
    return bus ? bus->Steinberg_Vst_AudioBusBuffers_channelBuffers64 : NULL;
}

// IComponentHandler

Steinberg_tresult vst3go_handler_begin_edit(void* handler, Steinberg_Vst_ParamID id) {
    struct Steinberg_Vst_IComponentHandler* h = (struct Steinberg_Vst_IComponentHandler*)handler;
    return (h && h->lpVtbl) ? h->lpVtbl->beginEdit(h, id) : Steinberg_kResultFalse;
}

Steinberg_tresult vst3go_handler_perform_edit(void* handler, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue value) {
    struct Steinberg_Vst_IComponentHandler* h = (struct Steinberg_Vst_IComponentHandler*)handler;
    return (h && h->lpVtbl) ? h->lpVtbl->performEdit(h, id, value) : Steinberg_kResultFalse;
}

Steinberg_tresult vst3go_handler_end_edit(void* handler, Steinberg_Vst_ParamID id) {
    struct Steinberg_Vst_IComponentHandler* h = (struct Steinberg_Vst_IComponentHandler*)handler;
    return (h && h->lpVtbl) ? h->lpVtbl->endEdit(h, id) : Steinberg_kResultFalse;
}

Steinberg_tresult vst3go_handler_restart(void* handler, int32_t flags) {
    struct Steinberg_Vst_IComponentHandler* h = (struct Steinberg_Vst_IComponentHandler*)handler;
    return (h && h->lpVtbl) ? h->lpVtbl->restartComponent(h, flags) : Steinberg_kResultFalse;
}

void vst3go_unknown_add_ref(void* unknown) {
    struct Steinberg_FUnknown* u = (struct Steinberg_FUnknown*)unknown;
    if (u && u->lpVtbl) {
        u->lpVtbl->addRef(u);
    }
}

void vst3go_unknown_release(void* unknown) {
    struct Steinberg_FUnknown* u = (struct Steinberg_FUnknown*)unknown;
    if (u && u->lpVtbl) {
        u->lpVtbl->release(u);
    }
}

// IInfoListener attributes

static void read_attr_string(struct Steinberg_Vst_IAttributeList* list, const char* key, Steinberg_char16* dst) {
    dst[0] = 0;
    if (list->lpVtbl->getString(list, key, dst, sizeof(Steinberg_Vst_String128)) != Steinberg_kResultOk) {
        dst[0] = 0;
    }
    dst[127] = 0;
}

static int32_t read_attr_int(struct Steinberg_Vst_IAttributeList* list, const char* key, int64_t* dst) {
    Steinberg_int64 value = 0;
    if (list->lpVtbl->getInt(list, key, &value) != Steinberg_kResultOk) {
        return 0;
    }
    *dst = value;
    return 1;
}

void vst3go_read_channel_info(void* attributeList, VST3GoChannelInfo* out) {
    struct Steinberg_Vst_IAttributeList* list = (struct Steinberg_Vst_IAttributeList*)attributeList;
    memset(out, 0, sizeof(*out));
    if (!list || !list->lpVtbl) {
        return;
    }
    read_attr_string(list, Steinberg_Vst_ChannelContext_kChannelNameKey, out->name);
    read_attr_string(list, Steinberg_Vst_ChannelContext_kChannelUIDKey, out->uid);
    read_attr_string(list, Steinberg_Vst_ChannelContext_kChannelIndexNamespaceKey, out->indexNamespace);
    out->hasColor = read_attr_int(list, Steinberg_Vst_ChannelContext_kChannelColorKey, &out->color);
    out->hasIndex = read_attr_int(list, Steinberg_Vst_ChannelContext_kChannelIndexKey, &out->index);
    out->hasPluginLocation = read_attr_int(list, Steinberg_Vst_ChannelContext_kChannelPluginLocationKey, &out->pluginLocation);
}
