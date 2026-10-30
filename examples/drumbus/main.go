// Command drumbus is a drum bus processor with two paths. The main path
// runs a transient shaper (Attack, Sustain) into a soft-knee glue
// compressor. The parallel path is a hard, fast compressor whose detector
// ignores the low end below HPF Sidechain, with 10 dB of makeup; Parallel
// Mix adds it under the main path ("New York" compression). Both
// compressors are stereo-linked, and read-only meters show their gain
// reduction.
//
// Build: scripts/build_vst3.sh examples/drumbus DrumBus
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/dynamics"
	"github.com/meteuix/vst3go/v2/pkg/dsp/filter"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramParallelThreshold uint32 = iota
	paramParallelRatio
	paramParallelAttack
	paramParallelRelease
	paramParallelHPF
	paramTransientAttack
	paramTransientSustain
	paramGlueThreshold
	paramGlueRatio
	paramGlueAttack
	paramGlueRelease
	paramParallelMix
	paramOutput
	paramGlueReduction
	paramParallelReduction
)

const parallelMakeupDB = 10.0

type drumBusPlugin struct{}

func (drumBusPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.drumbus",
		Name:     "Drum Bus",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (drumBusPlugin) CreateProcessor() plugin.Processor {
	p := &drumBusProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.ThresholdParameter(paramParallelThreshold, "Parallel Threshold", -60, 0, -20).Build(),
		param.RatioParameter(paramParallelRatio, "Parallel Ratio", 2, 20, 10).Build(),
		param.TimeParameter(paramParallelAttack, "Parallel Attack", 0.1, 50, 5).Build(),
		param.TimeParameter(paramParallelRelease, "Parallel Release", 10, 500, 100).Build(),
		param.FrequencyParameter(paramParallelHPF, "HPF Sidechain", 20, 500, 100).Build(),
		param.New(paramTransientAttack, "Transient Attack").Range(-100, 100).Default(0).Unit("%").Build(),
		param.New(paramTransientSustain, "Transient Sustain").Range(-100, 100).Default(0).Unit("%").Build(),
		param.ThresholdParameter(paramGlueThreshold, "Glue Threshold", -30, 0, -10).Build(),
		param.RatioParameter(paramGlueRatio, "Glue Ratio", 1.5, 10, 3).Build(),
		param.TimeParameter(paramGlueAttack, "Glue Attack", 10, 100, 30).Build(),
		param.TimeParameter(paramGlueRelease, "Glue Release", 50, 500, 200).Build(),
		param.MixParameter(paramParallelMix, "Parallel Mix").Default(50).Smooth(0.02).Build(),
		param.New(paramOutput, "Output").Range(-20, 20).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.New(paramGlueReduction, "Glue Reduction").Range(0, 20).Default(0).Unit("dB").ReadOnly().Build(),
		param.New(paramParallelReduction, "Parallel Reduction").Range(0, 40).Default(0).Unit("dB").ReadOnly().Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() {
		p.parallel.Reset()
		p.glue.Reset()
		p.shaper.Reset()
		p.keyHPF.Reset()
	})
	return p
}

type drumBusProcessor struct {
	*frameworkplugin.BaseProcessor
	parallel *dynamics.Compressor
	glue     *dynamics.Compressor
	shaper   *dynamics.TransientShaper
	keyHPF   *filter.Biquad
	hpfFreq  float64

	key       []float32
	parallelL []float32
	parallelR []float32
}

func (p *drumBusProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.parallel = dynamics.NewCompressor(sampleRate)
	p.parallel.SetKnee(dynamics.KneeHard, 0)
	p.parallel.SetMakeupGain(parallelMakeupDB)
	p.glue = dynamics.NewCompressor(sampleRate)
	p.glue.SetKnee(dynamics.KneeSoft, 4)
	p.shaper = dynamics.NewTransientShaper(sampleRate)
	p.keyHPF = filter.NewBiquad(2)
	p.hpfFreq = 0
	p.key = make([]float32, maxBlockSize)
	p.parallelL = make([]float32, maxBlockSize)
	p.parallelR = make([]float32, maxBlockSize)
	return nil
}

func (p *drumBusProcessor) GetTailSamples() int32 {
	return int32(0.5 * p.SampleRate())
}

func (p *drumBusProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	inL, inR := ctx.Input[0][:n], ctx.Input[1][:n]
	outL, outR := ctx.Output[0][:n], ctx.Output[1][:n]
	parL, parR := p.parallelL[:n], p.parallelR[:n]

	// Parallel path, from the input before the main path overwrites it
	// (hosts may process in place). The detector hears the high-passed,
	// linked input: kick drums do not pump it.
	if hpf := ctx.ParamPlain(paramParallelHPF); hpf != p.hpfFreq {
		p.keyHPF.SetHighpass(p.SampleRate(), hpf, math.Sqrt2/2)
		p.hpfFreq = hpf
	}
	keyL, keyR := p.key[:n], parL // parL is scratch until the compressor fills it
	copy(keyL, inL)
	copy(keyR, inR)
	p.keyHPF.Process(keyL, 0)
	p.keyHPF.Process(keyR, 1)
	for i := range keyL {
		keyL[i] = max(float32(math.Abs(float64(keyL[i]))), float32(math.Abs(float64(keyR[i]))))
	}
	c := p.parallel
	c.SetThreshold(ctx.ParamPlain(paramParallelThreshold))
	c.SetRatio(ctx.ParamPlain(paramParallelRatio))
	c.SetAttack(ctx.ParamPlain(paramParallelAttack) / 1000)
	c.SetRelease(ctx.ParamPlain(paramParallelRelease) / 1000)
	c.ProcessStereoSidechain(inL, inR, keyL, parL, parR)

	// Main path: transient shaper into the glue compressor.
	s := p.shaper
	s.SetAttack(ctx.ParamPlain(paramTransientAttack) / 100)
	s.SetSustain(ctx.ParamPlain(paramTransientSustain) / 100)
	s.ProcessStereo(inL, inR, outL, outR)
	g := p.glue
	g.SetThreshold(ctx.ParamPlain(paramGlueThreshold))
	g.SetRatio(ctx.ParamPlain(paramGlueRatio))
	g.SetAttack(ctx.ParamPlain(paramGlueAttack) / 1000)
	g.SetRelease(ctx.ParamPlain(paramGlueRelease) / 1000)
	g.ProcessStereo(outL, outR, outL, outR)

	mix, output := ctx.Smoothed(paramParallelMix), ctx.Smoothed(paramOutput)
	for i := range n {
		m := float32(mix.Next() / 100)
		gain := float32(math.Pow(10, output.Next()/20))
		outL[i] = (outL[i] + parL[i]*m) * gain
		outR[i] = (outR[i] + parR[i]*m) * gain
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	params := p.Parameters()
	ctx.SetOutputParameter(paramGlueReduction, params.Get(paramGlueReduction).Normalize(g.GetGainReduction()))
	ctx.SetOutputParameter(paramParallelReduction, params.Get(paramParallelReduction).Normalize(c.GetGainReduction()))
}

func init() {
	plugin.Register(drumBusPlugin{})
}

func main() { standalone.Main() }
