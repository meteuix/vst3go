#include "rtguard.h"

#include <stdlib.h>

#if defined(_WIN32)
#include <windows.h>
#elif defined(__APPLE__)
#include <mach/mach.h>
#include <mach/mach_time.h>
#include <mach/semaphore.h>
#include <mach/thread_policy.h>
#include <pthread.h>
#include <time.h>
#else
#include <errno.h>
#include <pthread.h>
#include <sched.h>
#include <semaphore.h>
#include <time.h>
#endif

// The host spins this long before sleeping on the done semaphore: most
// blocks finish within it, and waking from a sleep costs more. It also
// spins the last stretch before the deadline, as timed waits wake late
// (by up to 4 ms in a virtual machine): it sleeps only until sleepSlack
// before the deadline, at most RTGUARD_SLEEP_NS at a time.
#define RTGUARD_SPIN_NS 30000
#define RTGUARD_SLEEP_NS 1000000
#define RTGUARD_SLACK_MAX_NS 20000000

// While a job of this binary is late, the others get this long to enter
// Go before they are abandoned too (see lateJobs).
#define RTGUARD_ENTRY_NS 100000

enum { STATE_FREE, STATE_QUEUED, STATE_DONE, STATE_ABANDONED };


// Semaphores and threads.

#if defined(_WIN32)
typedef HANDLE rt_sem;
typedef HANDLE rt_thread;
static int sem_make(rt_sem* s) { *s = CreateSemaphoreW(NULL, 0, 0x7fffffff, NULL); return *s != NULL; }
static void sem_signal(rt_sem* s) { ReleaseSemaphore(*s, 1, NULL); }
static void sem_block(rt_sem* s) { WaitForSingleObject(*s, INFINITE); }
static void sem_destroy_(rt_sem* s) { CloseHandle(*s); }
#elif defined(__APPLE__)
typedef semaphore_t rt_sem;
typedef pthread_t rt_thread;
static int sem_make(rt_sem* s) { return semaphore_create(mach_task_self(), s, SYNC_POLICY_FIFO, 0) == KERN_SUCCESS; }
static void sem_signal(rt_sem* s) { semaphore_signal(*s); }
static void sem_block(rt_sem* s) {
    while (semaphore_wait(*s) == KERN_ABORTED) {}
}
// sem_sleep waits for the semaphore for up to ns; it returns whether it
// got it.
static int sem_sleep(rt_sem* s, int64_t ns) {
    mach_timespec_t t = { (unsigned int)(ns / 1000000000), (clock_res_t)(ns % 1000000000) };
    return semaphore_timedwait(*s, t) == KERN_SUCCESS;
}
static void sem_destroy_(rt_sem* s) { semaphore_destroy(mach_task_self(), *s); }
#else
typedef sem_t rt_sem;
typedef pthread_t rt_thread;
static int sem_make(rt_sem* s) { return sem_init(s, 0, 0) == 0; }
static void sem_signal(rt_sem* s) { sem_post(s); }
static void sem_block(rt_sem* s) {
    while (sem_wait(s) != 0 && errno == EINTR) {}
}
static int sem_sleep(rt_sem* s, int64_t ns) {
    struct timespec t;
    clock_gettime(CLOCK_REALTIME, &t);
    t.tv_sec += ns / 1000000000;
    t.tv_nsec += ns % 1000000000;
    if (t.tv_nsec >= 1000000000) {
        t.tv_sec++;
        t.tv_nsec -= 1000000000;
    }
    return sem_timedwait(s, &t) == 0;
}
static void sem_destroy_(rt_sem* s) { sem_destroy(s); }
#endif

