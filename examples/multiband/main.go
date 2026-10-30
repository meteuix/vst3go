// Command multiband is a three-band stereo compressor. A Linkwitz-Riley
// crossover splits the signal at Low/Mid and Mid/High; each band has its own
// linked compressor with threshold, ratio and makeup gain, and the bands are
// summed back. With every band left uncompressed the bands sum to the input
// (an all-pass), so the plugin colours nothing until it compresses.
//
// Build: scripts/build_vst3.sh examples/multiband Multiband
package main

import (
	"math"

	"github.com/th-release/vst3go/v2/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/v2/pkg/dsp/filter"
	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const numBands = 3

// Global parameters; band b's parameters start at bandParam(b, 0).
const (
	paramLowMid uint32 = iota
	paramMidHigh
	paramAttack
	paramRelease
	paramOutput
)

const (
	bandThreshold uint32 = iota
	bandRatio
	bandMakeup
	bandReduction
)

func bandParam(b int, p uint32) uint32 { return 100 + uint32(b)*10 + p }

var bandNames = [numBands]string{"Low", "Mid", "High"}

type multibandPlugin struct{}

func (multibandPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.multiband",
		Name:     "Multiband",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (multibandPlugin) CreateProcessor() plugin.Processor {
	p := &multibandProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	params := p.Parameters()
	params.Add(
		param.FrequencyParameter(paramLowMid, "Low/Mid", 40, 1000, 200).Build(),
		param.FrequencyParameter(paramMidHigh, "Mid/High", 1000, 16000, 3000).Build(),
		param.AttackParameter(paramAttack, "Attack", 100).Build(),
		param.ReleaseParameter(paramRelease, "Release", 1000).Build(),
		param.New(paramOutput, "Output").Range(-24, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
	)
	for b, name := range bandNames {
		params.Add(
			param.ThresholdParameter(bandParam(b, bandThreshold), name+" Threshold", -60, 0, 0).Build(),
			param.RatioParameter(bandParam(b, bandRatio), name+" Ratio", 1, 20, 2).Build(),
			param.New(bandParam(b, bandMakeup), name+" Makeup").Range(-12, 24).Default(0).Unit("dB").
				Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
			param.New(bandParam(b, bandReduction), name+" Reduction").Range(0, 24).Default(0).Unit("dB").
				ReadOnly().Build(),
		)
	}
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type multibandProcessor struct {
	*frameworkplugin.BaseProcessor
	crossover   *filter.Crossover
	compressors [numBands]*dynamics.Compressor
	// split[ch][b] is band b of channel ch for the current block.
	split [2][numBands][]float32
}

func (p *multibandProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	xo, err := filter.NewCrossover(sampleRate, 2, 200, 3000)
	if err != nil {
		return err
	}
	p.crossover = xo
	for b := range p.compressors {
		p.compressors[b] = dynamics.NewCompressor(sampleRate)
	}
	for ch := range p.split {
		for b := range p.split[ch] {
			p.split[ch][b] = make([]float32, maxBlockSize)
		}
	}
	p.reset()
	return nil
}

func (p *multibandProcessor) reset() {
	p.crossover.Reset()
	for _, c := range p.compressors {
		c.Reset()
	}
}

func (p *multibandProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	p.crossover.SetFrequency(0, ctx.ParamPlain(paramLowMid))
	p.crossover.SetFrequency(1, ctx.ParamPlain(paramMidHigh))
	attack, release := ctx.ParamPlain(paramAttack)/1000, ctx.ParamPlain(paramRelease)/1000

	var bands [2][numBands][]float32
	for ch := range bands {
		for b := range bands[ch] {
			bands[ch][b] = p.split[ch][b][:n]
		}
		p.crossover.Process(ctx.Input[ch], bands[ch][:], ch)
	}

	params := p.Parameters()
	for b, c := range p.compressors {
		c.SetThreshold(ctx.ParamPlain(bandParam(b, bandThreshold)))
		c.SetRatio(ctx.ParamPlain(bandParam(b, bandRatio)))
		c.SetAttack(attack)
		c.SetRelease(release)
		c.SetMakeupGain(ctx.ParamPlain(bandParam(b, bandMakeup)))
		c.ProcessStereo(bands[0][b], bands[1][b], bands[0][b], bands[1][b])
		id := bandParam(b, bandReduction)
		ctx.SetOutputParameter(id, params.Get(id).Normalize(c.GetGainReduction()))
	}

	output := ctx.Smoothed(paramOutput)
	for i := range n {
		gain := float32(math.Pow(10, output.Next()/20))
		for ch := range bands {
			var sum float32
			for b := range numBands {
				sum += bands[ch][b][i]
			}
			ctx.Output[ch][i] = sum * gain
		}
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(multibandPlugin{})
}

func main() { standalone.Main() }
