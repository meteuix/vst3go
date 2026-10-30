package plugin

import (
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"sync"
	"sync/atomic"
)

// DefaultGCHeadroom is how much garbage a plugin binary accumulates before
// the garbage collector runs (see SetGCHeadroom).
const DefaultGCHeadroom = 64 << 20

// gcHeadroomEnv overrides SetGCHeadroom for every plugin in the process (in
// bytes; 0 for Go's own pacing).
const gcHeadroomEnv = "VST3GO_GC_HEADROOM"

// gcActivateGarbage is the garbage that makes activation collect before
// the first block.
const gcActivateGarbage = 1 << 20

var (
	gcHeadroom  atomic.Int64
	gcStart     sync.Once
	gcOn        atomic.Bool
	gcAllocsAt  atomic.Uint64 // heap allocations at the end of the last cycle
	gcMetricsMu sync.Mutex
	gcMetrics   = []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/gc/heap/allocs:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
)

func init() { gcHeadroom.Store(DefaultGCHeadroom) }

// SetGCHeadroom sets how much garbage the binary may accumulate before the
// garbage collector runs; 0 leaves Go's own pacing (GOGC). Call it from
// init(), before the host creates an instance.
//
// The collector stops every goroutine for a moment, the audio thread's
// included, and while it marks it takes the processors an audio block
// needs. With Go's pacing it runs whenever the heap doubles from a 4 MB
// minimum and at least every two minutes, so the small allocations of an
// editor or of parameter text keep it running every few minutes. The
// headroom replaces that: the collector runs only when the binary allocated
// the headroom (or, for a heap larger than it, the live heap) since the
// last cycle, and activation collects what the instance allocated while it
// was created and loaded before the first block. The GOGC, GOMEMLIMIT and
// VST3GO_GC_HEADROOM environment variables take precedence.
func SetGCHeadroom(bytes int64) { gcHeadroom.Store(max(bytes, 0)) }

// startGCPolicy applies the headroom when the first instance is created.
func startGCPolicy() {
	gcStart.Do(func() {
		headroom := gcHeadroom.Load()
		if env := os.Getenv(gcHeadroomEnv); env != "" {
			headroom, _ = strconv.ParseInt(env, 10, 64)
		}
		if headroom <= 0 || os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" {
			return
		}
		gcHeadroom.Store(headroom)
		gcOn.Store(true)
		// The limit first: without one, turning the pacing off would let
		// the heap grow without bound.
		setGCLimit()
		debug.SetGCPercent(-1)
		watchGC()
	})
}

// setGCLimit lets the runtime's memory grow by the headroom, or by the live
// heap when that is larger, before the next cycle.
func setGCLimit() {
	gcMetricsMu.Lock()
	metrics.Read(gcMetrics)
	live := gcMetrics[0].Value.Uint64()
	allocs := gcMetrics[1].Value.Uint64()
	used := gcMetrics[2].Value.Uint64() - gcMetrics[3].Value.Uint64()
	gcMetricsMu.Unlock()
	grow := max(uint64(gcHeadroom.Load()), live)
	limit := used + grow
	if limit > math.MaxInt64 || limit < used {
		limit = math.MaxInt64
	}
	gcAllocsAt.Store(allocs)
	debug.SetMemoryLimit(int64(limit))
}

// gcSentinel is collected by every cycle, which then moves the limit.
type gcSentinel struct{ _ [4]uintptr }

func watchGC() {
	runtime.AddCleanup(new(gcSentinel), func(struct{}) {
		setGCLimit()
		watchGC()
	}, struct{}{})
}

// collectBeforeProcessing runs a cycle when activation finds more than
// gcActivateGarbage allocated since the last one: what creating, loading
// and sizing the instance left behind, which otherwise starts the next
// cycle during processing.
func collectBeforeProcessing() {
	if !gcOn.Load() {
		return
	}
	gcMetricsMu.Lock()
	metrics.Read(gcMetrics[1:2])
	allocs := gcMetrics[1].Value.Uint64()
	gcMetricsMu.Unlock()
	if allocs-gcAllocsAt.Load() > gcActivateGarbage {
		runtime.GC()
	}
}
