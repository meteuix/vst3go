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
    int32_t controlNumber; // legacy MIDI CC out
    int32_t controlValue;
    int32_t controlValue2;
    const uint8_t* data;
    uint32_t dataSize;
    uint32_t dataType;
} VST3GoEvent;

// Per-instance C scratch memory for out-parameters of real-time calls, so the
// audio thread never passes Go stack memory (which would escape) to C.
typedef struct VST3GoScratch {
    VST3GoEvent event;
    int32_t sampleOffset;
    double value;
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

// IComponentHandler helpers.
Steinberg_tresult vst3go_handler_begin_edit(void* handler, Steinberg_Vst_ParamID id);
Steinberg_tresult vst3go_handler_perform_edit(void* handler, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue value);
Steinberg_tresult vst3go_handler_end_edit(void* handler, Steinberg_Vst_ParamID id);
Steinberg_tresult vst3go_handler_restart(void* handler, int32_t flags);
void vst3go_unknown_add_ref(void* unknown);
void vst3go_unknown_release(void* unknown);

#endif // VST3GO_VST3_BRIDGE_H