// Workers. Every guard adds one worker to a pool shared by the binary's
// guards and removes one when freed, so a guard without a job in flight
// always finds a worker that is not running one. A job goes to the worker
// the calling thread used last, else to any worker that is spinning, else
// to a sleeping one. After a job a worker spins for RTGUARD_WORKER_SPIN_NS
// before it sleeps: a host calls its plugins one after another on the same
// thread, and a spinning worker takes the next job without the cost of a
// wake-up (several microseconds).
//
// Where waking a sleeping thread is slow (a virtual machine parks idle
// CPUs: wake-ups of a millisecond or more), sleeping between a host's
// blocks would make them late. A worker whose wake-up took more than a
// quarter of its job's deadline holds on: for the next RTGUARD_HOLD_JOBS
// jobs it spins through the gaps between blocks, up to RTGUARD_HOLD_MAX_NS
// (the longest recent gap, plus a quarter). A new worker spins up to
// RTGUARD_START_SPIN_NS for its first job, which follows activation.
#define RTGUARD_MAX_WORKERS 1024
#define RTGUARD_WORKER_SPIN_NS 20000
#define RTGUARD_HOLD_JOBS 4096
#define RTGUARD_HOLD_MAX_NS 6000000
#define RTGUARD_START_SPIN_NS 50000000

enum {
    W_NONE,  // free slot
    W_START, // starting, or being stopped
    W_SPIN,  // waiting for a job, spinning
    W_SLEEP, // waiting for a job on wake
    W_CLAIM, // being given a job
    W_BUSY,  // running job
    W_EXIT,  // told to stop
};

typedef struct {
    int state;
    VST3GoRTGuard* job;
    int64_t claimed; // when the job was handed over
    int64_t timeout; // its deadline
    // The worker's own: holding on (see above).
    int hold;       // jobs left to hold on for
    int64_t maxGap; // longest recent gap between jobs
    rt_sem wake;
    int haveWake;
    rt_thread thread;
    int64_t period_ns;
    void (*enter)(void);
} Worker;

static Worker workers[RTGUARD_MAX_WORKERS];
// highWater bounds the slots ever used.
static int highWater;

struct VST3GoRTGuard {
    int state;
    int entered;  // the running job got into Go (rtguard_entered)
    int sleeping; // the caller waits on done
    int inflight; // workers that may still touch the guard
    rtguard_job job;
    void* arg;
    rt_sem done;
};

// lateJobs counts the abandoned jobs of this binary that still run. Its
// instances share one Go runtime, so a stall that makes one late (garbage
// collection, scheduling) usually holds every worker: a host calling many
// instances in a row would wait out each one's deadline. While a job is
// late, a block whose job does not get into Go within RTGUARD_ENTRY_NS is
// abandoned at once; one that does gets its whole deadline, so a single
// slow instance does not cut the others short.
static int lateJobs;

// The pool lock serializes adding and removing workers (never on the audio
// path); lastWorker is a thread-local hint for the audio path.
#if defined(_WIN32)
static SRWLOCK poolLock = SRWLOCK_INIT;
static void pool_lock(void) { AcquireSRWLockExclusive(&poolLock); }
static void pool_unlock(void) { ReleaseSRWLockExclusive(&poolLock); }
static DWORD hintKey = TLS_OUT_OF_INDEXES;
static int hint_init(void) {
    if (hintKey == TLS_OUT_OF_INDEXES) hintKey = TlsAlloc();
    return hintKey != TLS_OUT_OF_INDEXES;
}
static Worker* hint_get(void) { return (Worker*)TlsGetValue(hintKey); }
static void hint_set(Worker* w) { TlsSetValue(hintKey, w); }
#else
static pthread_mutex_t poolLock = PTHREAD_MUTEX_INITIALIZER;
static void pool_lock(void) { pthread_mutex_lock(&poolLock); }
static void pool_unlock(void) { pthread_mutex_unlock(&poolLock); }
static pthread_key_t hintKey;
static int hintReady;
static int hint_init(void) {
    if (!hintReady) hintReady = pthread_key_create(&hintKey, NULL) == 0;
    return hintReady;
}
static Worker* hint_get(void) { return (Worker*)pthread_getspecific(hintKey); }
static void hint_set(Worker* w) { pthread_setspecific(hintKey, w); }
#endif

