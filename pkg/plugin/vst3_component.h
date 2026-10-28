#ifndef VST3GO_VST3_COMPONENT_H
#define VST3GO_VST3_COMPONENT_H

#include "../../include/vst3/vst3_c_api.h"

// Creates the COM object for one plugin instance. goComponent is the opaque Go
// handle passed back to every Go callback. The object starts with one reference.
void* createComponent(uintptr_t goComponent);
Steinberg_tresult componentQueryInterface(void* component, const Steinberg_TUID iid, void** obj);
Steinberg_uint32 componentRelease(void* component);

// IComponent
extern Steinberg_tresult GoComponentInitialize(uintptr_t component, void* context);
extern Steinberg_tresult GoComponentTerminate(uintptr_t component);
extern Steinberg_tresult GoComponentSetIoMode(uintptr_t component, int32_t mode);
extern int32_t GoComponentGetBusCount(uintptr_t component, int32_t type, int32_t dir);
extern Steinberg_tresult GoComponentGetBusInfo(uintptr_t component, int32_t type, int32_t dir, int32_t index, struct Steinberg_Vst_BusInfo* bus);
extern Steinberg_tresult GoComponentActivateBus(uintptr_t component, int32_t type, int32_t dir, int32_t index, int32_t state);
extern Steinberg_tresult GoComponentSetActive(uintptr_t component, int32_t state);
extern Steinberg_tresult GoComponentSetState(uintptr_t component, void* state);
extern Steinberg_tresult GoComponentGetState(uintptr_t component, void* state);

// IAudioProcessor
extern Steinberg_tresult GoAudioSetBusArrangements(uintptr_t component, Steinberg_Vst_SpeakerArrangement* inputs, int32_t numIns, Steinberg_Vst_SpeakerArrangement* outputs, int32_t numOuts);
extern Steinberg_tresult GoAudioGetBusArrangement(uintptr_t component, int32_t dir, int32_t index, Steinberg_Vst_SpeakerArrangement* arr);
extern Steinberg_tresult GoAudioCanProcessSampleSize(uintptr_t component, int32_t symbolicSampleSize);
extern uint32_t GoAudioGetLatencySamples(uintptr_t component);
extern Steinberg_tresult GoAudioSetupProcessing(uintptr_t component, struct Steinberg_Vst_ProcessSetup* setup);
extern Steinberg_tresult GoAudioSetProcessing(uintptr_t component, int32_t state);
extern Steinberg_tresult GoAudioProcess(uintptr_t component, struct Steinberg_Vst_ProcessData* data);
extern uint32_t GoAudioGetTailSamples(uintptr_t component);

// IEditController
extern Steinberg_tresult GoEditControllerSetComponentState(uintptr_t component, void* state);
extern Steinberg_tresult GoEditControllerSetState(uintptr_t component, void* state);
extern Steinberg_tresult GoEditControllerGetState(uintptr_t component, void* state);
extern int32_t GoEditControllerGetParameterCount(uintptr_t component);
extern Steinberg_tresult GoEditControllerGetParameterInfo(uintptr_t component, int32_t paramIndex, struct Steinberg_Vst_ParameterInfo* info);
extern Steinberg_tresult GoEditControllerGetParamStringByValue(uintptr_t component, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue valueNormalized, Steinberg_Vst_TChar* string);
extern Steinberg_tresult GoEditControllerGetParamValueByString(uintptr_t component, Steinberg_Vst_ParamID id, Steinberg_Vst_TChar* string, Steinberg_Vst_ParamValue* valueNormalized);
extern double GoEditControllerNormalizedParamToPlain(uintptr_t component, uint32_t id, double valueNormalized);
extern double GoEditControllerPlainParamToNormalized(uintptr_t component, uint32_t id, double plainValue);
extern double GoEditControllerGetParamNormalized(uintptr_t component, uint32_t id);
extern Steinberg_tresult GoEditControllerSetParamNormalized(uintptr_t component, uint32_t id, double value);
extern Steinberg_tresult GoEditControllerSetComponentHandler(uintptr_t component, void* handler);
extern void* GoEditControllerCreateView(uintptr_t component, char* name);

// IMidiMapping
extern Steinberg_tresult GoMidiMappingGetAssignment(uintptr_t component, int32_t busIndex, int16_t channel, int16_t controller, Steinberg_Vst_ParamID* id);

// Lifecycle
extern void GoReleaseComponent(uintptr_t component);

#endif // VST3GO_VST3_COMPONENT_H
