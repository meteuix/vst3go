// Command filter is a multi-mode state variable filter (pkg/dsp/filter):
// lowpass, highpass, bandpass or notch with cutoff, resonance and dry/wet
// mix. Cutoff changes are smoothed so sweeps do not zipper.
//
// Build: scripts/build_vst3.sh examples/filter Filter
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/filter"
	"github.com/th-release/vst3go/pkg/dsp/mix"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramFilterType uint32 = iota
	paramCutoff
	paramResonance
	paramMix
)

// Filter types, in the order of the Filter Type choices.
const (
	typeLowpass = iota
	typeHighpass
	typeBandpass
	typeNotch
)

const numChannels = 2

type filterPlugin struct{}

func (filterPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.filter",
		Name:     "Multi-Mode Filter",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxFilter,
	}
}

func (filterPlugin) CreateProcessor() plugin.Processor {
	p := &filterProcessor{
		BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo()),
		svf:           filter.NewSVF(numChannels),
	}
	p.Parameters().Add(
		param.Choice(paramFilterType, "Filter Type", []param.ChoiceOption{
			{Value: typeLowpass, Name: "Lowpass"},
			{Value: typeHighpass, Name: "Highpass"},
			{Value: typeBandpass, Name: "Bandpass"},
			{Value: typeNotch, Name: "Notch"},
		}).Build(),
		param.FrequencyParameter(paramCutoff, "Cutoff", 20, 20000, 800).SmoothMultiplicative(0.02).Build(),
		param.QParameter(paramResonance, "Resonance", 0.5, 10, math.Sqrt2/2).Smooth(0.02).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
	)
	p.OnReset(p.svf.Reset)
	return p
}

type filterProcessor struct {
	*frameworkplugin.BaseProcessor
	svf *filter.SVF
}

func (p *filterProcessor) ProcessAudio(ctx *process.Context) {
	mode := int(math.Round(ctx.ParamPlain(paramFilterType)))
	cutoff := ctx.Smoothed(paramCutoff)
	q := ctx.Smoothed(paramResonance)
	wet := ctx.Smoothed(paramMix)
	channels := min(ctx.NumInputChannels(), ctx.NumOutputChannels(), numChannels)
	for ch := channels; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	sampleRate := p.SampleRate()
	nyquistGuard := sampleRate * 0.49
	for i := 0; i < ctx.NumSamples(); i++ {
		resonance := q.Next()
		p.svf.SetFrequencyAndQ(sampleRate, min(cutoff.Next(), nyquistGuard), resonance)
		amount := float32(wet.Next() / 100)
		for ch := 0; ch < channels; ch++ {
			dry := ctx.Input[ch][i]
			out := p.svf.ProcessSample(dry, ch)
			var filtered float32
			switch mode {
			case typeHighpass:
				filtered = out.Highpass
			case typeBandpass:
				// The SVF bandpass peaks at Q; scale it to unity gain.
				filtered = out.Bandpass / float32(resonance)
			case typeNotch:
				filtered = out.Notch
			default:
				filtered = out.Lowpass
			}
			ctx.Output[ch][i] = mix.DryWet(dry, filtered, amount)
		}
	}
}

func init() {
	plugin.Register(filterPlugin{})
}

func main() { standalone.Main() }