static inline void cpu_relax(void) {
#if defined(_WIN32)
    YieldProcessor();
#elif defined(__x86_64__) || defined(__i386__)
    __builtin_ia32_pause();
#elif defined(__aarch64__)
    __asm__ __volatile__("yield");
#endif
}

#if !defined(_WIN32)
// sleepSlack is how late a timed wait may wake: measured by the first
// rtguard_new, then the latest wake-up seen, shrinking by 1/64 with every
// wait that wakes in time.
static int64_t sleepSlack = RTGUARD_SLEEP_NS;
static int slackMeasured;

// sleep_on waits up to ns on s and returns how late it woke, or -1 when it
// got the semaphore.
static int64_t sleep_on(rt_sem* s, int64_t ns) {
    int64_t start = rtguard_now_ns();
    if (sem_sleep(s, ns)) return -1;
    int64_t late = rtguard_now_ns() - start - ns;
    return late > 0 ? late : 0;
}

// measure_slack sets sleepSlack from three waits on s, which nothing
// signals, half again as late as the latest.
static void measure_slack(rt_sem* s) {
    int64_t late = 0;
    for (int i = 0; i < 3; i++) {
        int64_t l = sleep_on(s, RTGUARD_SLEEP_NS);
        if (l > late) late = l;
    }
    late += late / 2;
    __atomic_store_n(&sleepSlack, late < RTGUARD_SLACK_MAX_NS ? late : RTGUARD_SLACK_MAX_NS, __ATOMIC_RELAXED);
}

// sleep_until waits on the done semaphore until about deadline: until
// sleepSlack before it, learning from how late it woke. The worker signals
// done only while sleeping is set, so the caller sets it and checks the
// state once more before it sleeps.
static void sleep_until(VST3GoRTGuard* g, int64_t now, int64_t deadline) {
    int64_t slack = __atomic_load_n(&sleepSlack, __ATOMIC_RELAXED);
    int64_t ns = deadline - now - slack;
    if (ns <= 0) return;
    __atomic_store_n(&g->sleeping, 1, __ATOMIC_SEQ_CST);
    int64_t late = -1;
    if (__atomic_load_n(&g->state, __ATOMIC_SEQ_CST) == STATE_QUEUED) {
        late = sleep_on(&g->done, ns < RTGUARD_SLEEP_NS ? ns : RTGUARD_SLEEP_NS);
    }
    __atomic_store_n(&g->sleeping, 0, __ATOMIC_RELAXED);
    if (late < 0) return;
    if (late > slack) {
        slack = late < RTGUARD_SLACK_MAX_NS ? late : RTGUARD_SLACK_MAX_NS;
    } else {
        slack -= slack / 64;
    }
    __atomic_store_n(&sleepSlack, slack, __ATOMIC_RELAXED);
}
#endif

int64_t rtguard_now_ns(void) {
#if defined(_WIN32)
    static LARGE_INTEGER freq;
    LARGE_INTEGER now;
    if (freq.QuadPart == 0) QueryPerformanceFrequency(&freq);
    QueryPerformanceCounter(&now);
    return (int64_t)((double)now.QuadPart * 1e9 / (double)freq.QuadPart);
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
#endif
}

rtguard_fp rtguard_flush_denormals(void) {
#if defined(__x86_64__) || defined(__i386__)
    unsigned int csr = __builtin_ia32_stmxcsr();
    if ((csr & 0x8040) != 0x8040) __builtin_ia32_ldmxcsr(csr | 0x8040); // FTZ | DAZ
    return csr;
#else
    return 0;
#endif
}

void rtguard_restore_fp(rtguard_fp saved) {
#if defined(__x86_64__) || defined(__i386__)
    if (((unsigned int)saved & 0x8040) != 0x8040) __builtin_ia32_ldmxcsr((unsigned int)saved);
#else
    (void)saved;
#endif
}

