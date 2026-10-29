// Command delay is a stereo feedback delay built on pkg/dsp/delay: delay
// time, feedback and dry/wet mix, with the delay time smoothed so
// automation glides instead of clicking.
//
// Build: scripts/build_vst3.sh examples/delay Delay
package main

import (
	"github.com/th-release/vst3go/pkg/dsp/delay"
	"github.com/th-release/vst3go/pkg/dsp/mix"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramDelayTime uint32 = iota
	paramFeedback
	paramMix
)

const (
	maxDelaySeconds = 1.0
	numChannels     = 2
)

type delayPlugin struct{}

func (delayPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.delay",
		Name:     "Simple Delay",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDelay,
	}
}

func (delayPlugin) CreateProcessor() plugin.Processor {
	p := &delayProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramDelayTime, "Delay Time").
			Range(1, maxDelaySeconds*1000).
			Default(250).
			Unit("ms").
			Formatter(param.TimeFormatter, param.TimeParser).
			SmoothMultiplicative(0.05).
			Build(),
		param.New(paramFeedback, "Feedback").
			Range(0, 95).
			Default(30).
			Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).
			Smooth(0.02).
			Build(),
		param.MixParameter(paramMix, "Mix").Default(50).Smooth(0.02).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type delayProcessor struct {
	*frameworkplugin.BaseProcessor
	lines [numChannels]*delay.Line
}

func (p *delayProcessor) prepare(sampleRate float64, _ int32) error {
	for ch := range p.lines {
		p.lines[ch] = delay.New(maxDelaySeconds, sampleRate)
	}
	return nil
}

func (p *delayProcessor) reset() {
	for _, line := range p.lines {
		if line != nil {
			line.Reset()
		}
	}
}

// GetTailSamples covers the feedback decaying by 60 dB at the longest delay
// and the highest feedback.
func (p *delayProcessor) GetTailSamples() int32 {
	return int32(p.SampleRate() * maxDelaySeconds * 135)
}

func (p *delayProcessor) ProcessAudio(ctx *process.Context) {
	delayMs := ctx.Smoothed(paramDelayTime)
	feedback := ctx.Smoothed(paramFeedback)
	wet := ctx.Smoothed(paramMix)
	channels := min(ctx.NumInputChannels(), ctx.NumOutputChannels(), numChannels)
	for ch := channels; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	sampleRate := p.SampleRate()
	for i := 0; i < ctx.NumSamples(); i++ {
		d := delayMs.Next() * sampleRate / 1000
		fb := float32(feedback.Next() / 100)
		amount := float32(wet.Next() / 100)
		for ch := 0; ch < channels; ch++ {
			line := p.lines[ch]
			dry := ctx.Input[ch][i]
			delayed := line.Read(d)
			ctx.Output[ch][i] = mix.DryWet(dry, delayed, amount)
			line.Write(dry + delayed*fb)
		}
	}
}

func init() {
	plugin.Register(delayPlugin{})
}

func main() { standalone.Main() }
