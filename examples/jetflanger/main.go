// Command jetflanger is a stereo flanger (modulation.Flanger) with high
// feedback for the jet sweep. Negative FB inverts the feedback for a
// hollower tone; Manual Mode parks the sweep at Manual instead of following
// the LFO. The right channel sweeps opposite to the left.
//
// Build: scripts/build_vst3.sh examples/jetflanger JetFlanger
package main

import (
	"github.com/meteuix/vst3go/v2/pkg/dsp/modulation"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramRate uint32 = iota
	paramDelay
	paramDepth
	paramFeedback
	paramNegativeFeedback
	paramManual
	paramManualMode
	paramMix
)

var offOn = []param.ChoiceOption{{Value: 0, Name: "Off"}, {Value: 1, Name: "On"}}

type jetFlangerPlugin struct{}

func (jetFlangerPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.jetflanger",
		Name:     "Jet Flanger",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxModulation,
	}
}

func (jetFlangerPlugin) CreateProcessor() plugin.Processor {
	p := &jetFlangerProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.RateParameter(paramRate, "Rate", 0.05, 10, 0.5).Build(),
		param.TimeParameter(paramDelay, "Delay", 0.5, 10, 5).Build(),
		param.New(paramDepth, "Depth").Range(0, 10).Default(2).Unit("ms").Build(),
		param.New(paramFeedback, "Feedback").Range(0, 99).Default(70).Unit("%").Build(),
		param.Choice(paramNegativeFeedback, "Negative FB", offOn).Build(),
		param.New(paramManual, "Manual").Range(0, 100).Default(50).Unit("%").Build(),
		param.Choice(paramManualMode, "Manual Mode", offOn).Build(),
		param.MixParameter(paramMix, "Mix").Default(50).Build(),
	)
	p.OnInitialize(func(sampleRate float64, _ int32) error {
		p.flanger = modulation.NewFlanger(sampleRate)
		return nil
	})
	p.OnReset(func() { p.flanger.Reset() })
	return p
}

type jetFlangerProcessor struct {
	*frameworkplugin.BaseProcessor
	flanger *modulation.Flanger
}

// GetTailSamples covers the feedback ringing out.
func (p *jetFlangerProcessor) GetTailSamples() int32 {
	return int32(p.SampleRate())
}

func (p *jetFlangerProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	f := p.flanger
	f.SetRate(ctx.ParamPlain(paramRate))
	f.SetDelay(ctx.ParamPlain(paramDelay))
	f.SetDepth(ctx.ParamPlain(paramDepth))
	feedback := ctx.ParamPlain(paramFeedback) / 100
	if ctx.ParamPlain(paramNegativeFeedback) >= 0.5 {
		feedback = -feedback
	}
	f.SetFeedback(feedback)
	f.SetManual(ctx.ParamPlain(paramManual) / 100)
	f.SetManualMode(ctx.ParamPlain(paramManualMode) >= 0.5)
	f.SetMix(ctx.ParamPlain(paramMix) / 100)

	n := ctx.NumSamples()
	f.ProcessStereoBuffer(ctx.Input[0][:n], ctx.Input[1][:n], ctx.Output[0][:n], ctx.Output[1][:n])
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(jetFlangerPlugin{})
}

func main() { standalone.Main() }
