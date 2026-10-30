// Command granulator is a granular delay and freezer: the input (summed
// to mono) is recorded into a four-second buffer and replayed as short
// Hann-windowed grains from Position behind the newest input, with Spray,
// Jitter, Pitch Spray and Spread scattering where, when, at what pitch and
// where in the stereo field each grain plays. Freeze stops recording so
// the grains keep looping over the held audio.
//
// Build: scripts/build_vst3.sh examples/granulator Granulator
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/granular"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramSize uint32 = iota
	paramDensity
	paramPosition
	paramSpray
	paramPitch
	paramPitchSpray
	paramSpread
	paramJitter
	paramFreeze
	paramMix
)

const (
	bufferSeconds = 4
	maxGrains     = 128
)

type granulatorPlugin struct{}

func (granulatorPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.granulator",
		Name:     "Granulator",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDelay,
	}
}

func percent(id uint32, name string, def float64) *param.Builder {
	return param.New(id, name).Range(0, 100).Default(def).Unit("%").
		Formatter(param.PercentFormatter, param.PercentParser)
}

func (granulatorPlugin) CreateProcessor() plugin.Processor {
	p := &granulatorProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.TimeParameter(paramSize, "Size", 5, 500, 80).Build(),
		param.New(paramDensity, "Density").Range(1, 100).Default(20).Logarithmic().Unit("/s").Build(),
		param.TimeParameter(paramPosition, "Position", 0, 3500, 200).Build(),
		param.TimeParameter(paramSpray, "Spray", 0, 500, 0).Build(),
		param.New(paramPitch, "Pitch").Range(-24, 24).Default(0).Steps(48).Unit("st").Build(),
		param.New(paramPitchSpray, "Pitch Spray").Range(0, 12).Default(0).Unit("st").Build(),
		percent(paramSpread, "Spread", 0).Build(),
		percent(paramJitter, "Jitter", 0).Build(),
		param.Choice(paramFreeze, "Freeze", []param.ChoiceOption{{Value: 0, Name: "Off"}, {Value: 1, Name: "On"}}).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.grains.Reset() })
	return p
}

type granulatorProcessor struct {
	*frameworkplugin.BaseProcessor
	grains     *granular.Granulator
	mono, l, r []float32
}

func (p *granulatorProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.grains = granular.New(sampleRate, bufferSeconds, maxGrains)
	p.mono = make([]float32, maxBlockSize)
	p.l = make([]float32, maxBlockSize)
	p.r = make([]float32, maxBlockSize)
	return nil
}

func (p *granulatorProcessor) GetTailSamples() int32 {
	return int32(bufferSeconds * p.SampleRate())
}

func (p *granulatorProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) == 0 || len(ctx.Output) < 2 {
		ctx.Clear()
		return
	}
	n := ctx.NumSamples()
	g := p.grains
	g.SetSize(ctx.ParamPlain(paramSize) / 1000)
	g.SetDensity(ctx.ParamPlain(paramDensity))
	g.SetPosition(ctx.ParamPlain(paramPosition) / 1000)
	g.SetSpray(ctx.ParamPlain(paramSpray) / 1000)
	g.SetPitch(math.Exp2(ctx.ParamPlain(paramPitch) / 12))
	g.SetPitchSpray(ctx.ParamPlain(paramPitchSpray))
	g.SetSpread(ctx.ParamPlain(paramSpread) / 100)
	g.SetJitter(ctx.ParamPlain(paramJitter) / 100)
	g.Freeze(ctx.ParamPlain(paramFreeze) >= 0.5)

	mono := p.mono[:n]
	copy(mono, ctx.Input[0][:n])
	if len(ctx.Input) > 1 {
		for i, x := range ctx.Input[1][:n] {
			mono[i] = 0.5 * (mono[i] + x)
		}
	}
	wetL, wetR := p.l[:n], p.r[:n]
	g.Process(mono, wetL, wetR)

	mix := ctx.Smoothed(paramMix)
	inL, inR := ctx.Input[0][:n], ctx.Input[0][:n]
	if len(ctx.Input) > 1 {
		inR = ctx.Input[1][:n]
	}
	outL, outR := ctx.Output[0][:n], ctx.Output[1][:n]
	for i := range n {
		m := float32(mix.Next() / 100)
		dl, dr := inL[i], inR[i]
		outL[i] = dl*(1-m) + wetL[i]*m
		outR[i] = dr*(1-m) + wetR[i]*m
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(granulatorPlugin{})
}

func main() { standalone.Main() }
