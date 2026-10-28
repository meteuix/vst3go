#include "vst3_component.h"
#include <string.h>
#include <stdlib.h>

// One COM object exposes the processor and controller interfaces of a
// single-component plugin. Every secondary interface is a small struct whose
// first member is its vtable and whose second member points back to the owner.

typedef struct Component Component;

typedef struct {
    struct Steinberg_Vst_IAudioProcessorVtbl* lpVtbl;
    Component* component;
} AudioProcessorInterface;

typedef struct {
    struct Steinberg_Vst_IEditControllerVtbl* lpVtbl;
    Component* component;
} EditControllerInterface;

typedef struct {
    struct Steinberg_Vst_IMidiMappingVtbl* lpVtbl;
    Component* component;
} MidiMappingInterface;

typedef struct {
    struct Steinberg_Vst_IProcessContextRequirementsVtbl* lpVtbl;
    Component* component;
} ContextRequirementsInterface;

struct Component {
    struct Steinberg_Vst_IComponentVtbl* lpVtbl; // must be first
    AudioProcessorInterface audioProcessor;
    EditControllerInterface editController;
    MidiMappingInterface midiMapping;
    ContextRequirementsInterface contextRequirements;
    int32_t refCount;
    uintptr_t goComponent;
};

#define OWNER(type, ptr) (((type*)(ptr))->component)

// FUnknown (shared by all interfaces)

Steinberg_tresult componentQueryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);

static Steinberg_uint32 componentAddRef(Component* component) {
    return (Steinberg_uint32)__atomic_add_fetch(&component->refCount, 1, __ATOMIC_ACQ_REL);
}

