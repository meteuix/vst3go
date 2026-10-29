#include <stdint.h>

// rt_run calls GoRTProcess n times from a new native thread (not created
// by Go, like a host's audio thread) once every period_ns, records each
// call's duration and returns 1 when the thread got real-time scheduling,
// 0 when not, or -1 when the thread could not be started.
int rt_run(int n, int64_t period_ns, int64_t *durations);
