// Command vintagechorus is a stereo chorus (modulation.Chorus) with up to
// four voices spread across the stereo field, feedback and dry/wet mix.
//
// Build: scripts/build_vst3.sh examples/vintagechorus VintageChorus
package main

import (
	"github.com/th-release/vst3go/v2/pkg/dsp/modulation"
	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const (
	paramRate uint32 = iota
	paramDepth
	paramDelay
	paramFeedback
	paramSpread
	paramVoices
	paramMix
)

type vintageChorusPlugin struct{}

func (vintageChorusPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.vintagechorus",
		Name:     "Vintage Chorus",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxModulation,
	}
}

func (vintageChorusPlugin) CreateProcessor() plugin.Processor {
	p := &vintageChorusProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.RateParameter(paramRate, "Rate", 0.1, 10, 0.5).Build(),
		param.New(paramDepth, "Depth").Range(0, 10).Default(2).Unit("ms").Build(),
		param.TimeParameter(paramDelay, "Delay", 10, 50, 20).Build(),
		param.New(paramFeedback, "Feedback").Range(0, 50).Default(10).Unit("%").Build(),
		param.New(paramSpread, "Spread").Range(0, 100).Default(100).Unit("%").Build(),
		param.Choice(paramVoices, "Voices", []param.ChoiceOption{
			{Value: 1, Name: "1"},
			{Value: 2, Name: "2"},
			{Value: 3, Name: "3"},
			{Value: 4, Name: "4"},
		}).Default(2).Build(),
		param.MixParameter(paramMix, "Mix").Default(50).Build(),
	)
	p.OnInitialize(func(sampleRate float64, _ int32) error {
		p.chorus = modulation.NewChorus(sampleRate)
		return nil
	})
	p.OnReset(func() { p.chorus.Reset() })
	return p
}

type vintageChorusProcessor struct {
	*frameworkplugin.BaseProcessor
	chorus *modulation.Chorus
}

// GetTailSamples covers the longest delay and a feedback decay.
func (p *vintageChorusProcessor) GetTailSamples() int32 {
	return int32(0.5 * p.SampleRate())
}

func (p *vintageChorusProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	c := p.chorus
	c.SetRate(ctx.ParamPlain(paramRate))
	c.SetDepth(ctx.ParamPlain(paramDepth))
	c.SetDelay(ctx.ParamPlain(paramDelay))
	c.SetFeedback(ctx.ParamPlain(paramFeedback) / 100)
	c.SetSpread(ctx.ParamPlain(paramSpread) / 100)
	c.SetVoices(int(ctx.ParamPlain(paramVoices)))
	c.SetMix(ctx.ParamPlain(paramMix) / 100)

	n := ctx.NumSamples()
	c.ProcessStereoBuffer(ctx.Input[0][:n], ctx.Input[1][:n], ctx.Output[0][:n], ctx.Output[1][:n])
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(vintageChorusPlugin{})
}

func main() { standalone.Main() }
