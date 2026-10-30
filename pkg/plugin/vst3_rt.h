#ifndef VST3GO_VST3_RT_H
#define VST3GO_VST3_RT_H

// VST3 process calls through the deadline guard (rtguard.h). Each block is
// copied into a job the worker owns: audio, parameter changes, events and
// the process context, the lists behind small IParameterChanges and
// IEventList implementations. After the worker ran the Go processor on
// the copy in time, the output audio, parameter changes and events are
// copied to the host's. A late block outputs silence; the input of a
// block skipped because a late one is still running, and input that does
// not fit a job, is carried into the next (events at offset 0, the last
// value of each parameter), so no note-off or parameter change is lost.
// A parameter flush (a block of no samples) is a job too, so in real-time
// mode the audio thread never enters Go.

#include "../../include/vst3/vst3_c_api.h"
#include <stdint.h>

typedef struct VST3GoRT VST3GoRT;

// vst3go_rt_new prepares the guard for blocks of up to maxSamples samples
// of sampleSize (kSample32 or kSample64) on up to channels channels in
// total (inputs and outputs), waiting at most share of each block's
// duration. It returns NULL when it cannot allocate or start the worker.
VST3GoRT* vst3go_rt_new(uintptr_t goComponent, double sampleRate, int32_t maxSamples, int32_t sampleSize, int32_t channels, double share);

// vst3go_rt_free waits for a running block and frees rt (NULL is fine).
void vst3go_rt_free(VST3GoRT* rt);

// vst3go_rt_process processes one block (the audio thread's call).
Steinberg_tresult vst3go_rt_process(VST3GoRT* rt, struct Steinberg_Vst_ProcessData* data);

#endif
