// Command mastercompressor is a linked stereo bus compressor with soft
// knee, automatic makeup gain, a high-pass filter on the detector and up to
// 10 ms of lookahead. The audio always runs through the full 10 ms delay and
// Lookahead only moves the detector, so the reported latency stays constant
// and Lookahead can be automated without glitches or host restarts.
//
// Build: scripts/build_vst3.sh examples/mastercompressor MasterCompressor
package main

import (
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/pkg/dsp/filter"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramThreshold uint32 = iota
	paramRatio
	paramAttack
	paramRelease
	paramKnee
	paramMakeup
	paramAutoMakeup
	paramLookahead
	paramKeyHPF
	paramReduction
	paramOutputPeak
)

const (
	maxLookaheadMs = 10.0
	keyHPFOff      = 20.0 // Key HPF at its minimum leaves the detector unfiltered
)

type masterCompressorPlugin struct{}

func (masterCompressorPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.mastercompressor",
		Name:     "Master Compressor",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (masterCompressorPlugin) CreateProcessor() plugin.Processor {
	p := &masterCompressorProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.ThresholdParameter(paramThreshold, "Threshold", -60, 0, -12).Build(),
		param.RatioParameter(paramRatio, "Ratio", 1, 20, 4).Build(),
		param.AttackParameter(paramAttack, "Attack", 100).Build(),
		param.ReleaseParameter(paramRelease, "Release", 1000).Build(),
		param.New(paramKnee, "Knee").Range(0, 12).Default(2).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.New(paramMakeup, "Makeup Gain").Range(-12, 24).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.Choice(paramAutoMakeup, "Auto Makeup", []param.ChoiceOption{
			{Value: 0, Name: "Off"},
			{Value: 1, Name: "On"},
		}).Build(),
		param.TimeParameter(paramLookahead, "Lookahead", 0, maxLookaheadMs, 2).Build(),
		param.FrequencyParameter(paramKeyHPF, "Key HPF", keyHPFOff, 500, keyHPFOff).Build(),
		param.New(paramReduction, "Gain Reduction").Range(0, 24).Default(0).Unit("dB").ReadOnly().Build(),
		param.OutputLevelMeter(paramOutputPeak, "Output Peak").Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type masterCompressorProcessor struct {
	*frameworkplugin.BaseProcessor
	compressor *dynamics.Compressor
	keyHPF     *filter.Biquad
	hpfFreq    float64
	keyL, keyR []float32

	// The linked detection signal is delayed by (10 ms - Lookahead).
	keyDelay []float32
	keyPos   int

	latency  int32
	reported int32 // latency last announced to the host
	host     atomic.Pointer[hostRef]
}

type hostRef struct{ plugin.Host }

func (p *masterCompressorProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

func (p *masterCompressorProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.compressor = dynamics.NewCompressor(sampleRate)
	p.compressor.SetLookahead(maxLookaheadMs / 1000)
	p.latency = int32(p.compressor.Latency())
	p.keyHPF = filter.NewBiquad(2)
	p.hpfFreq = 0
	p.keyL = make([]float32, maxBlockSize)
	p.keyR = make([]float32, maxBlockSize)
	p.keyDelay = make([]float32, p.latency+1)
	p.reset()
	return nil
}

func (p *masterCompressorProcessor) reset() {
	p.compressor.Reset()
	p.keyHPF.Reset()
	clear(p.keyDelay)
	p.keyPos = 0
}

// SetActive announces a changed latency (it depends on the sample rate).
func (p *masterCompressorProcessor) SetActive(active bool) error {
	if host := p.host.Load(); active && host != nil && p.latency != p.reported {
		p.reported = p.latency
		_ = host.RestartComponent(plugin.RestartLatencyChanged)
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *masterCompressorProcessor) GetLatencySamples() int32 { return p.latency }

func (p *masterCompressorProcessor) GetTailSamples() int32 { return p.latency }

// autoMakeup estimates the average gain reduction for program material
// peaking around 0 dBFS: half the threshold's distance, compressed.
func autoMakeup(threshold, ratio float64) float64 {
	return -threshold / 2 * (1 - 1/ratio)
}

func (p *masterCompressorProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	c := p.compressor
	threshold, ratio := ctx.ParamPlain(paramThreshold), ctx.ParamPlain(paramRatio)
	c.SetThreshold(threshold)
	c.SetRatio(ratio)
	c.SetAttack(ctx.ParamPlain(paramAttack) / 1000)
	c.SetRelease(ctx.ParamPlain(paramRelease) / 1000)
	if knee := ctx.ParamPlain(paramKnee); knee > 0 {
		c.SetKnee(dynamics.KneeSoft, knee)
	} else {
		c.SetKnee(dynamics.KneeHard, 0)
	}
	makeup := ctx.ParamPlain(paramMakeup)
	if ctx.ParamPlain(paramAutoMakeup) >= 0.5 {
		makeup = autoMakeup(threshold, ratio)
	}
	c.SetMakeupGain(makeup)

	// Detection signal: both channels through the optional HPF, linked.
	keyL, keyR := p.keyL[:n], p.keyR[:n]
	copy(keyL, ctx.Input[0])
	copy(keyR, ctx.Input[1])
	if hpf := ctx.ParamPlain(paramKeyHPF); hpf > keyHPFOff+0.5 {
		if hpf != p.hpfFreq {
			p.keyHPF.SetHighpass(p.SampleRate(), hpf, math.Sqrt2/2)
			p.hpfFreq = hpf
		}
		p.keyHPF.Process(keyL, 0)
		p.keyHPF.Process(keyR, 1)
	}
	size := len(p.keyDelay)
	keyLag := int(p.latency) - int(math.Round(ctx.ParamPlain(paramLookahead)/1000*p.SampleRate()))
	keyLag = max(0, min(keyLag, size-1))
	for i := range keyL {
		p.keyDelay[p.keyPos] = max(float32(math.Abs(float64(keyL[i]))), float32(math.Abs(float64(keyR[i]))))
		keyL[i] = p.keyDelay[(p.keyPos-keyLag+size)%size]
		p.keyPos = (p.keyPos + 1) % size
	}

	// Each output sample depends only on earlier input, so in-place
	// processing is safe.
	c.ProcessStereoSidechain(ctx.Input[0], ctx.Input[1], keyL, ctx.Output[0], ctx.Output[1])
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	peak := 0.0
	for _, out := range ctx.Output[:2] {
		for _, x := range out {
			peak = max(peak, math.Abs(float64(x)))
		}
	}
	params := p.Parameters()
	ctx.SetOutputParameter(paramReduction, params.Get(paramReduction).Normalize(c.GetGainReduction()))
	ctx.SetOutputParameter(paramOutputPeak, params.Get(paramOutputPeak).Normalize(20*math.Log10(max(peak, 1e-3))))
}

func init() {
	plugin.Register(masterCompressorPlugin{})
}

func main() { standalone.Main() }
