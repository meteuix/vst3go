//go:build darwin || linux

// Command rtcheck measures how long the plugin runtime takes per audio
// block when called the way a host calls it: from a native real-time
// thread that Go did not create, while other goroutines allocate and keep
// the garbage collector busy. It prints the distribution of call times
// against the block's real-time budget and exits 1 when a call misses it.
//
//	go run ./internal/validation/rtcheck -seconds 30 -block 128 -garbage 200
package main

/*
#include <stdlib.h>
#include "rt.h"
*/
import "C"

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"slices"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/th-release/vst3go/pkg/dsp/convolution"
	"github.com/th-release/vst3go/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/pkg/dsp/filter"
	"github.com/th-release/vst3go/pkg/dsp/resample"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
)

const (
	paramDrive uint32 = iota
	paramMix
)

// chainPlugin is a typical insert effect: compressor, low-pass, 4x
// oversampled saturation and a half-second convolution reverb.
type chainPlugin struct{ block int }

func (chainPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.vst3go.validation.rtcheck", Name: "RT Check", Version: "1.0.0", Vendor: "VST3Go"}
}

func (cp chainPlugin) CreateProcessor() plugin.Processor {
	p := &chainProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo()), block: cp.block}
	p.Parameters().Add(
		param.New(paramDrive, "Drive").Range(0, 24).Default(12).Unit("dB").Smooth(0.02).Build(),
		param.MixParameter(paramMix, "Mix").Default(30).Smooth(0.02).Build(),
	)
	p.OnInitialize(p.prepare)
	return p
}

type chainProcessor struct {
	*frameworkplugin.BaseProcessor
	block int
	comp  *dynamics.Compressor
	tone  *filter.Biquad
	over  [2]*resample.Oversampler
	rev   [2]*convolution.Convolver
	wet   []float32
	mix   []float32
}

func (p *chainProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.comp = dynamics.NewCompressor(sampleRate)
	p.comp.SetThreshold(-18)
	p.comp.SetRatio(4)
	p.tone = filter.NewBiquad(2)
	p.tone.SetLowpass(sampleRate, 12000, 0.707)
	ir := make([]float32, int(sampleRate/2))
	seed := uint32(1)
	for i := range ir {
		seed = seed*1664525 + 1013904223
		ir[i] = (float32(seed>>8)/(1<<24) - 0.5) * float32(math.Exp(-6*float64(i)/float64(len(ir))))
	}
	for ch := range 2 {
		o, err := resample.NewOversampler(4, int(maxBlockSize))
		if err != nil {
			return err
		}
		c, err := convolution.New(ir, p.block)
		if err != nil {
			return err
		}
		p.over[ch], p.rev[ch] = o, c
	}
	p.wet = make([]float32, maxBlockSize)
	p.mix = make([]float32, maxBlockSize)
	return nil
}

func (p *chainProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	l, r := ctx.Output[0][:n], ctx.Output[1][:n]
	p.comp.ProcessStereo(ctx.Input[0][:n], ctx.Input[1][:n], l, r)
	drive := float32(math.Pow(10, ctx.ParamPlain(paramDrive)/20))
	ramp := ctx.Smoothed(paramMix)
	mix := p.mix[:n]
	for i := range mix {
		mix[i] = float32(ramp.Next() / 100)
	}
	for ch, out := range [][]float32{l, r} {
		p.tone.Process(out, ch)
		high := p.over[ch].Up(out)
		for i, x := range high {
			high[i] = float32(math.Tanh(float64(x * drive)))
		}
		p.over[ch].Down(high, out)
		p.rev[ch].Process(out, p.wet[:n])
		for i, m := range mix {
			out[i] = out[i]*(1-m) + p.wet[i]*m
		}
	}
}

var (
	instance  *plugin.Offline
	in, out   [][]float32
	processed atomic.Int64
)

//export GoRTProcess
func GoRTProcess() {
	if err := instance.Process(in, out); err != nil {
		panic(err)
	}
	processed.Add(1)
}

