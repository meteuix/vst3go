#ifndef VST3GO_VST3_COMPONENT_H
#define VST3GO_VST3_COMPONENT_H

#include "../../include/vst3/vst3_c_api.h"
#include "vst3_bridge.h"

// Object kinds. A single-component object implements the processor and the
// controller interfaces; a distributed plugin uses one object of each of the
// other two kinds, connected through IConnectionPoint.
#define VST3GO_KIND_SINGLE 0
#define VST3GO_KIND_PROCESSOR 1
#define VST3GO_KIND_CONTROLLER 2

// Optional interfaces, exposed only when the Go processor implements them.
#define VST3GO_CAP_UNITS (1 << 0)
#define VST3GO_CAP_PROGRAM_DATA (1 << 1)
#define VST3GO_CAP_NOTE_EXPRESSION (1 << 2)
#define VST3GO_CAP_PHYSICAL_UI (1 << 3)
#define VST3GO_CAP_KEYSWITCH (1 << 4)
#define VST3GO_CAP_MIDI_LEARN (1 << 5)
#define VST3GO_CAP_INFO_LISTENER (1 << 6)

// Creates the COM object for one plugin instance. goComponent is the opaque Go
// handle passed back to every Go callback; controllerCid (16 bytes) is the
// controller class of a processor-kind object. The object starts with one
// reference.
void* createComponent(uintptr_t goComponent, int32_t kind, int32_t caps, const char* controllerCid);
Steinberg_tresult componentQueryInterface(void* component, const Steinberg_TUID iid, void** obj);
Steinberg_uint32 componentRelease(void* component);

// Sends a message to the connected peer object (distributed plugins only).
Steinberg_tresult vst3go_send_message(void* component, const char* id, const void* data, uint32_t size);

// IComponent
extern Steinberg_tresult GoComponentInitialize(uintptr_t component);
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

// IProgramListData
extern Steinberg_tresult GoProgramDataSupported(uintptr_t component, int32_t listId);
extern Steinberg_tresult GoProgramDataGet(uintptr_t component, int32_t listId, int32_t programIndex, void* stream);
extern Steinberg_tresult GoProgramDataSet(uintptr_t component, int32_t listId, int32_t programIndex, void* stream);

// IEditController
extern Steinberg_tresult GoControllerInitialize(uintptr_t component);
extern Steinberg_tresult GoControllerTerminate(uintptr_t component);
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

// IMidiMapping, IMidiMapping2
extern Steinberg_tresult GoMidiMappingGetAssignment(uintptr_t component, int32_t busIndex, int16_t channel, int16_t controller, Steinberg_Vst_ParamID* id);
extern uint32_t GoMidiMapping1Count(uintptr_t component, int32_t direction);
extern Steinberg_tresult GoMidiMapping1Fill(uintptr_t component, int32_t direction, struct Steinberg_Vst_Midi1ControllerParamIDAssignmentList* list);
extern uint32_t GoMidiMapping2Count(uintptr_t component, int32_t direction);
extern Steinberg_tresult GoMidiMapping2Fill(uintptr_t component, int32_t direction, struct Steinberg_Vst_Midi2ControllerParamIDAssignmentList* list);

// IMidiLearn, IMidiLearn2
extern Steinberg_tresult GoMidiLearn1(uintptr_t component, int32_t busIndex, int16_t channel, int16_t controller);
extern Steinberg_tresult GoMidiLearn2(uintptr_t component, int32_t busIndex, uint8_t channel, uint8_t byte1, uint8_t byte2);

// IUnitInfo
extern int32_t GoUnitCount(uintptr_t component);
extern Steinberg_tresult GoUnitInfo(uintptr_t component, int32_t index, struct Steinberg_Vst_UnitInfo* info);
extern int32_t GoProgramListCount(uintptr_t component);
extern Steinberg_tresult GoProgramListInfo(uintptr_t component, int32_t index, struct Steinberg_Vst_ProgramListInfo* info);
extern Steinberg_tresult GoProgramName(uintptr_t component, int32_t listId, int32_t programIndex, Steinberg_Vst_TChar* name);
extern Steinberg_tresult GoProgramHasPitchNames(uintptr_t component, int32_t listId, int32_t programIndex);
extern Steinberg_tresult GoProgramPitchName(uintptr_t component, int32_t listId, int32_t programIndex, int16_t pitch, Steinberg_Vst_TChar* name);

// INoteExpressionController, INoteExpressionPhysicalUIMapping
extern int32_t GoNoteExpressionCount(uintptr_t component, int32_t busIndex, int16_t channel);
extern Steinberg_tresult GoNoteExpressionInfo(uintptr_t component, int32_t busIndex, int16_t channel, int32_t index, struct Steinberg_Vst_NoteExpressionTypeInfo* info);
extern Steinberg_tresult GoNoteExpressionString(uintptr_t component, int32_t busIndex, int16_t channel, uint32_t typeId, double value, Steinberg_Vst_TChar* string);
extern Steinberg_tresult GoNoteExpressionValue(uintptr_t component, int32_t busIndex, int16_t channel, uint32_t typeId, Steinberg_Vst_TChar* string, double* value);
extern Steinberg_tresult GoPhysicalUIMapping(uintptr_t component, int32_t busIndex, int16_t channel, struct Steinberg_Vst_PhysicalUIMapList* list);

// IKeyswitchController
extern int32_t GoKeyswitchCount(uintptr_t component, int32_t busIndex, int16_t channel);
extern Steinberg_tresult GoKeyswitchInfo(uintptr_t component, int32_t busIndex, int16_t channel, int32_t index, struct Steinberg_Vst_KeyswitchInfo* info);

// IInfoListener
extern Steinberg_tresult GoSetChannelInfo(uintptr_t component, VST3GoChannelInfo* info);

// IConnectionPoint
extern void GoConnectionNotify(uintptr_t component, char* id, void* data, uint32_t size);

// Lifecycle
extern void GoReleaseComponent(uintptr_t component);

#endif // VST3GO_VST3_COMPONENT_H
