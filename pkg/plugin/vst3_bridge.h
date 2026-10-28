#ifndef VST3GO_VST3_BRIDGE_H
#define VST3GO_VST3_BRIDGE_H

#include "../../include/vst3/vst3_c_api.h"

#if defined(_WIN32)
#define VST3GO_EXPORT __declspec(dllexport)
#else
#define VST3GO_EXPORT __attribute__((visibility("default")))
#endif

// Factory callbacks implemented in Go.
extern void GoGetFactoryInfo(struct Steinberg_PFactoryInfo* info);
extern int32_t GoCountClasses(void);
extern Steinberg_tresult GoGetClassInfo2(int32_t index, struct Steinberg_PClassInfo2* info);
extern void* GoCreateInstance(char* cid, char* iid);

// Flattened VST3 event used to move events between C and Go without touching
// the C union from Go.
typedef struct VST3GoEvent {
    int32_t type;
    int32_t busIndex;
    int32_t sampleOffset;
    int32_t flags;
    int32_t channel;
    int32_t pitch;
    int32_t noteId;
    int32_t length;
    float tuning;
    float value; // velocity or pressure, 0..1
    uint32_t expressionType;
    double expressionValue;
    uint64_t expressionIntValue;
    int32_t controlNumber; // legacy MIDI CC out
    int32_t controlValue;
    int32_t controlValue2;
    int16_t root;     // chord/scale
    int16_t bassNote; // chord
    int16_t mask;     // chord/scale
    const uint8_t* data;
    uint32_t dataSize;
    uint32_t dataType;
    const Steinberg_char16* text; // chord/scale name, note expression text
    uint32_t textLen;             // in UTF-16 code units
} VST3GoEvent;

#define VST3GO_DATA_CAPACITY (64 * 1024)
#define VST3GO_TEXT_CAPACITY 8192

// Per-instance C scratch memory for real-time calls: out-parameters (so the
// audio thread never passes escaping Go memory to C) and payload storage for
// output events, which must stay valid until the host consumed them.
typedef struct VST3GoScratch {
    VST3GoEvent event;
    int32_t sampleOffset;
    double value;
    uint32_t dataUsed;
    uint32_t textUsed;
    uint8_t data[VST3GO_DATA_CAPACITY];
    Steinberg_char16 text[VST3GO_TEXT_CAPACITY];
} VST3GoScratch;

// Parameter change helpers (input and output queues).
int32_t vst3go_paramchanges_count(void* changes);
void* vst3go_paramchanges_queue(void* changes, int32_t index);
uint32_t vst3go_paramqueue_id(void* queue);
int32_t vst3go_paramqueue_count(void* queue);
Steinberg_tresult vst3go_paramqueue_point(void* queue, int32_t index, int32_t* sampleOffset, double* value);
Steinberg_tresult vst3go_paramchanges_add_point(void* changes, uint32_t id, int32_t sampleOffset, double value);

// Event list helpers.
int32_t vst3go_events_count(void* list);
Steinberg_tresult vst3go_events_get(void* list, int32_t index, VST3GoEvent* out);
Steinberg_tresult vst3go_events_add(void* list, const VST3GoEvent* in);

// Audio bus helpers.
float** vst3go_bus_channels32(struct Steinberg_Vst_AudioBusBuffers* bus);
double** vst3go_bus_channels64(struct Steinberg_Vst_AudioBusBuffers* bus);

// IComponentHandler helpers.
Steinberg_tresult vst3go_handler_begin_edit(void* handler, Steinberg_Vst_ParamID id);
Steinberg_tresult vst3go_handler_perform_edit(void* handler, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue value);
Steinberg_tresult vst3go_handler_end_edit(void* handler, Steinberg_Vst_ParamID id);
Steinberg_tresult vst3go_handler_restart(void* handler, int32_t flags);
void vst3go_unknown_add_ref(void* unknown);
void vst3go_unknown_release(void* unknown);

// Channel context (IInfoListener) attributes, flattened.
typedef struct VST3GoChannelInfo {
    Steinberg_Vst_String128 name;
    Steinberg_Vst_String128 uid;
    Steinberg_Vst_String128 indexNamespace;
    int64_t color;
    int64_t index;
    int64_t pluginLocation;
    int32_t hasColor;
    int32_t hasIndex;
    int32_t hasPluginLocation;
} VST3GoChannelInfo;

void vst3go_read_channel_info(void* attributeList, VST3GoChannelInfo* out);

#endif // VST3GO_VST3_BRIDGE_H