// churn allocates bytesPerSecond, keeping about live bytes reachable, so
// the collector runs continuously.
func churn(bytesPerSecond, live int, stop <-chan struct{}) {
	const chunk = 64 << 10
	keep := make([][]byte, max(1, live/chunk))
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	perTick := bytesPerSecond / 1000
	for i := 0; ; {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		for b := 0; b < perTick; b += chunk {
			keep[i%len(keep)] = make([]byte, chunk)
			i++
		}
	}
}

func main() {
	seconds := flag.Float64("seconds", 20, "how long to run")
	rate := flag.Float64("rate", 48000, "sample rate")
	block := flag.Int("block", 128, "block size in samples (a power of two)")
	garbage := flag.Int("garbage", 200, "MB per second other goroutines allocate (0 for none)")
	live := flag.Int("live", 256, "MB the allocating goroutines keep reachable")
	flag.Parse()

	var err error
	instance, err = plugin.NewOffline(chainPlugin{block: *block}, plugin.OfflineSetup{SampleRate: *rate, MaxBlockSize: *block})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer instance.Close()
	in = [][]float32{make([]float32, *block), make([]float32, *block)}
	out = [][]float32{make([]float32, *block), make([]float32, *block)}
	for i := range in[0] {
		in[0][i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/(*rate)))
		in[1][i] = in[0][i]
	}

	stop := make(chan struct{})
	if *garbage > 0 {
		for range max(1, runtime.GOMAXPROCS(0)/2) {
			go churn(*garbage<<20/max(1, runtime.GOMAXPROCS(0)/2), *live<<20/max(1, runtime.GOMAXPROCS(0)/2), stop)
		}
	}

	period := time.Duration(float64(*block) / *rate * float64(time.Second))
	n := int(*seconds / period.Seconds())
	durations := (*[1 << 30]C.int64_t)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.int64_t(0)))))[:n:n]
	defer C.free(unsafe.Pointer(&durations[0]))

	gcBefore := readGC()
	realtime := C.rt_run(C.int(n), C.int64_t(period), &durations[0])
	close(stop)
	gcAfter := readGC()
	if realtime < 0 {
		fmt.Fprintln(os.Stderr, "rtcheck: cannot start the audio thread")
		os.Exit(2)
	}

	times := make([]time.Duration, n)
	for i, d := range durations {
		times[i] = time.Duration(d)
	}
	slices.Sort(times)
	pct := func(p float64) time.Duration { return times[min(n-1, int(p*float64(n)))] }
	over := n - slices.IndexFunc(times, func(d time.Duration) bool { return d > period })
	if over > n {
		over = 0
	}
	fmt.Printf("blocks       %d x %d samples at %.0f Hz (budget %v), real-time thread: %v\n", n, *block, *rate, period, realtime == 1)
	pause := "none"
	if d := longestPause(gcBefore, gcAfter); d > 0 {
		pause = "under " + d.String()
	}
	fmt.Printf("allocation   %d MB/s in other goroutines, %d GC cycles, longest stop-the-world pause %s\n",
		*garbage, gcAfter.cycles-gcBefore.cycles, pause)
	fmt.Printf("call time    p50 %v  p99 %v  p99.9 %v  max %v\n", pct(0.5), pct(0.99), pct(0.999), times[n-1])
	fmt.Printf("over budget  %d blocks; over half the budget %d blocks\n", over, n-countBelow(times, period/2))
	if over > 0 {
		os.Exit(1)
	}
}

func countBelow(sorted []time.Duration, limit time.Duration) int {
	i, _ := slices.BinarySearch(sorted, limit+1)
	return i
}

type gcStats struct {
	cycles uint64
	pauses *metrics.Float64Histogram
}

// readGC reads the GC cycle count and the stop-the-world pause histogram.
func readGC() gcStats {
	samples := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}, {Name: "/sched/pauses/total/gc:seconds"}}
	metrics.Read(samples)
	return gcStats{cycles: samples[0].Value.Uint64(), pauses: samples[1].Value.Float64Histogram()}
}

// longestPause returns the upper bound of the highest histogram bucket
// that gained pauses between before and after.
func longestPause(before, after gcStats) time.Duration {
	h := after.pauses
	for i := len(h.Counts) - 1; i >= 0; i-- {
		if h.Counts[i] > before.pauses.Counts[i] {
			return time.Duration(h.Buckets[i+1] * float64(time.Second))
		}
	}
	return 0
}