Steinberg_uint32 componentRelease(void* thisInterface) {
    Component* component = (Component*)thisInterface;
    int32_t count = __atomic_sub_fetch(&component->refCount, 1, __ATOMIC_ACQ_REL);
    if (count == 0) {
        GoReleaseComponent(component->goComponent);
        free(component);
    }
    return (Steinberg_uint32)count;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(thisInterface, iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE component_addRef(void* thisInterface) {
    return componentAddRef((Component*)thisInterface);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE component_release(void* thisInterface) {
    return componentRelease(thisInterface);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(OWNER(AudioProcessorInterface, thisInterface), iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_addRef(void* thisInterface) {
    return componentAddRef(OWNER(AudioProcessorInterface, thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_release(void* thisInterface) {
    return componentRelease(OWNER(AudioProcessorInterface, thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(OWNER(EditControllerInterface, thisInterface), iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE controller_addRef(void* thisInterface) {
    return componentAddRef(OWNER(EditControllerInterface, thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE controller_release(void* thisInterface) {
    return componentRelease(OWNER(EditControllerInterface, thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE midi_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(OWNER(MidiMappingInterface, thisInterface), iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE midi_addRef(void* thisInterface) {
    return componentAddRef(OWNER(MidiMappingInterface, thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE midi_release(void* thisInterface) {
    return componentRelease(OWNER(MidiMappingInterface, thisInterface));
}

// IComponent

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_initialize(void* thisInterface, struct Steinberg_FUnknown* context) {
    return GoComponentInitialize(((Component*)thisInterface)->goComponent, context);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_terminate(void* thisInterface) {
    return GoComponentTerminate(((Component*)thisInterface)->goComponent);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getControllerClassId(void* thisInterface, Steinberg_TUID classId) {
    (void)thisInterface;
    // Single-component plugin: the host queries IEditController on this object.
    if (classId) {
        memset(classId, 0, sizeof(Steinberg_TUID));
    }
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setIoMode(void* thisInterface, Steinberg_Vst_IoMode mode) {
    return GoComponentSetIoMode(((Component*)thisInterface)->goComponent, mode);
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE component_getBusCount(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir) {
    return GoComponentGetBusCount(((Component*)thisInterface)->goComponent, type, dir);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getBusInfo(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, struct Steinberg_Vst_BusInfo* bus) {
    if (!bus) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentGetBusInfo(((Component*)thisInterface)->goComponent, type, dir, index, bus);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getRoutingInfo(void* thisInterface, struct Steinberg_Vst_RoutingInfo* inInfo, struct Steinberg_Vst_RoutingInfo* outInfo) {
    (void)thisInterface;
    (void)inInfo;
    (void)outInfo;
    return Steinberg_kNotImplemented;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_activateBus(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, Steinberg_TBool state) {
    return GoComponentActivateBus(((Component*)thisInterface)->goComponent, type, dir, index, state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setActive(void* thisInterface, Steinberg_TBool state) {
    return GoComponentSetActive(((Component*)thisInterface)->goComponent, state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentSetState(((Component*)thisInterface)->goComponent, state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentGetState(((Component*)thisInterface)->goComponent, state);
}

// IAudioProcessor

#define AUDIO_GO(thisInterface) (OWNER(AudioProcessorInterface, thisInterface)->goComponent)

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setBusArrangements(void* thisInterface, Steinberg_Vst_SpeakerArrangement* inputs, Steinberg_int32 numIns, Steinberg_Vst_SpeakerArrangement* outputs, Steinberg_int32 numOuts) {
    if ((numIns > 0 && !inputs) || (numOuts > 0 && !outputs) || numIns < 0 || numOuts < 0) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioSetBusArrangements(AUDIO_GO(thisInterface), inputs, numIns, outputs, numOuts);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_getBusArrangement(void* thisInterface, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, Steinberg_Vst_SpeakerArrangement* arr) {
    if (!arr) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioGetBusArrangement(AUDIO_GO(thisInterface), dir, index, arr);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_canProcessSampleSize(void* thisInterface, Steinberg_int32 symbolicSampleSize) {
    return GoAudioCanProcessSampleSize(AUDIO_GO(thisInterface), symbolicSampleSize);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_getLatencySamples(void* thisInterface) {
    return GoAudioGetLatencySamples(AUDIO_GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setupProcessing(void* thisInterface, struct Steinberg_Vst_ProcessSetup* setup) {
    if (!setup) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioSetupProcessing(AUDIO_GO(thisInterface), setup);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setProcessing(void* thisInterface, Steinberg_TBool state) {
    return GoAudioSetProcessing(AUDIO_GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_process(void* thisInterface, struct Steinberg_Vst_ProcessData* data) {
    if (!data) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioProcess(AUDIO_GO(thisInterface), data);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_getTailSamples(void* thisInterface) {
    return GoAudioGetTailSamples(AUDIO_GO(thisInterface));
}

// IEditController

#define CONTROLLER_GO(thisInterface) (OWNER(EditControllerInterface, thisInterface)->goComponent)

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_initialize(void* thisInterface, struct Steinberg_FUnknown* context) {
    // The component and controller share one object and one initialize call.
    (void)thisInterface;
    (void)context;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_terminate(void* thisInterface) {
    (void)thisInterface;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setComponentState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerSetComponentState(CONTROLLER_GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerSetState(CONTROLLER_GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetState(CONTROLLER_GO(thisInterface), state);
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE controller_getParameterCount(void* thisInterface) {
    return GoEditControllerGetParameterCount(CONTROLLER_GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParameterInfo(void* thisInterface, Steinberg_int32 paramIndex, struct Steinberg_Vst_ParameterInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParameterInfo(CONTROLLER_GO(thisInterface), paramIndex, info);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParamStringByValue(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue valueNormalized, Steinberg_Vst_String128 string) {
    if (!string) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParamStringByValue(CONTROLLER_GO(thisInterface), id, valueNormalized, string);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParamValueByString(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_TChar* string, Steinberg_Vst_ParamValue* valueNormalized) {
    if (!string || !valueNormalized) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParamValueByString(CONTROLLER_GO(thisInterface), id, string, valueNormalized);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_normalizedParamToPlain(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue valueNormalized) {
    return GoEditControllerNormalizedParamToPlain(CONTROLLER_GO(thisInterface), id, valueNormalized);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_plainParamToNormalized(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue plainValue) {
    return GoEditControllerPlainParamToNormalized(CONTROLLER_GO(thisInterface), id, plainValue);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_getParamNormalized(void* thisInterface, Steinberg_Vst_ParamID id) {
    return GoEditControllerGetParamNormalized(CONTROLLER_GO(thisInterface), id);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setParamNormalized(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue value) {
    return GoEditControllerSetParamNormalized(CONTROLLER_GO(thisInterface), id, value);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setComponentHandler(void* thisInterface, struct Steinberg_Vst_IComponentHandler* handler) {
    return GoEditControllerSetComponentHandler(CONTROLLER_GO(thisInterface), handler);
}

static struct Steinberg_IPlugView* SMTG_STDMETHODCALLTYPE controller_createView(void* thisInterface, Steinberg_FIDString name) {
    return (struct Steinberg_IPlugView*)GoEditControllerCreateView(CONTROLLER_GO(thisInterface), (char*)name);
}

// IMidiMapping

static Steinberg_tresult SMTG_STDMETHODCALLTYPE midi_getMidiControllerAssignment(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_Vst_CtrlNumber midiControllerNumber, Steinberg_Vst_ParamID* id) {
    if (!id) {
        return Steinberg_kInvalidArgument;
    }
    return GoMidiMappingGetAssignment(OWNER(MidiMappingInterface, thisInterface)->goComponent, busIndex, channel, midiControllerNumber, id);
}

// IProcessContextRequirements: request every ProcessContext field that
// process.TransportInfo exposes.

static Steinberg_tresult SMTG_STDMETHODCALLTYPE requirements_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(OWNER(ContextRequirementsInterface, thisInterface), iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE requirements_addRef(void* thisInterface) {
    return componentAddRef(OWNER(ContextRequirementsInterface, thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE requirements_release(void* thisInterface) {
    return componentRelease(OWNER(ContextRequirementsInterface, thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE requirements_getProcessContextRequirements(void* thisInterface) {
    (void)thisInterface;
    return Steinberg_Vst_IProcessContextRequirements_Flags_kNeedContinousTimeSamples |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedProjectTimeMusic |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedBarPositionMusic |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedCycleMusic |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedSamplesToNextClock |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedTempo |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedTimeSignature |
           Steinberg_Vst_IProcessContextRequirements_Flags_kNeedTransportState;
}

static struct Steinberg_Vst_IProcessContextRequirementsVtbl contextRequirementsVtbl = {
    requirements_queryInterface,
    requirements_addRef,
    requirements_release,
    requirements_getProcessContextRequirements
};

static struct Steinberg_Vst_IComponentVtbl componentVtbl = {
    component_queryInterface,
    component_addRef,
    component_release,
    component_initialize,
    component_terminate,
    component_getControllerClassId,
    component_setIoMode,
    component_getBusCount,
    component_getBusInfo,
    component_getRoutingInfo,
    component_activateBus,
    component_setActive,
    component_setState,
    component_getState
};

static struct Steinberg_Vst_IAudioProcessorVtbl audioProcessorVtbl = {
    audio_queryInterface,
    audio_addRef,
    audio_release,
    audio_setBusArrangements,
    audio_getBusArrangement,
    audio_canProcessSampleSize,
    audio_getLatencySamples,
    audio_setupProcessing,
    audio_setProcessing,
    audio_process,
    audio_getTailSamples
};

static struct Steinberg_Vst_IEditControllerVtbl editControllerVtbl = {
    controller_queryInterface,
    controller_addRef,
    controller_release,
    controller_initialize,
    controller_terminate,
    controller_setComponentState,
    controller_setState,
    controller_getState,
    controller_getParameterCount,
    controller_getParameterInfo,
    controller_getParamStringByValue,
    controller_getParamValueByString,
    controller_normalizedParamToPlain,
    controller_plainParamToNormalized,
    controller_getParamNormalized,
    controller_setParamNormalized,
    controller_setComponentHandler,
    controller_createView
};

static struct Steinberg_Vst_IMidiMappingVtbl midiMappingVtbl = {
    midi_queryInterface,
    midi_addRef,
    midi_release,
    midi_getMidiControllerAssignment
};

void* createComponent(uintptr_t goComponent) {
    Component* component = (Component*)calloc(1, sizeof(Component));
    if (!component) {
        return NULL;
    }

    component->lpVtbl = &componentVtbl;
    component->audioProcessor.lpVtbl = &audioProcessorVtbl;
    component->audioProcessor.component = component;
    component->editController.lpVtbl = &editControllerVtbl;
    component->editController.component = component;
    component->midiMapping.lpVtbl = &midiMappingVtbl;
    component->midiMapping.component = component;
    component->contextRequirements.lpVtbl = &contextRequirementsVtbl;
    component->contextRequirements.component = component;
    component->refCount = 1;
    component->goComponent = goComponent;
    return component;
}

Steinberg_tresult componentQueryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    Component* component = (Component*)thisInterface;
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }

    void* result = NULL;
    if (memcmp(iid, Steinberg_FUnknown_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_IPluginBase_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_Vst_IComponent_iid, sizeof(Steinberg_TUID)) == 0) {
        result = component;
    } else if (memcmp(iid, Steinberg_Vst_IAudioProcessor_iid, sizeof(Steinberg_TUID)) == 0) {
        result = &component->audioProcessor;
    } else if (memcmp(iid, Steinberg_Vst_IEditController_iid, sizeof(Steinberg_TUID)) == 0) {
        result = &component->editController;
    } else if (memcmp(iid, Steinberg_Vst_IMidiMapping_iid, sizeof(Steinberg_TUID)) == 0) {
        result = &component->midiMapping;
    } else if (memcmp(iid, Steinberg_Vst_IProcessContextRequirements_iid, sizeof(Steinberg_TUID)) == 0) {
        result = &component->contextRequirements;
    }

    if (!result) {
        *obj = NULL;
        return Steinberg_kNoInterface;
    }

    componentAddRef(component);
    *obj = result;
    return Steinberg_kResultOk;
}