// make_realtime gives the calling thread the scheduling of an audio
// device thread: a time-constraint policy on macOS, SCHED_FIFO on Linux
// (when the process may use it) and time-critical priority on Windows.
static void make_realtime(int64_t period_ns) {
#if defined(_WIN32)
    (void)period_ns;
    SetThreadPriority(GetCurrentThread(), THREAD_PRIORITY_TIME_CRITICAL);
#elif defined(__APPLE__)
    mach_timebase_info_data_t tb;
    mach_timebase_info(&tb);
    double ticks_per_ns = (double)tb.denom / tb.numer;
    thread_time_constraint_policy_data_t policy;
    policy.period = (uint32_t)(period_ns * ticks_per_ns);
    policy.computation = (uint32_t)(period_ns / 2 * ticks_per_ns);
    policy.constraint = (uint32_t)(period_ns * ticks_per_ns);
    policy.preemptible = 1;
    thread_policy_set(pthread_mach_thread_np(pthread_self()), THREAD_TIME_CONSTRAINT_POLICY,
        (thread_policy_t)&policy, THREAD_TIME_CONSTRAINT_POLICY_COUNT);
#else
    (void)period_ns;
    struct sched_param sp = { .sched_priority = sched_get_priority_max(SCHED_FIFO) - 10 };
    pthread_setschedparam(pthread_self(), SCHED_FIFO, &sp);
#endif
}

// finish ends g's job: done, or freed when it was abandoned. The worker
// does not touch g after it.
static void finish(VST3GoRTGuard* g) {
    int expected = STATE_QUEUED;
    if (__atomic_compare_exchange_n(&g->state, &expected, STATE_DONE, 0, __ATOMIC_SEQ_CST, __ATOMIC_ACQUIRE)) {
        if (__atomic_load_n(&g->sleeping, __ATOMIC_SEQ_CST)) sem_signal(&g->done);
    } else {
        // Abandoned: nobody waits for it.
        __atomic_sub_fetch(&lateJobs, 1, __ATOMIC_ACQ_REL);
        __atomic_store_n(&g->state, STATE_FREE, __ATOMIC_RELEASE);
    }
    __atomic_sub_fetch(&g->inflight, 1, __ATOMIC_RELEASE);
}

