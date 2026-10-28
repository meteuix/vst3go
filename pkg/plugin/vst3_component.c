#include "vst3_component.h"
#include <string.h>
#include <stdlib.h>

// One COM object per plugin instance. Its first member is the primary vtable
// (IComponent, or IEditController for controller-kind objects); every other
// interface is a SubInterface whose first member is its vtable and whose
// second member points back to the owning object.

typedef struct Component Component;

typedef struct {
    const void* lpVtbl;
    Component* component;
} SubInterface;

struct Component {
    const struct Steinberg_Vst_IComponentVtbl* lpVtbl; // must be first
    SubInterface audioProcessor;
    SubInterface contextRequirements;
    SubInterface programListData;
    SubInterface editController;
    SubInterface midiMapping;
    SubInterface midiMapping2;
    SubInterface midiLearn;
    SubInterface midiLearn2;
    SubInterface unitInfo;
    SubInterface noteExpression;
    SubInterface physicalUI;
    SubInterface keyswitch;
    SubInterface infoListener;
    SubInterface connectionPoint;
    int32_t refCount;
    int32_t kind;
    int32_t caps;
    uintptr_t goComponent;
    Steinberg_TUID controllerCid;
    struct Steinberg_FUnknown* hostContext;     // from initialize, one reference
    struct Steinberg_Vst_IConnectionPoint* peer; // distributed plugins, one reference
};

#define OWNER(ptr) (((SubInterface*)(ptr))->component)
#define GO(ptr) (OWNER(ptr)->goComponent)
#define SELF_GO(ptr) (((Component*)(ptr))->goComponent)

static int iid_equal(const Steinberg_TUID a, const Steinberg_TUID b) {
    return memcmp(a, b, sizeof(Steinberg_TUID)) == 0;
}

// FUnknown

static Steinberg_uint32 componentAddRef(Component* component) {
    return (Steinberg_uint32)__atomic_add_fetch(&component->refCount, 1, __ATOMIC_ACQ_REL);
}

static void releaseUnknown(struct Steinberg_FUnknown** unknown) {
    if (*unknown) {
        (*unknown)->lpVtbl->release(*unknown);
        *unknown = NULL;
    }
}

