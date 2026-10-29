// Command chain_fx builds its processing from pkg/framework/dsp chains and
// switches between two of them: "Dynamics" (DC blocker, gate, compressor,
// linked across the stereo channels) and "Simple" (DC blocker plus a
// pink noise layer).
//
// Build: scripts/build_vst3.sh examples/chain_fx ChainFX
package main

import (
	"github.com/th-release/vst3go/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/pkg/dsp/utility"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/dsp"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramBypass uint32 = iota
	paramGateThreshold
	paramCompThreshold
	paramCompRatio
	paramNoiseAmount
	paramChainSelect
)

const (
	chainDynamics = 0
	chainSimple   = 1

	// maxNoiseLevel is the noise amplitude at Noise Amount 100 % (-40 dB).
	maxNoiseLevel = 0.01
)

type chainFXPlugin struct{}

func (chainFXPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.chainfx",
		Name:     "ChainFX",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFx,
	}
}

func (chainFXPlugin) CreateProcessor() plugin.Processor {
	p := &chainFXProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.BypassParameter(paramBypass, "Bypass").Build(),
		param.ThresholdParameter(paramGateThreshold, "Gate Threshold", -80, 0, -60).Build(),
		param.ThresholdParameter(paramCompThreshold, "Comp Threshold", -60, 0, -20).Build(),
		param.RatioParameter(paramCompRatio, "Comp Ratio", 1, 20, 4).Build(),
		param.MixParameter(paramNoiseAmount, "Noise Amount").Default(0).Build(),
		param.Choice(paramChainSelect, "Chain Select", []param.ChoiceOption{
			{Value: chainDynamics, Name: "Dynamics"},
			{Value: chainSimple, Name: "Simple"},
		}).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type chainFXProcessor struct {
	*frameworkplugin.BaseProcessor

	gate       *dynamics.Gate
	compressor *dynamics.Compressor
	noise      *dsp.NoiseAdapter

	dynamicsChain *dsp.StereoChain
	simpleChain   *dsp.StereoChain
}

func (p *chainFXProcessor) prepare(sampleRate float64, _ int32) error {
	p.gate = dynamics.NewGate(sampleRate)
	p.gate.SetAttack(0.0001)
	p.gate.SetHold(0.010)
	p.gate.SetRelease(0.100)

	p.compressor = dynamics.NewCompressor(sampleRate)
	p.compressor.SetAttack(0.010)
	p.compressor.SetRelease(0.100)
	p.compressor.SetKnee(dynamics.KneeSoft, 2)

	p.noise = dsp.NewNoiseAdapter(utility.PinkNoise, 0)

	// Each chain gets its own DC blocker: a stage keeps per-channel state.
	var err error
	p.dynamicsChain, err = dsp.NewStereoBuilder("Dynamics").
		WithProcessor(dsp.NewDCBlockerAdapter(sampleRate)).
		WithProcessor(dsp.NewGateAdapter(p.gate)).
		WithProcessor(dsp.NewCompressorAdapter(p.compressor)).
		Build()
	if err != nil {
		return err
	}
	p.simpleChain, err = dsp.NewStereoBuilder("Simple").
		WithProcessor(dsp.NewDCBlockerAdapter(sampleRate)).
		WithProcessor(p.noise).
		Build()
	return err
}

func (p *chainFXProcessor) reset() {
	if p.dynamicsChain != nil {
		p.dynamicsChain.Reset()
		p.simpleChain.Reset()
	}
}

// GetTailSamples covers the gate hold and release.
func (p *chainFXProcessor) GetTailSamples() int32 {
	return int32(p.SampleRate() * 0.11)
}

func (p *chainFXProcessor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(paramBypass) >= 0.5 {
		ctx.PassThrough()
		return
	}

	p.gate.SetThreshold(ctx.ParamPlain(paramGateThreshold))
	p.compressor.SetThreshold(ctx.ParamPlain(paramCompThreshold))
	p.compressor.SetRatio(ctx.ParamPlain(paramCompRatio))
	p.noise.SetMix(float32(ctx.ParamPlain(paramNoiseAmount) / 100 * maxNoiseLevel))

	// The chains process in place, so start from a copy of the input.
	ctx.PassThrough()
	if len(ctx.Output) < 2 {
		return
	}
	left, right := ctx.Output[0], ctx.Output[1]
	if ctx.ParamPlain(paramChainSelect) >= chainSimple {
		p.simpleChain.ProcessStereo(left, right)
	} else {
		p.dynamicsChain.ProcessStereo(left, right)
	}
}

func init() {
	plugin.Register(chainFXPlugin{})
}

func main() { standalone.Main() }