static void worker(Worker* w) {
    make_realtime(w->period_ns);
    rtguard_flush_denormals();
    w->enter();
    __atomic_store_n(&w->state, W_SPIN, __ATOMIC_RELEASE);
    int64_t spinFor = RTGUARD_START_SPIN_NS;
    for (;;) {
        int64_t idle = rtguard_now_ns();
        int s, slept = 0;
        for (;;) {
            s = __atomic_load_n(&w->state, __ATOMIC_ACQUIRE);
            if (s == W_BUSY || s == W_EXIT) break;
            if (s == W_SPIN && rtguard_now_ns() - idle > spinFor) {
                int spin = W_SPIN;
                if (__atomic_compare_exchange_n(&w->state, &spin, W_SLEEP, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) {
                    // Whoever takes a sleeping worker signals wake.
                    sem_block(&w->wake);
                    slept = 1;
                }
                continue;
            }
            cpu_relax();
        }
        if (s == W_EXIT) return;
        int64_t start = rtguard_now_ns();
        if (slept && start - w->claimed > w->timeout / 4) {
            w->hold = RTGUARD_HOLD_JOBS;
        }
        if (spinFor != RTGUARD_START_SPIN_NS) {
            // Not the wait for the first job, which follows activation.
            int64_t gap = start - idle;
            w->maxGap = gap > w->maxGap ? gap : w->maxGap - w->maxGap / 64;
        }
        spinFor = RTGUARD_WORKER_SPIN_NS;
        if (w->hold > 0) {
            w->hold--;
            int64_t hold = w->maxGap + w->maxGap / 4;
            if (hold > RTGUARD_HOLD_MAX_NS) hold = RTGUARD_HOLD_MAX_NS;
            if (hold > spinFor) spinFor = hold;
        }
        VST3GoRTGuard* g = w->job;
        g->job(g->arg);
        // Available before g's state changes: a guard without a job in
        // flight always finds a worker.
        __atomic_store_n(&w->state, W_SPIN, __ATOMIC_RELEASE);
        finish(g);
    }
}

#if defined(_WIN32)
static DWORD WINAPI worker_main(LPVOID arg) {
    worker((Worker*)arg);
    return 0;
}
#else
static void* worker_main(void* arg) {
    worker((Worker*)arg);
    return NULL;
}
#endif

static void nap(void) {
#if defined(_WIN32)
    Sleep(1);
#else
    struct timespec ts = { 0, 100000 };
    nanosleep(&ts, NULL);
#endif
}

// add_worker starts a worker and waits until it entered Go. Pool lock held.
static int add_worker(void (*enter)(void), int64_t period_ns) {
    int i = 0;
    while (i < RTGUARD_MAX_WORKERS && __atomic_load_n(&workers[i].state, __ATOMIC_ACQUIRE) != W_NONE) i++;
    if (i == RTGUARD_MAX_WORKERS) return 0;
    Worker* w = &workers[i];
    if (!w->haveWake) {
        if (!sem_make(&w->wake)) return 0;
        w->haveWake = 1;
    }
    w->enter = enter;
    w->period_ns = period_ns;
    __atomic_store_n(&w->state, W_START, __ATOMIC_RELEASE);
#if defined(_WIN32)
    w->thread = CreateThread(NULL, 0, worker_main, w, 0, NULL);
    int started = w->thread != NULL;
#else
    int started = pthread_create(&w->thread, NULL, worker_main, w) == 0;
#endif
    if (!started) {
        __atomic_store_n(&w->state, W_NONE, __ATOMIC_RELEASE);
        return 0;
    }
    while (__atomic_load_n(&w->state, __ATOMIC_ACQUIRE) == W_START) nap();
    if (i + 1 > __atomic_load_n(&highWater, __ATOMIC_RELAXED)) __atomic_store_n(&highWater, i + 1, __ATOMIC_RELEASE);
    return 1;
}

// take moves an idle worker (state from) to W_CLAIM.
static int take(Worker* w, int from) {
    return __atomic_load_n(&w->state, __ATOMIC_RELAXED) == from &&
        __atomic_compare_exchange_n(&w->state, &from, W_CLAIM, 0, __ATOMIC_ACQ_REL, __ATOMIC_RELAXED);
}

// remove_worker stops an idle worker. Pool lock held.
static void remove_worker(void) {
    for (;;) {
        int n = __atomic_load_n(&highWater, __ATOMIC_ACQUIRE);
        for (int i = 0; i < n; i++) {
            Worker* w = &workers[i];
            int from = W_SPIN;
            if (!take(w, from) && !take(w, from = W_SLEEP)) continue;
            __atomic_store_n(&w->state, W_EXIT, __ATOMIC_RELEASE);
            if (from == W_SLEEP) sem_signal(&w->wake);
#if defined(_WIN32)
            WaitForSingleObject(w->thread, INFINITE);
            CloseHandle(w->thread);
#else
            pthread_join(w->thread, NULL);
#endif
            __atomic_store_n(&w->state, W_NONE, __ATOMIC_RELEASE);
            return;
        }
        // Every worker is busy for a moment.
        nap();
    }
}

// claim gives g's job, due within timeout_ns, to a worker: the hint, else
// a spinning one, else a sleeping one. One is free (see Workers), so it
// only retries while other threads take the ones it tried.
static void claim(VST3GoRTGuard* g, int64_t timeout_ns) {
    Worker* hint = hint_get();
    Worker* w = NULL;
    int from = W_SPIN;
    if (hint && take(hint, W_SPIN)) {
        w = hint;
    }
    while (!w) {
        int n = __atomic_load_n(&highWater, __ATOMIC_ACQUIRE);
        for (int i = 0; i < n && !w; i++) {
            if (take(&workers[i], W_SPIN)) w = &workers[i];
        }
        for (int i = 0; i < n && !w; i++) {
            if (take(&workers[i], W_SLEEP)) {
                w = &workers[i];
                from = W_SLEEP;
            }
        }
        if (!w) cpu_relax();
    }
    w->job = g;
    w->claimed = rtguard_now_ns();
    w->timeout = timeout_ns;
    __atomic_store_n(&w->state, W_BUSY, __ATOMIC_RELEASE);
    if (from == W_SLEEP) sem_signal(&w->wake);
    if (w != hint) hint_set(w);
}

VST3GoRTGuard* rtguard_new(rtguard_job job, void* arg, void (*enter)(void), int64_t period_ns) {
    VST3GoRTGuard* g = (VST3GoRTGuard*)calloc(1, sizeof(VST3GoRTGuard));
    if (!g) return NULL;
    g->job = job;
    g->arg = arg;
    if (!sem_make(&g->done)) {
        free(g);
        return NULL;
    }
#if !defined(_WIN32)
    int unmeasured = 0;
    if (__atomic_compare_exchange_n(&slackMeasured, &unmeasured, 1, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) {
        measure_slack(&g->done);
    }
#endif
    pool_lock();
    int ok = hint_init() && add_worker(enter, period_ns);
    pool_unlock();
    if (!ok) {
        sem_destroy_(&g->done);
        free(g);
        return NULL;
    }
    return g;
}

void rtguard_entered(VST3GoRTGuard* g) {
    __atomic_store_n(&g->entered, 1, __ATOMIC_RELEASE);
}

int rtguard_run(VST3GoRTGuard* g, int64_t timeout_ns) {
    __atomic_store_n(&g->entered, 0, __ATOMIC_RELAXED);
    __atomic_add_fetch(&g->inflight, 1, __ATOMIC_RELAXED);
    __atomic_store_n(&g->state, STATE_QUEUED, __ATOMIC_RELEASE);
    claim(g, timeout_ns);

    int64_t start = rtguard_now_ns();
    int64_t full = start + timeout_ns;
    int64_t entry = __atomic_load_n(&lateJobs, __ATOMIC_ACQUIRE) > 0 ? start + RTGUARD_ENTRY_NS : full;
    for (;;) {
        if (__atomic_load_n(&g->state, __ATOMIC_ACQUIRE) == STATE_DONE) {
            __atomic_store_n(&g->state, STATE_FREE, __ATOMIC_RELEASE);
            return RTGUARD_DONE;
        }
        int64_t now = rtguard_now_ns();
        int64_t deadline = entry < full && !__atomic_load_n(&g->entered, __ATOMIC_ACQUIRE) ? entry : full;
        if (now >= deadline) {
            break;
        }
        if (now - start < RTGUARD_SPIN_NS || deadline - now < 2 * RTGUARD_SPIN_NS) {
            cpu_relax();
            continue;
        }
#if defined(_WIN32)
        // Windows timed waits round up to the timer tick (about 1 ms or
        // more); yield instead.
        SwitchToThread();
#else
        // The deadline less a spin: the state is checked before it passes.
        sleep_until(g, now, deadline - RTGUARD_SPIN_NS);
#endif
    }
    int expected = STATE_QUEUED;
    if (__atomic_compare_exchange_n(&g->state, &expected, STATE_ABANDONED, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) {
        __atomic_add_fetch(&lateJobs, 1, __ATOMIC_ACQ_REL);
        return RTGUARD_LATE;
    }
    // It finished as the deadline passed.
    __atomic_store_n(&g->state, STATE_FREE, __ATOMIC_RELEASE);
    return RTGUARD_DONE;
}

int rtguard_idle(VST3GoRTGuard* g) {
    return __atomic_load_n(&g->state, __ATOMIC_ACQUIRE) == STATE_FREE;
}

void rtguard_quiesce(VST3GoRTGuard* g) {
    while (!rtguard_idle(g)) nap();
}

void rtguard_free(VST3GoRTGuard* g) {
    if (!g) return;
    rtguard_quiesce(g);
    while (__atomic_load_n(&g->inflight, __ATOMIC_ACQUIRE) > 0) nap();
    pool_lock();
    remove_worker();
    pool_unlock();
    sem_destroy_(&g->done);
    free(g);
}
