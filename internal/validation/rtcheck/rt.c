//go:build darwin || linux

#include "rt.h"

#include <pthread.h>
#include <stdint.h>
#include <time.h>

#ifdef __APPLE__
#include <mach/mach.h>
#include <mach/mach_time.h>
#include <mach/thread_policy.h>
#else
#include <sched.h>
#endif

extern void GoRTProcess(void);

typedef struct {
	int n;
	int64_t period_ns;
	int64_t *durations;
	int realtime;
} rt_job;

static int64_t now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// make_realtime gives the calling thread the scheduling an audio device
// thread gets: a time-constraint policy on macOS, SCHED_FIFO on Linux
// (which needs CAP_SYS_NICE or an rtprio limit).
static int make_realtime(int64_t period_ns) {
#ifdef __APPLE__
	mach_timebase_info_data_t tb;
	mach_timebase_info(&tb);
	double ticks_per_ns = (double)tb.denom / tb.numer;
	thread_time_constraint_policy_data_t policy;
	policy.period = (uint32_t)(period_ns * ticks_per_ns);
	policy.computation = (uint32_t)(period_ns / 2 * ticks_per_ns);
	policy.constraint = (uint32_t)(period_ns * ticks_per_ns);
	policy.preemptible = 1;
	return thread_policy_set(pthread_mach_thread_np(pthread_self()), THREAD_TIME_CONSTRAINT_POLICY,
		(thread_policy_t)&policy, THREAD_TIME_CONSTRAINT_POLICY_COUNT) == KERN_SUCCESS;
#else
	struct sched_param sp = { .sched_priority = sched_get_priority_max(SCHED_FIFO) - 10 };
	return pthread_setschedparam(pthread_self(), SCHED_FIFO, &sp) == 0;
#endif
}

static void *rt_loop(void *arg) {
	rt_job *job = arg;
	job->realtime = make_realtime(job->period_ns);
	int64_t next = now_ns();
	for (int i = 0; i < job->n; i++) {
		int64_t start = now_ns();
		GoRTProcess();
		job->durations[i] = now_ns() - start;
		next += job->period_ns;
		int64_t wait = next - now_ns();
		if (wait > 0) {
			struct timespec ts = { wait / 1000000000, wait % 1000000000 };
			nanosleep(&ts, NULL);
		} else {
			next = now_ns();
		}
	}
	return NULL;
}

int rt_run(int n, int64_t period_ns, int64_t *durations) {
	rt_job job = { n, period_ns, durations, 0 };
	pthread_t thread;
	if (pthread_create(&thread, NULL, rt_loop, &job) != 0) {
		return -1;
	}
	pthread_join(thread, NULL);
	return job.realtime;
}
