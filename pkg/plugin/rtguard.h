#ifndef VST3GO_RTGUARD_H
#define VST3GO_RTGUARD_H

// Deadline guard for the audio callback. The host's audio thread hands a
// block to a real-time worker thread, which runs the Go processor, and
// waits for it until a deadline. A block that misses the
// deadline is abandoned: the caller outputs silence and the worker
// finishes it in the background, so a stall in Go (garbage collection,
// scheduling) costs the plugin one block instead of stalling the host.
//
// The caller copies the block's input into memory the worker owns before
// rtguard_run and copies the output back after a successful run: the
// worker may still be using its job after the caller has returned.
//
// rtguard_idle and rtguard_run are for one audio thread at a time and do
// not allocate or lock; rtguard_entered is for the job; rtguard_new,
// rtguard_quiesce and rtguard_free are for other threads.

#include <stdint.h>

typedef struct VST3GoRTGuard VST3GoRTGuard;

typedef void (*rtguard_job)(void* arg);

enum {
    RTGUARD_DONE = 0, // the job ran in time
    RTGUARD_LATE = 1, // the deadline passed; the job finishes in the background
};

// rtguard_new returns a guard whose rtguard_run runs job(arg) on a worker
// thread. The binary's guards share their workers: each adds one, scheduled
// as a real-time thread with the given period (where the platform allows)
// and with denormals flushed (rtguard_flush_denormals). The worker first
// calls enter and rtguard_new waits for it: the thread's first call into Go
// (binding it to the runtime) happens here, not in a job on the audio
// thread's clock. It returns NULL when the thread cannot start.
VST3GoRTGuard* rtguard_new(rtguard_job job, void* arg, void (*enter)(void), int64_t period_ns);

// rtguard_idle reports whether the guard's job is not running: a late job
// may still be. Only an idle guard may be given a new job.
int rtguard_idle(VST3GoRTGuard* g);

// rtguard_run runs the job on a worker and waits at most timeout_ns.
int rtguard_run(VST3GoRTGuard* g, int64_t timeout_ns);

// rtguard_entered is called by the job once it got into Go: while another
// job of the binary is late, a job that has not within RTGUARD_ENTRY_NS is
// given up (see lateJobs in rtguard.c).
void rtguard_entered(VST3GoRTGuard* g);

// rtguard_quiesce waits until no job is running.
void rtguard_quiesce(VST3GoRTGuard* g);

// rtguard_free waits for a running job, stops a worker and frees g.
void rtguard_free(VST3GoRTGuard* g);

// rtguard_now_ns is a monotonic clock.
int64_t rtguard_now_ns(void);

// rtguard_flush_denormals makes the calling thread's float arithmetic flush
// denormal numbers to zero on x86 (FTZ and DAZ), where they take a hundred
// times longer, as audio code expects, and returns the previous mode for
// rtguard_restore_fp. Calls into Go on a host's thread are wrapped in the
// pair. ARM cores handle denormals at full speed; it does nothing there.
typedef uint64_t rtguard_fp;
rtguard_fp rtguard_flush_denormals(void);
void rtguard_restore_fp(rtguard_fp saved);

#endif
