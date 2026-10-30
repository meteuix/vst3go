// Command gain is a gain plugin with switchable parameter smoothing: with
// smoothing on, gain changes ramp over an adjustable time instead of
// jumping, which removes zipper noise on automation.
//
// Build: scripts/build_vst3.sh examples/gain SmoothGain
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/gain"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramGain uint32 = iota
	paramOutputLevel
	paramBypass
	paramSmoothingEnabled
	paramSmoothingTime
)

const (
	minGainDB          = -24.0
	maxGainDB          = 24.0
	minSmoothingMs     = 0.1
	maxSmoothingMs     = 100.0
	defaultSmoothingMs = 5.0
)

type gainPlugin struct{}

func (gainPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.smoothgain",
		Name:     "Smooth Gain",
		Version:  "2.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxTools,
	}
}

func (gainPlugin) CreateProcessor() plugin.Processor {
	p := &gainProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramGain, "Gain").
			Range(minGainDB, maxGainDB).
			Default(0).
			Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).
			Build(),
		param.OutputLevelMeter(paramOutputLevel, "Output Level").Build(),
		param.BypassParameter(paramBypass, "Bypass").Build(),
		param.New(paramSmoothingEnabled, "Smoothing").Toggle().Build(),
		param.New(paramSmoothingTime, "Smoothing Time").
			Range(minSmoothingMs, maxSmoothingMs).
			Default(defaultSmoothingMs).
			Logarithmic().
			Unit("ms").
			Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type gainProcessor struct {
	*frameworkplugin.BaseProcessor

	// ramp smooths the gain in dB; smoothingMs is the ramp time it was
	// built with.
	ramp        param.Ramp
	smoothingMs float64
}

func (p *gainProcessor) prepare(sampleRate float64, _ int32) error {
	p.smoothingMs = p.Parameters().Get(paramSmoothingTime).GetPlainValue()
	p.ramp = param.NewRamp(param.SmoothLinear, sampleRate, p.smoothingMs/1000,
		p.Parameters().Get(paramGain).GetPlainValue())
	return nil
}

func (p *gainProcessor) reset() {
	p.ramp.SetCurrentAndTarget(p.Parameters().Get(paramGain).GetPlainValue())
}

func (p *gainProcessor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(paramBypass) >= 0.5 {
		ctx.PassThrough()
		return
	}

	gainDB := ctx.ParamPlain(paramGain)
	if ctx.Param(paramSmoothingEnabled) < 0.5 {
		p.ramp.SetCurrentAndTarget(gainDB)
	} else {
		if ms := ctx.ParamPlain(paramSmoothingTime); ms != p.smoothingMs {
			p.smoothingMs = ms
			current := p.ramp.Current()
			p.ramp.Reset(p.SampleRate(), ms/1000)
			p.ramp.SetCurrentAndTarget(current)
		}
		p.ramp.SetTarget(gainDB)
	}

	n := ctx.NumSamples()
	peak := float32(0)
	if p.ramp.IsSmoothing() {
		// Per-sample gain from the ramp, shared by all channels.
		gains := ctx.WorkBuffer()[:n]
		for i := range gains {
			gains[i] = gain.DbToLinear32(float32(p.ramp.Next()))
		}
		for ch := range ctx.Output {
			out := ctx.Output[ch]
			if ch >= len(ctx.Input) {
				clear(out)
				continue
			}
			for i, x := range ctx.Input[ch] {
				out[i] = x * gains[i]
				peak = max(peak, float32(math.Abs(float64(out[i]))))
			}
		}
	} else {
		g := gain.DbToLinear32(float32(p.ramp.Current()))
		ctx.ProcessChannels(func(_ int, input, output []float32) {
			copy(output, input)
			gain.ApplyBuffer(output, g)
			for _, x := range output {
				peak = max(peak, float32(math.Abs(float64(x))))
			}
		})
	}

	meter := p.Parameters().Get(paramOutputLevel)
	ctx.SetOutputParameter(paramOutputLevel, meter.Normalize(max(-60, float64(gain.LinearToDb32(peak)))))
}

func init() {
	plugin.Register(gainPlugin{})
}

func main() { standalone.Main() }
