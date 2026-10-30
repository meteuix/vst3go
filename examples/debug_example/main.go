// Command debug_example shows how to debug a plugin without breaking the
// audio thread's rules. ProcessAudio only measures: it times each block
// (debug.AudioProcessProfiler), analyzes the output (debug.AudioAnalyzer),
// counts clipped and non-finite samples into atomics and publishes CPU
// load and peak meters. A reporter goroutine, running while the plugin is
// active, turns those numbers into log lines (debug.Logger) at the chosen
// Debug Level, in vst3go_debug/debug_example.log under the temp directory.
//
// Build: scripts/build_vst3.sh examples/debug_example DebugExample
package main

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/debug"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const (
	paramBypass uint32 = iota
	paramGain
	paramDebugLevel
	paramCPULoad
	paramPeak
)

// debugPlugin's zero value logs to the temp directory once a second.
type debugPlugin struct {
	logTo       io.Writer     // log destination; nil for the log file
	reportEvery time.Duration // reporter period; 0 for one second
}

func (debugPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.debugexample",
		Name:     "DebugExample",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxTools,
	}
}

func (d debugPlugin) CreateProcessor() plugin.Processor {
	p := &debugProcessor{
		BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo()),
		analyzer:      debug.NewAudioAnalyzer(),
		logTo:         d.logTo,
		reportEvery:   d.reportEvery,
	}
	if p.reportEvery <= 0 {
		p.reportEvery = time.Second
	}
	p.Parameters().Add(
		param.BypassParameter(paramBypass, "Bypass").Build(),
		param.New(paramGain, "Gain").Range(-24, 12).Default(-3).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.Choice(paramDebugLevel, "Debug Level", []param.ChoiceOption{
			{Value: float64(debug.LogLevelDebug), Name: "Debug"},
			{Value: float64(debug.LogLevelInfo), Name: "Info"},
			{Value: float64(debug.LogLevelWarn), Name: "Warn"},
			{Value: float64(debug.LogLevelError), Name: "Error"},
			{Value: float64(debug.LogLevelFatal), Name: "Fatal"},
			{Value: float64(debug.LogLevelOff), Name: "Off"},
		}).Default(float64(debug.LogLevelInfo)).Build(),
		param.New(paramCPULoad, "CPU Load").Range(0, 100).Unit("%").ReadOnly().Build(),
		param.OutputLevelMeter(paramPeak, "Output Peak").Build(),
	)
	p.OnInitialize(p.prepare)
	return p
}

// blockStats are written by the audio thread and read by the reporter.
type blockStats struct {
	clipped   atomic.Uint64 // output samples at or above full scale
	nonFinite atomic.Uint64 // NaN or Inf output samples (replaced by 0)
	peakBits  atomic.Uint64 // highest output peak since the last report
}

type debugProcessor struct {
	*frameworkplugin.BaseProcessor

	logTo       io.Writer
	reportEvery time.Duration
	logger      *debug.Logger
	profiler    *debug.AudioProcessProfiler
	analyzer    *debug.AudioAnalyzer
	stats       blockStats

	// Reporter goroutine, running while active.
	mu       sync.Mutex
	stop     chan struct{}
	finished chan struct{}
}

func (p *debugProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	if p.logger == nil {
		p.logger = p.newLogger()
	}
	p.profiler = debug.NewAudioProcessProfiler(sampleRate)
	p.logger.Info("initialized: %.0f Hz, blocks up to %d samples", sampleRate, maxBlockSize)
	return nil
}

func (p *debugProcessor) newLogger() *debug.Logger {
	if p.logTo != nil {
		return debug.New(p.logTo, "DebugExample", debug.FlagLevel|debug.FlagPrefix)
	}
	path := filepath.Join(os.TempDir(), "vst3go_debug", "debug_example.log")
	logger, err := debug.NewFileLogger(path, "DebugExample", debug.DefaultFlags)
	if err != nil {
		logger = debug.New(os.Stderr, "DebugExample", debug.DefaultFlags)
		logger.Warn("cannot open %s: %v", path, err)
	}
	return logger
}

// SetActive starts the reporter when processing starts and stops it, with
// a final report, when processing stops.
func (p *debugProcessor) SetActive(active bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if active && p.stop == nil {
		p.profiler.Reset()
		p.stop = make(chan struct{})
		p.finished = make(chan struct{})
		go p.report(p.stop, p.finished)
	} else if !active && p.stop != nil {
		close(p.stop)
		<-p.finished
		p.stop, p.finished = nil, nil
		p.logger.Info("deactivated\n%s", p.profiler.AudioReport())
	}
	return p.BaseProcessor.SetActive(active)
}

// report logs the audio thread's statistics every reportEvery.
func (p *debugProcessor) report(stop <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	ticker := time.NewTicker(p.reportEvery)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			p.logStats()
			return
		case <-ticker.C:
			p.logStats()
		}
	}
}

func (p *debugProcessor) logStats() {
	p.logger.SetLevel(debug.LogLevel(p.Parameters().Get(paramDebugLevel).GetPlainValue()))
	if n := p.stats.nonFinite.Swap(0); n > 0 {
		p.logger.Error("%d NaN/Inf output samples replaced by silence", n)
	}
	if n := p.stats.clipped.Swap(0); n > 0 {
		p.logger.Warn("%d output samples clipped", n)
	}
	peak := math.Float64frombits(p.stats.peakBits.Swap(0))
	if load := p.profiler.CPULoad(); load > 80 {
		p.logger.Warn("high CPU load: %.2f%%", load)
	}
	p.logger.Debug("blocks %d, CPU load %.2f%% (last %.2f%%, peak %.2f%%), output peak %.3f",
		p.profiler.Blocks(), p.profiler.CPULoad(), p.profiler.LastLoad(), p.profiler.MaxLoad(), peak)
}

func (p *debugProcessor) ProcessAudio(ctx *process.Context) {
	start := p.profiler.Begin()
	n := ctx.NumSamples()

	if ctx.Param(paramBypass) >= 0.5 {
		ctx.PassThrough()
	} else {
		gain := ctx.Smoothed(paramGain)
		gains := ctx.WorkBuffer()
		for i := range gains {
			gains[i] = float32(math.Pow(10, gain.Next()/20))
		}
		ctx.PassThrough()
		for _, out := range ctx.Output {
			for i := range out {
				out[i] *= gains[i]
			}
		}
	}

	peak := 0.0
	for _, out := range ctx.Output {
		for i, x := range out {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				out[i] = 0
				p.stats.nonFinite.Add(1)
			}
		}
		result := p.analyzer.Analyze(out[:n])
		peak = max(peak, float64(result.Peak))
		p.stats.clipped.Add(uint64(result.ClippedSamples))
	}
	for {
		old := p.stats.peakBits.Load()
		if peak <= math.Float64frombits(old) || p.stats.peakBits.CompareAndSwap(old, math.Float64bits(peak)) {
			break
		}
	}

	p.profiler.End(start, n)
	ctx.SetOutputParameter(paramCPULoad, min(p.profiler.LastLoad(), 100)/100)
	meter := p.Parameters().Get(paramPeak)
	db := -60.0
	if peak > 0 {
		db = max(-60, 20*math.Log10(peak))
	}
	ctx.SetOutputParameter(paramPeak, meter.Normalize(db))
}

func init() {
	plugin.Register(debugPlugin{})
}

func main() { standalone.Main() }
