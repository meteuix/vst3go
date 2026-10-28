// Command ducker is an example sidechain effect: the main signal is ducked
// whenever the sidechain gets loud. A short lookahead delay shows latency
// reporting.
//
// Build: scripts/build_vst3.sh internal/validation/ducker Ducker
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
)

const (
	paramThreshold uint32 = iota
	paramDepth
	paramRelease
	paramReduction
	paramBypass
)

const (
	lookaheadMs = 2.0
	maxChannels = 2
)

type duckerPlugin struct{}

func (duckerPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.ducker",
		Name:     "VST3Go Ducker",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (duckerPlugin) CreateProcessor() plugin.Processor {
	p := &duckerProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereoSidechain())}
	p.Parameters().Add(
		param.ThresholdParameter(paramThreshold, "Threshold", -60, 0, -30).Build(),
		param.New(paramDepth, "Depth").Range(0, 48).Default(18).Unit("dB").Build(),
		param.ReleaseParameter(paramRelease, "Release", 1000).Build(),
		param.New(paramReduction, "Gain Reduction").Range(0, 48).Default(0).Unit("dB").ReadOnly().Build(),
		param.BypassParameter(paramBypass, "Bypass").Build(),
	)
	p.OnInitialize(p.prepare)
	return p
}

type duckerProcessor struct {
	*frameworkplugin.BaseProcessor
	delay    [maxChannels][]float32
	writePos int
	latency  int32
	envelope float64
}

func (p *duckerProcessor) prepare(sampleRate float64, _ int32) error {
	p.latency = int32(math.Round(sampleRate * lookaheadMs / 1000))
	for ch := range p.delay {
		p.delay[ch] = make([]float32, p.latency+1)
	}
	p.writePos = 0
	p.envelope = 0
	return nil
}

// GetLatencySamples reports the lookahead so hosts compensate for it.
func (p *duckerProcessor) GetLatencySamples() int32 { return p.latency }

func (p *duckerProcessor) GetTailSamples() int32 { return p.latency }

func (p *duckerProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	sidechain := ctx.Sidechain()
	threshold := math.Pow(10, ctx.ParamPlain(paramThreshold)/20)
	depth := ctx.ParamPlain(paramDepth)
	if ctx.Param(paramBypass) >= 0.5 {
		depth = 0 // keep the lookahead delay so bypass stays latency-aligned
	}
	releaseCoeff := math.Exp(-1 / (math.Max(ctx.ParamPlain(paramRelease), 1) / 1000 * p.SampleRate()))
	size := len(p.delay[0])
	maxReduction := 0.0

	for i := 0; i < n; i++ {
		// Detect on the sidechain; without one the ducker stays open.
		level := 0.0
		for _, ch := range sidechain {
			level = math.Max(level, math.Abs(float64(ch[i])))
		}
		target := 0.0
		if level > threshold {
			target = 1
		}
		if target > p.envelope {
			p.envelope = target
		} else {
			p.envelope = target + (p.envelope-target)*releaseCoeff
		}
		reduction := depth * p.envelope
		maxReduction = math.Max(maxReduction, reduction)
		gain := float32(math.Pow(10, -reduction/20))

		readPos := (p.writePos + 1) % size
		for ch := 0; ch < len(ctx.Output) && ch < maxChannels; ch++ {
			in := float32(0)
			if ch < len(ctx.Input) {
				in = ctx.Input[ch][i]
			}
			p.delay[ch][p.writePos] = in
			ctx.Output[ch][i] = p.delay[ch][readPos] * gain
		}
		p.writePos = readPos
	}
	for ch := maxChannels; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	if meter, ok := p.Parameters().GetOK(paramReduction); ok {
		ctx.SetOutputParameter(paramReduction, meter.Normalize(maxReduction))
	}
}

func init() {
	plugin.Register(duckerPlugin{})
}

func main() {}
