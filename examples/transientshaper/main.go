// Command transientshaper shapes the attacks and sustain of a stereo signal
// independently of its level (dynamics.TransientShaper): Attack makes
// onsets punchier or softer, Sustain makes tails longer or drier. A
// read-only meter shows the gain being applied.
//
// Build: scripts/build_vst3.sh examples/transientshaper TransientShaper
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/dynamics"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramAttack uint32 = iota
	paramSustain
	paramOutput
	paramMix
	paramGain
)

type transientShaperPlugin struct{}

func (transientShaperPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.transientshaper",
		Name:     "Transient Shaper",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (transientShaperPlugin) CreateProcessor() plugin.Processor {
	p := &transientShaperProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramAttack, "Attack").Range(-100, 100).Default(0).Unit("%").Build(),
		param.New(paramSustain, "Sustain").Range(-100, 100).Default(0).Unit("%").Build(),
		param.New(paramOutput, "Output").Range(-24, 24).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
		param.New(paramGain, "Shaping Gain").Range(-24, 24).Default(0).Unit("dB").ReadOnly().Build(),
	)
	p.OnInitialize(func(sampleRate float64, maxBlockSize int32) error {
		p.shaper = dynamics.NewTransientShaper(sampleRate)
		for ch := range p.wet {
			p.wet[ch] = make([]float32, maxBlockSize)
		}
		return nil
	})
	p.OnReset(func() { p.shaper.Reset() })
	return p
}

type transientShaperProcessor struct {
	*frameworkplugin.BaseProcessor
	shaper *dynamics.TransientShaper
	wet    [2][]float32
}

func (p *transientShaperProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	s := p.shaper
	s.SetAttack(ctx.ParamPlain(paramAttack) / 100)
	s.SetSustain(ctx.ParamPlain(paramSustain) / 100)

	inL, inR := ctx.Input[0][:n], ctx.Input[1][:n]
	wetL, wetR := p.wet[0][:n], p.wet[1][:n]
	s.ProcessStereo(inL, inR, wetL, wetR)

	output, mix := ctx.Smoothed(paramOutput), ctx.Smoothed(paramMix)
	outL, outR := ctx.Output[0], ctx.Output[1]
	for i := range n {
		g := float32(math.Pow(10, output.Next()/20))
		m := float32(mix.Next() / 100)
		outL[i] = (inL[i] + (wetL[i]-inL[i])*m) * g
		outR[i] = (inR[i] + (wetR[i]-inR[i])*m) * g
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
	ctx.SetOutputParameter(paramGain, p.Parameters().Get(paramGain).Normalize(s.GainDB()))
}

func init() {
	plugin.Register(transientShaperPlugin{})
}

func main() { standalone.Main() }
