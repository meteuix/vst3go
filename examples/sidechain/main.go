// Command sidechain is a sidechain compressor: the main stereo signal is
// compressed by the level of the sidechain input (the louder of its
// channels), with the same gain on both main channels. With External Key
// off, or with no sidechain connected, it compresses by its own level. A
// read-only meter reports the gain reduction at the end of each block.
//
// Build: scripts/build_vst3.sh examples/sidechain Sidechain
package main

import (
	"math"

	"github.com/th-release/vst3go/v2/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const (
	paramThreshold uint32 = iota
	paramRatio
	paramAttack
	paramRelease
	paramKnee
	paramMakeup
	paramMix
	paramExternalKey
	paramReduction
)

type sidechainPlugin struct{}

func (sidechainPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.sidechain",
		Name:     "Sidechain Compressor",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (sidechainPlugin) CreateProcessor() plugin.Processor {
	p := &sidechainProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereoSidechain())}
	p.Parameters().Add(
		param.ThresholdParameter(paramThreshold, "Threshold", -60, 0, -20).Build(),
		param.RatioParameter(paramRatio, "Ratio", 1, 20, 4).Build(),
		param.AttackParameter(paramAttack, "Attack", 100).Build(),
		param.ReleaseParameter(paramRelease, "Release", 1000).Build(),
		param.New(paramKnee, "Knee").Range(0, 12).Default(2).Unit("dB").Build(),
		param.New(paramMakeup, "Makeup Gain").Range(-12, 24).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
		param.Choice(paramExternalKey, "External Key", []param.ChoiceOption{
			{Value: 0, Name: "Off"},
			{Value: 1, Name: "On"},
		}).Default(1).Build(),
		param.New(paramReduction, "Gain Reduction").Range(0, 48).Default(0).Unit("dB").ReadOnly().Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.compressor.Reset() })
	return p
}

type sidechainProcessor struct {
	*frameworkplugin.BaseProcessor
	compressor *dynamics.Compressor
	key        []float32
	dry        [2][]float32
}

func (p *sidechainProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.compressor = dynamics.NewCompressor(sampleRate)
	p.key = make([]float32, maxBlockSize)
	for ch := range p.dry {
		p.dry[ch] = make([]float32, maxBlockSize)
	}
	return nil
}

// GetTailSamples covers the release after the key goes quiet.
func (p *sidechainProcessor) GetTailSamples() int32 {
	return int32(math.Ceil(p.Parameters().Get(paramRelease).GetPlainValue() / 1000 * p.SampleRate()))
}

func (p *sidechainProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	c := p.compressor
	c.SetThreshold(ctx.ParamPlain(paramThreshold))
	c.SetRatio(ctx.ParamPlain(paramRatio))
	c.SetAttack(ctx.ParamPlain(paramAttack) / 1000)
	c.SetRelease(ctx.ParamPlain(paramRelease) / 1000)
	c.SetKnee(dynamics.KneeSoft, ctx.ParamPlain(paramKnee))
	c.SetMakeupGain(ctx.ParamPlain(paramMakeup))

	// The detection signal: the louder sidechain channel, or the louder
	// main channel when there is no key.
	keyIn := ctx.Input
	if side := ctx.Sidechain(); len(side) > 0 && ctx.ParamPlain(paramExternalKey) >= 0.5 {
		keyIn = side
	}
	key := p.key[:n]
	clear(key)
	for _, ch := range keyIn {
		for i, x := range ch[:n] {
			key[i] = max(key[i], float32(math.Abs(float64(x))))
		}
	}

	// Hosts may process in place: keep the dry signal for the mix.
	dryL, dryR := p.dry[0][:n], p.dry[1][:n]
	copy(dryL, ctx.Input[0])
	copy(dryR, ctx.Input[1])
	outL, outR := ctx.Output[0][:n], ctx.Output[1][:n]

	c.ProcessStereoSidechain(dryL, dryR, key, outL, outR)
	mix := ctx.Smoothed(paramMix)
	for i := range n {
		wet := float32(mix.Next() / 100)
		outL[i] = dryL[i] + (outL[i]-dryL[i])*wet
		outR[i] = dryR[i] + (outR[i]-dryR[i])*wet
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	ctx.SetOutputParameter(paramReduction, p.Parameters().Get(paramReduction).Normalize(c.GetGainReduction()))
}

func init() {
	plugin.Register(sidechainPlugin{})
}

func main() { standalone.Main() }
