#ifndef VST3GO_CLAP_RT_H
#define VST3GO_CLAP_RT_H

// CLAP process calls through the deadline guard (rtguard.h), the CLAP
// counterpart of vst3_rt.h: each block's audio, events and transport are
// copied into a job the worker owns, and its output audio and events are
// copied back when it ran in time. A late block outputs silence; the input
// events of a block skipped because a late one is still running, and those
// that do not fit a job, move to the start of the next. Parameter flushes
// on the audio thread and resets run as jobs too, so in real-time mode the
// audio thread never enters Go. AU and LV2 wrap the CLAP plugin, so they go
// through it too.

#include "clap_bridge.h"

typedef struct VST3GoClapRT VST3GoClapRT;

// vst3go_clap_rt_new prepares the guard for blocks of up to maxFrames on up
// to channels channels in total, waiting at most share of each block's
// duration. It returns NULL when it cannot allocate or start the worker.
VST3GoClapRT* vst3go_clap_rt_new(VST3GoClapPlugin* plugin, double sampleRate, uint32_t maxFrames, uint32_t channels, double share);

// vst3go_clap_rt_free waits for a running block, applies a deferred reset
// and frees rt (NULL is fine).
void vst3go_clap_rt_free(VST3GoClapRT* rt);

// vst3go_clap_rt_process processes one block (the audio thread's call).
clap_process_status vst3go_clap_rt_process(VST3GoClapRT* rt, const clap_process_t* process);

// vst3go_clap_rt_flush is params.flush on the audio thread: through the
// worker, or on this thread in offline mode.
void vst3go_clap_rt_flush(VST3GoClapRT* rt, const clap_input_events_t* in, const clap_output_events_t* out);

// vst3go_clap_rt_reset defers a reset to the start of the next job.
void vst3go_clap_rt_reset(VST3GoClapRT* rt);

#endif