Steinberg_uint32 componentRelease(void* thisInterface) {
    Component* component = (Component*)thisInterface;
    int32_t count = __atomic_sub_fetch(&component->refCount, 1, __ATOMIC_ACQ_REL);
    if (count == 0) {
        releaseUnknown((struct Steinberg_FUnknown**)&component->peer);
        releaseUnknown(&component->hostContext);
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

static Steinberg_tresult SMTG_STDMETHODCALLTYPE sub_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    return componentQueryInterface(OWNER(thisInterface), iid, obj);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE sub_addRef(void* thisInterface) {
    return componentAddRef(OWNER(thisInterface));
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE sub_release(void* thisInterface) {
    return componentRelease(OWNER(thisInterface));
}

static void storeHostContext(Component* component, struct Steinberg_FUnknown* context) {
    if (context) {
        context->lpVtbl->addRef(context);
    }
    releaseUnknown(&component->hostContext);
    component->hostContext = context;
}

// IComponent

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_initialize(void* thisInterface, struct Steinberg_FUnknown* context) {
    Component* component = (Component*)thisInterface;
    storeHostContext(component, context);
    return GoComponentInitialize(component->goComponent);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_terminate(void* thisInterface) {
    Component* component = (Component*)thisInterface;
    Steinberg_tresult result = GoComponentTerminate(component->goComponent);
    releaseUnknown(&component->hostContext);
    return result;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getControllerClassId(void* thisInterface, Steinberg_TUID classId) {
    Component* component = (Component*)thisInterface;
    if (!classId) {
        return Steinberg_kInvalidArgument;
    }
    if (component->kind == VST3GO_KIND_PROCESSOR) {
        memcpy(classId, component->controllerCid, sizeof(Steinberg_TUID));
        return Steinberg_kResultOk;
    }
    // Single component: the host queries IEditController on this object.
    memset(classId, 0, sizeof(Steinberg_TUID));
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setIoMode(void* thisInterface, Steinberg_Vst_IoMode mode) {
    return GoComponentSetIoMode(SELF_GO(thisInterface), mode);
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE component_getBusCount(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir) {
    return GoComponentGetBusCount(SELF_GO(thisInterface), type, dir);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getBusInfo(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, struct Steinberg_Vst_BusInfo* bus) {
    if (!bus) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentGetBusInfo(SELF_GO(thisInterface), type, dir, index, bus);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getRoutingInfo(void* thisInterface, struct Steinberg_Vst_RoutingInfo* inInfo, struct Steinberg_Vst_RoutingInfo* outInfo) {
    (void)thisInterface;
    (void)inInfo;
    (void)outInfo;
    return Steinberg_kNotImplemented;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_activateBus(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, Steinberg_TBool state) {
    return GoComponentActivateBus(SELF_GO(thisInterface), type, dir, index, state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setActive(void* thisInterface, Steinberg_TBool state) {
    return GoComponentSetActive(SELF_GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_setState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentSetState(SELF_GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE component_getState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoComponentGetState(SELF_GO(thisInterface), state);
}

static const struct Steinberg_Vst_IComponentVtbl componentVtbl = {
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

// IAudioProcessor

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setBusArrangements(void* thisInterface, Steinberg_Vst_SpeakerArrangement* inputs, Steinberg_int32 numIns, Steinberg_Vst_SpeakerArrangement* outputs, Steinberg_int32 numOuts) {
    if ((numIns > 0 && !inputs) || (numOuts > 0 && !outputs) || numIns < 0 || numOuts < 0) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioSetBusArrangements(GO(thisInterface), inputs, numIns, outputs, numOuts);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_getBusArrangement(void* thisInterface, Steinberg_Vst_BusDirection dir, Steinberg_int32 index, Steinberg_Vst_SpeakerArrangement* arr) {
    if (!arr) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioGetBusArrangement(GO(thisInterface), dir, index, arr);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_canProcessSampleSize(void* thisInterface, Steinberg_int32 symbolicSampleSize) {
    return GoAudioCanProcessSampleSize(GO(thisInterface), symbolicSampleSize);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_getLatencySamples(void* thisInterface) {
    return GoAudioGetLatencySamples(GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setupProcessing(void* thisInterface, struct Steinberg_Vst_ProcessSetup* setup) {
    if (!setup) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioSetupProcessing(GO(thisInterface), setup);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_setProcessing(void* thisInterface, Steinberg_TBool state) {
    return GoAudioSetProcessing(GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE audio_process(void* thisInterface, struct Steinberg_Vst_ProcessData* data) {
    if (!data) {
        return Steinberg_kInvalidArgument;
    }
    return GoAudioProcess(GO(thisInterface), data);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE audio_getTailSamples(void* thisInterface) {
    return GoAudioGetTailSamples(GO(thisInterface));
}

static const struct Steinberg_Vst_IAudioProcessorVtbl audioProcessorVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    audio_setBusArrangements,
    audio_getBusArrangement,
    audio_canProcessSampleSize,
    audio_getLatencySamples,
    audio_setupProcessing,
    audio_setProcessing,
    audio_process,
    audio_getTailSamples
};

// IProcessContextRequirements: request every ProcessContext field that
// process.TransportInfo exposes.

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

static const struct Steinberg_Vst_IProcessContextRequirementsVtbl contextRequirementsVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    requirements_getProcessContextRequirements
};

// IProgramListData

static Steinberg_tresult SMTG_STDMETHODCALLTYPE programdata_supported(void* thisInterface, Steinberg_Vst_ProgramListID listId) {
    return GoProgramDataSupported(GO(thisInterface), listId);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE programdata_get(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex, struct Steinberg_IBStream* data) {
    if (!data) {
        return Steinberg_kInvalidArgument;
    }
    return GoProgramDataGet(GO(thisInterface), listId, programIndex, data);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE programdata_set(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex, struct Steinberg_IBStream* data) {
    if (!data) {
        return Steinberg_kInvalidArgument;
    }
    return GoProgramDataSet(GO(thisInterface), listId, programIndex, data);
}

static const struct Steinberg_Vst_IProgramListDataVtbl programListDataVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    programdata_supported,
    programdata_get,
    programdata_set
};

// IEditController

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_initialize(void* thisInterface, struct Steinberg_FUnknown* context) {
    Component* component = OWNER(thisInterface);
    if (component->kind != VST3GO_KIND_CONTROLLER) {
        // Single component: initialize already ran through IComponent.
        return Steinberg_kResultOk;
    }
    storeHostContext(component, context);
    return GoControllerInitialize(component->goComponent);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_terminate(void* thisInterface) {
    Component* component = OWNER(thisInterface);
    if (component->kind != VST3GO_KIND_CONTROLLER) {
        return Steinberg_kResultOk;
    }
    Steinberg_tresult result = GoControllerTerminate(component->goComponent);
    releaseUnknown(&component->hostContext);
    return result;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setComponentState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerSetComponentState(GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerSetState(GO(thisInterface), state);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getState(void* thisInterface, struct Steinberg_IBStream* state) {
    if (!state) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetState(GO(thisInterface), state);
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE controller_getParameterCount(void* thisInterface) {
    return GoEditControllerGetParameterCount(GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParameterInfo(void* thisInterface, Steinberg_int32 paramIndex, struct Steinberg_Vst_ParameterInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParameterInfo(GO(thisInterface), paramIndex, info);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParamStringByValue(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue valueNormalized, Steinberg_Vst_String128 string) {
    if (!string) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParamStringByValue(GO(thisInterface), id, valueNormalized, string);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_getParamValueByString(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_TChar* string, Steinberg_Vst_ParamValue* valueNormalized) {
    if (!string || !valueNormalized) {
        return Steinberg_kInvalidArgument;
    }
    return GoEditControllerGetParamValueByString(GO(thisInterface), id, string, valueNormalized);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_normalizedParamToPlain(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue valueNormalized) {
    return GoEditControllerNormalizedParamToPlain(GO(thisInterface), id, valueNormalized);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_plainParamToNormalized(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue plainValue) {
    return GoEditControllerPlainParamToNormalized(GO(thisInterface), id, plainValue);
}

static Steinberg_Vst_ParamValue SMTG_STDMETHODCALLTYPE controller_getParamNormalized(void* thisInterface, Steinberg_Vst_ParamID id) {
    return GoEditControllerGetParamNormalized(GO(thisInterface), id);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setParamNormalized(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue value) {
    return GoEditControllerSetParamNormalized(GO(thisInterface), id, value);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE controller_setComponentHandler(void* thisInterface, struct Steinberg_Vst_IComponentHandler* handler) {
    return GoEditControllerSetComponentHandler(GO(thisInterface), handler);
}

static struct Steinberg_IPlugView* SMTG_STDMETHODCALLTYPE controller_createView(void* thisInterface, Steinberg_FIDString name) {
    return (struct Steinberg_IPlugView*)GoEditControllerCreateView(GO(thisInterface), (char*)name);
}

static const struct Steinberg_Vst_IEditControllerVtbl editControllerVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
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

// IMidiMapping

static Steinberg_tresult SMTG_STDMETHODCALLTYPE midi_getMidiControllerAssignment(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_Vst_CtrlNumber midiControllerNumber, Steinberg_Vst_ParamID* id) {
    if (!id) {
        return Steinberg_kInvalidArgument;
    }
    return GoMidiMappingGetAssignment(GO(thisInterface), busIndex, channel, midiControllerNumber, id);
}

static const struct Steinberg_Vst_IMidiMappingVtbl midiMappingVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    midi_getMidiControllerAssignment
};

// IMidiMapping2

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE midi2_getNumMidi2ControllerAssignments(void* thisInterface, Steinberg_Vst_BusDirections direction) {
    return GoMidiMapping2Count(GO(thisInterface), direction);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE midi2_getMidi2ControllerAssignments(void* thisInterface, Steinberg_Vst_BusDirections direction, const struct Steinberg_Vst_Midi2ControllerParamIDAssignmentList* list) {
    if (!list) {
        return Steinberg_kInvalidArgument;
    }
    return GoMidiMapping2Fill(GO(thisInterface), direction, (struct Steinberg_Vst_Midi2ControllerParamIDAssignmentList*)list);
}

static Steinberg_uint32 SMTG_STDMETHODCALLTYPE midi2_getNumMidi1ControllerAssignments(void* thisInterface, Steinberg_Vst_BusDirections direction) {
    return GoMidiMapping1Count(GO(thisInterface), direction);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE midi2_getMidi1ControllerAssignments(void* thisInterface, Steinberg_Vst_BusDirections direction, const struct Steinberg_Vst_Midi1ControllerParamIDAssignmentList* list) {
    if (!list) {
        return Steinberg_kInvalidArgument;
    }
    return GoMidiMapping1Fill(GO(thisInterface), direction, (struct Steinberg_Vst_Midi1ControllerParamIDAssignmentList*)list);
}

static const struct Steinberg_Vst_IMidiMapping2Vtbl midiMapping2Vtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    midi2_getNumMidi2ControllerAssignments,
    midi2_getMidi2ControllerAssignments,
    midi2_getNumMidi1ControllerAssignments,
    midi2_getMidi1ControllerAssignments
};

// IMidiLearn, IMidiLearn2

static Steinberg_tresult SMTG_STDMETHODCALLTYPE learn_onLiveMIDIControllerInput(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_Vst_CtrlNumber midiCC) {
    return GoMidiLearn1(GO(thisInterface), busIndex, channel, midiCC);
}

static const struct Steinberg_Vst_IMidiLearnVtbl midiLearnVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    learn_onLiveMIDIControllerInput
};

static Steinberg_tresult SMTG_STDMETHODCALLTYPE learn2_onLiveMidi2ControllerInput(void* thisInterface, Steinberg_Vst_BusIndex index, Steinberg_Vst_MidiChannel channel, struct Steinberg_Vst_Midi2Controller midiCC) {
    return GoMidiLearn2(GO(thisInterface), index, channel, midiCC.byte1, midiCC.byte2);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE learn2_onLiveMidi1ControllerInput(void* thisInterface, Steinberg_Vst_BusIndex index, Steinberg_Vst_MidiChannel channel, Steinberg_Vst_CtrlNumber midiCC) {
    return GoMidiLearn1(GO(thisInterface), index, channel, midiCC);
}

static const struct Steinberg_Vst_IMidiLearn2Vtbl midiLearn2Vtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    learn2_onLiveMidi2ControllerInput,
    learn2_onLiveMidi1ControllerInput
};

// IUnitInfo

static Steinberg_int32 SMTG_STDMETHODCALLTYPE unit_getUnitCount(void* thisInterface) {
    return GoUnitCount(GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getUnitInfo(void* thisInterface, Steinberg_int32 unitIndex, struct Steinberg_Vst_UnitInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoUnitInfo(GO(thisInterface), unitIndex, info);
}

static Steinberg_int32 SMTG_STDMETHODCALLTYPE unit_getProgramListCount(void* thisInterface) {
    return GoProgramListCount(GO(thisInterface));
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getProgramListInfo(void* thisInterface, Steinberg_int32 listIndex, struct Steinberg_Vst_ProgramListInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoProgramListInfo(GO(thisInterface), listIndex, info);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getProgramName(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex, Steinberg_Vst_String128 name) {
    if (!name) {
        return Steinberg_kInvalidArgument;
    }
    return GoProgramName(GO(thisInterface), listId, programIndex, name);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getProgramInfo(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex, Steinberg_Vst_CString attributeId, Steinberg_Vst_String128 attributeValue) {
    (void)thisInterface;
    (void)listId;
    (void)programIndex;
    (void)attributeId;
    (void)attributeValue;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_hasProgramPitchNames(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex) {
    return GoProgramHasPitchNames(GO(thisInterface), listId, programIndex);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getProgramPitchName(void* thisInterface, Steinberg_Vst_ProgramListID listId, Steinberg_int32 programIndex, Steinberg_int16 midiPitch, Steinberg_Vst_String128 name) {
    if (!name) {
        return Steinberg_kInvalidArgument;
    }
    return GoProgramPitchName(GO(thisInterface), listId, programIndex, midiPitch, name);
}

static Steinberg_Vst_UnitID SMTG_STDMETHODCALLTYPE unit_getSelectedUnit(void* thisInterface) {
    (void)thisInterface;
    return Steinberg_Vst_kRootUnitId;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_selectUnit(void* thisInterface, Steinberg_Vst_UnitID unitId) {
    (void)thisInterface;
    (void)unitId;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_getUnitByBus(void* thisInterface, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, Steinberg_int32 busIndex, Steinberg_int32 channel, Steinberg_Vst_UnitID* unitId) {
    (void)thisInterface;
    (void)type;
    (void)dir;
    (void)busIndex;
    (void)channel;
    (void)unitId;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE unit_setUnitProgramData(void* thisInterface, Steinberg_int32 listOrUnitId, Steinberg_int32 programIndex, struct Steinberg_IBStream* data) {
    (void)thisInterface;
    (void)listOrUnitId;
    (void)programIndex;
    (void)data;
    return Steinberg_kNotImplemented;
}

static const struct Steinberg_Vst_IUnitInfoVtbl unitInfoVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    unit_getUnitCount,
    unit_getUnitInfo,
    unit_getProgramListCount,
    unit_getProgramListInfo,
    unit_getProgramName,
    unit_getProgramInfo,
    unit_hasProgramPitchNames,
    unit_getProgramPitchName,
    unit_getSelectedUnit,
    unit_selectUnit,
    unit_getUnitByBus,
    unit_setUnitProgramData
};

// INoteExpressionController

static Steinberg_int32 SMTG_STDMETHODCALLTYPE noteexp_getNoteExpressionCount(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel) {
    return GoNoteExpressionCount(GO(thisInterface), busIndex, channel);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE noteexp_getNoteExpressionInfo(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_int32 noteExpressionIndex, struct Steinberg_Vst_NoteExpressionTypeInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoNoteExpressionInfo(GO(thisInterface), busIndex, channel, noteExpressionIndex, info);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE noteexp_getNoteExpressionStringByValue(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_Vst_NoteExpressionTypeID id, Steinberg_Vst_NoteExpressionValue valueNormalized, Steinberg_Vst_String128 string) {
    if (!string) {
        return Steinberg_kInvalidArgument;
    }
    return GoNoteExpressionString(GO(thisInterface), busIndex, channel, id, valueNormalized, string);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE noteexp_getNoteExpressionValueByString(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_Vst_NoteExpressionTypeID id, const Steinberg_Vst_TChar* string, Steinberg_Vst_NoteExpressionValue* valueNormalized) {
    if (!string || !valueNormalized) {
        return Steinberg_kInvalidArgument;
    }
    return GoNoteExpressionValue(GO(thisInterface), busIndex, channel, id, (Steinberg_Vst_TChar*)string, valueNormalized);
}

static const struct Steinberg_Vst_INoteExpressionControllerVtbl noteExpressionVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    noteexp_getNoteExpressionCount,
    noteexp_getNoteExpressionInfo,
    noteexp_getNoteExpressionStringByValue,
    noteexp_getNoteExpressionValueByString
};

// INoteExpressionPhysicalUIMapping

static Steinberg_tresult SMTG_STDMETHODCALLTYPE physicalui_getPhysicalUIMapping(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, struct Steinberg_Vst_PhysicalUIMapList* list) {
    if (!list) {
        return Steinberg_kInvalidArgument;
    }
    return GoPhysicalUIMapping(GO(thisInterface), busIndex, channel, list);
}

static const struct Steinberg_Vst_INoteExpressionPhysicalUIMappingVtbl physicalUIVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    physicalui_getPhysicalUIMapping
};

// IKeyswitchController

static Steinberg_int32 SMTG_STDMETHODCALLTYPE keyswitch_getKeyswitchCount(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel) {
    return GoKeyswitchCount(GO(thisInterface), busIndex, channel);
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE keyswitch_getKeyswitchInfo(void* thisInterface, Steinberg_int32 busIndex, Steinberg_int16 channel, Steinberg_int32 keySwitchIndex, struct Steinberg_Vst_KeyswitchInfo* info) {
    if (!info) {
        return Steinberg_kInvalidArgument;
    }
    return GoKeyswitchInfo(GO(thisInterface), busIndex, channel, keySwitchIndex, info);
}

static const struct Steinberg_Vst_IKeyswitchControllerVtbl keyswitchVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    keyswitch_getKeyswitchCount,
    keyswitch_getKeyswitchInfo
};

// IInfoListener

static Steinberg_tresult SMTG_STDMETHODCALLTYPE info_setChannelContextInfos(void* thisInterface, struct Steinberg_Vst_IAttributeList* list) {
    if (!list) {
        return Steinberg_kInvalidArgument;
    }
    VST3GoChannelInfo info;
    vst3go_read_channel_info(list, &info);
    return GoSetChannelInfo(GO(thisInterface), &info);
}

static const struct Steinberg_Vst_ChannelContext_IInfoListenerVtbl infoListenerVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    info_setChannelContextInfos
};

// IConnectionPoint (distributed plugins). Messages carry one binary
// attribute named "data".

static const char* messageDataKey = "data";

static Steinberg_tresult SMTG_STDMETHODCALLTYPE connection_connect(void* thisInterface, struct Steinberg_Vst_IConnectionPoint* other) {
    Component* component = OWNER(thisInterface);
    if (!other) {
        return Steinberg_kInvalidArgument;
    }
    if (component->peer) {
        return Steinberg_kResultFalse;
    }
    other->lpVtbl->addRef(other);
    component->peer = other;
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE connection_disconnect(void* thisInterface, struct Steinberg_Vst_IConnectionPoint* other) {
    Component* component = OWNER(thisInterface);
    if (!other || other != component->peer) {
        return Steinberg_kInvalidArgument;
    }
    releaseUnknown((struct Steinberg_FUnknown**)&component->peer);
    return Steinberg_kResultOk;
}

static Steinberg_tresult SMTG_STDMETHODCALLTYPE connection_notify(void* thisInterface, struct Steinberg_Vst_IMessage* message) {
    if (!message) {
        return Steinberg_kInvalidArgument;
    }
    Steinberg_FIDString id = message->lpVtbl->getMessageID(message);
    if (!id) {
        return Steinberg_kResultFalse;
    }
    const void* data = NULL;
    Steinberg_uint32 size = 0;
    struct Steinberg_Vst_IAttributeList* attributes = message->lpVtbl->getAttributes(message);
    if (attributes && attributes->lpVtbl->getBinary(attributes, messageDataKey, &data, &size) != Steinberg_kResultOk) {
        data = NULL;
        size = 0;
    }
    GoConnectionNotify(GO(thisInterface), (char*)id, (void*)data, size);
    return Steinberg_kResultOk;
}

static const struct Steinberg_Vst_IConnectionPointVtbl connectionPointVtbl = {
    sub_queryInterface,
    sub_addRef,
    sub_release,
    connection_connect,
    connection_disconnect,
    connection_notify
};

Steinberg_tresult vst3go_send_message(void* thisComponent, const char* id, const void* data, uint32_t size) {
    Component* component = (Component*)thisComponent;
    if (!component || !component->peer || !component->hostContext) {
        return Steinberg_kResultFalse;
    }

    struct Steinberg_Vst_IHostApplication* app = NULL;
    if (component->hostContext->lpVtbl->queryInterface(component->hostContext, Steinberg_Vst_IHostApplication_iid, (void**)&app) != Steinberg_kResultOk || !app) {
        return Steinberg_kResultFalse;
    }
    Steinberg_TUID cid;
    Steinberg_TUID iid;
    memcpy(cid, Steinberg_Vst_IMessage_iid, sizeof(Steinberg_TUID));
    memcpy(iid, Steinberg_Vst_IMessage_iid, sizeof(Steinberg_TUID));
    struct Steinberg_Vst_IMessage* message = NULL;
    Steinberg_tresult result = app->lpVtbl->createInstance(app, cid, iid, (void**)&message);
    app->lpVtbl->release(app);
    if (result != Steinberg_kResultOk || !message) {
        return Steinberg_kResultFalse;
    }

    message->lpVtbl->setMessageID(message, id);
    struct Steinberg_Vst_IAttributeList* attributes = message->lpVtbl->getAttributes(message);
    if (attributes && size > 0) {
        attributes->lpVtbl->setBinary(attributes, messageDataKey, data, size);
    }
    result = component->peer->lpVtbl->notify(component->peer, message);
    message->lpVtbl->release(message);
    return result;
}

// Object creation and interface lookup

static void initSub(SubInterface* sub, const void* vtbl, Component* component) {
    sub->lpVtbl = vtbl;
    sub->component = component;
}

void* createComponent(uintptr_t goComponent, int32_t kind, int32_t caps, const char* controllerCid) {
    Component* component = (Component*)calloc(1, sizeof(Component));
    if (!component) {
        return NULL;
    }

    component->lpVtbl = &componentVtbl;
    initSub(&component->audioProcessor, &audioProcessorVtbl, component);
    initSub(&component->contextRequirements, &contextRequirementsVtbl, component);
    initSub(&component->programListData, &programListDataVtbl, component);
    initSub(&component->editController, &editControllerVtbl, component);
    initSub(&component->midiMapping, &midiMappingVtbl, component);
    initSub(&component->midiMapping2, &midiMapping2Vtbl, component);
    initSub(&component->midiLearn, &midiLearnVtbl, component);
    initSub(&component->midiLearn2, &midiLearn2Vtbl, component);
    initSub(&component->unitInfo, &unitInfoVtbl, component);
    initSub(&component->noteExpression, &noteExpressionVtbl, component);
    initSub(&component->physicalUI, &physicalUIVtbl, component);
    initSub(&component->keyswitch, &keyswitchVtbl, component);
    initSub(&component->infoListener, &infoListenerVtbl, component);
    initSub(&component->connectionPoint, &connectionPointVtbl, component);
    component->refCount = 1;
    component->kind = kind;
    component->caps = caps;
    component->goComponent = goComponent;
    if (controllerCid) {
        memcpy(component->controllerCid, controllerCid, sizeof(Steinberg_TUID));
    }
    return component;
}

static void* findInterface(Component* component, const Steinberg_TUID iid) {
    int processorSide = component->kind != VST3GO_KIND_CONTROLLER;
    int controllerSide = component->kind != VST3GO_KIND_PROCESSOR;
    int caps = component->caps;

    if (iid_equal(iid, Steinberg_FUnknown_iid) || iid_equal(iid, Steinberg_IPluginBase_iid)) {
        return processorSide ? (void*)component : (void*)&component->editController;
    }
    if (processorSide) {
        if (iid_equal(iid, Steinberg_Vst_IComponent_iid)) {
            return component;
        }
        if (iid_equal(iid, Steinberg_Vst_IAudioProcessor_iid)) {
            return &component->audioProcessor;
        }
        if (iid_equal(iid, Steinberg_Vst_IProcessContextRequirements_iid)) {
            return &component->contextRequirements;
        }
        if ((caps & VST3GO_CAP_PROGRAM_DATA) && iid_equal(iid, Steinberg_Vst_IProgramListData_iid)) {
            return &component->programListData;
        }
    }
    if (controllerSide) {
        if (iid_equal(iid, Steinberg_Vst_IEditController_iid)) {
            return &component->editController;
        }
        if (iid_equal(iid, Steinberg_Vst_IMidiMapping_iid)) {
            return &component->midiMapping;
        }
        if (iid_equal(iid, Steinberg_Vst_IMidiMapping2_iid)) {
            return &component->midiMapping2;
        }
        if ((caps & VST3GO_CAP_MIDI_LEARN) && iid_equal(iid, Steinberg_Vst_IMidiLearn_iid)) {
            return &component->midiLearn;
        }
        if ((caps & VST3GO_CAP_MIDI_LEARN) && iid_equal(iid, Steinberg_Vst_IMidiLearn2_iid)) {
            return &component->midiLearn2;
        }
        if ((caps & VST3GO_CAP_UNITS) && iid_equal(iid, Steinberg_Vst_IUnitInfo_iid)) {
            return &component->unitInfo;
        }
        if ((caps & VST3GO_CAP_NOTE_EXPRESSION) && iid_equal(iid, Steinberg_Vst_INoteExpressionController_iid)) {
            return &component->noteExpression;
        }
        if ((caps & VST3GO_CAP_PHYSICAL_UI) && iid_equal(iid, Steinberg_Vst_INoteExpressionPhysicalUIMapping_iid)) {
            return &component->physicalUI;
        }
        if ((caps & VST3GO_CAP_KEYSWITCH) && iid_equal(iid, Steinberg_Vst_IKeyswitchController_iid)) {
            return &component->keyswitch;
        }
        if ((caps & VST3GO_CAP_INFO_LISTENER) && iid_equal(iid, Steinberg_Vst_ChannelContext_IInfoListener_iid)) {
            return &component->infoListener;
        }
    }
    if (component->kind != VST3GO_KIND_SINGLE && iid_equal(iid, Steinberg_Vst_IConnectionPoint_iid)) {
        return &component->connectionPoint;
    }
    return NULL;
}

Steinberg_tresult componentQueryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    Component* component = (Component*)thisInterface;
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }
    void* result = iid ? findInterface(component, iid) : NULL;
    if (!result) {
        *obj = NULL;
        return Steinberg_kNoInterface;
    }
    componentAddRef(component);
    *obj = result;
    return Steinberg_kResultOk;
}
