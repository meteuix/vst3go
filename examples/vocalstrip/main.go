// Command vocalstrip is a vocal channel strip: gate, compressor, three-band
// EQ (low shelf, mid peak, high shelf) and a true-peak lookahead limiter,
// each with its own enable switch, then output gain. Gate, compressor and
// limiter are stereo-linked. The limiter's 5 ms lookahead is the plugin's
// latency whether the limiter is on or not: when it is off the signal runs
// through a matching delay, so toggling it never shifts the audio.
//
// Build: scripts/build_vst3.sh examples/vocalstrip VocalStrip
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
	paramGateOn uint32 = iota
	paramGateThreshold
	paramGateRange

	paramCompOn
	paramCompThreshold
	paramCompRatio
	paramCompAttack
	paramCompRelease

	paramEQOn
	paramLowFreq
	paramLowGain
	paramMidFreq
	paramMidGain
	paramMidQ
	paramHighFreq
	paramHighGain

	paramLimiterOn
	paramCeiling

	paramOutput
	paramCompReduction
	paramLimiterReduction
)

const limiterLookahead = 0.005 // seconds

type vocalStripPlugin struct{}

func (vocalStripPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.vocalstrip",
		Name:     "Vocal Strip",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxVocals,
	}
}

func onOff(id uint32, name string, def float64) *param.Builder {
	return param.Choice(id, name, []param.ChoiceOption{{Value: 0, Name: "Off"}, {Value: 1, Name: "On"}}).Default(def)
}

func (vocalStripPlugin) CreateProcessor() plugin.Processor {
	p := &vocalStripProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		onOff(paramGateOn, "Gate", 1).Build(),
		param.ThresholdParameter(paramGateThreshold, "Gate Threshold", -80, 0, -50).Build(),
		param.ThresholdParameter(paramGateRange, "Gate Range", -60, 0, -30).Build(),

		onOff(paramCompOn, "Compressor", 1).Build(),
		param.ThresholdParameter(paramCompThreshold, "Comp Threshold", -60, 0, -18).Build(),
		param.RatioParameter(paramCompRatio, "Comp Ratio", 1, 20, 3).Build(),
		param.TimeParameter(paramCompAttack, "Comp Attack", 0.1, 100, 5).Build(),
		param.TimeParameter(paramCompRelease, "Comp Release", 10, 1000, 100).Build(),

		onOff(paramEQOn, "EQ", 1).Build(),
		param.FrequencyParameter(paramLowFreq, "Low Freq", 20, 1000, 120).Build(),
		param.New(paramLowGain, "Low Gain").Range(-12, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.FrequencyParameter(paramMidFreq, "Mid Freq", 200, 10000, 2500).Build(),
		param.New(paramMidGain, "Mid Gain").Range(-12, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.QParameter(paramMidQ, "Mid Q", 0.1, 10, 1).Build(),
		param.FrequencyParameter(paramHighFreq, "High Freq", 1000, 20000, 8000).Build(),
		param.New(paramHighGain, "High Gain").Range(-12, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),

		onOff(paramLimiterOn, "Limiter", 1).Build(),
		param.ThresholdParameter(paramCeiling, "Ceiling", -12, 0, -1).Build(),

		param.New(paramOutput, "Output").Range(-12, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.New(paramCompReduction, "Comp Reduction").Range(0, 30).Default(0).Unit("dB").ReadOnly().Build(),
		param.New(paramLimiterReduction, "Limiter Reduction").Range(0, 20).Default(0).Unit("dB").ReadOnly().Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type vocalStripProcessor struct {
	*frameworkplugin.BaseProcessor
	gate       *dynamics.Gate
	compressor *dynamics.Compressor
	low        *filter.Biquad
	mid        *filter.Biquad
	high       *filter.Biquad
	limiter    *dynamics.Limiter

	// The limited signal and the bypass delay matching its latency.
	limited  [2][]float32
	bypass   [2][]float32
	writePos int

	reported int32 // latency last announced to the host
	host     atomic.Pointer[hostRef]
}

type hostRef struct{ plugin.Host }

func (p *vocalStripProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

func (p *vocalStripProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.gate = dynamics.NewGate(sampleRate)
	p.gate.SetAttack(0.001)
	p.gate.SetHold(0.010)
	p.gate.SetRelease(0.050)
	p.gate.SetHysteresis(3)
	p.compressor = dynamics.NewCompressor(sampleRate)
	p.compressor.SetKnee(dynamics.KneeSoft, 2)
	p.low, p.mid, p.high = filter.NewBiquad(2), filter.NewBiquad(2), filter.NewBiquad(2)
	p.limiter = dynamics.NewLimiter(sampleRate)
	p.limiter.SetRelease(0.050)
	p.limiter.SetTruePeak(true)
	p.limiter.SetLookahead(limiterLookahead)
	for ch := range p.limited {
		p.limited[ch] = make([]float32, maxBlockSize)
		p.bypass[ch] = make([]float32, p.limiter.Latency())
	}
	p.writePos = 0
	return nil
}

func (p *vocalStripProcessor) reset() {
	p.gate.Reset()
	p.compressor.Reset()
	p.low.Reset()
	p.mid.Reset()
	p.high.Reset()
	p.limiter.Reset()
	for ch := range p.bypass {
		clear(p.bypass[ch])
	}
	p.writePos = 0
}

// SetActive announces the latency (it depends on the sample rate).
func (p *vocalStripProcessor) SetActive(active bool) error {
	if host := p.host.Load(); active && host != nil && p.GetLatencySamples() != p.reported {
		p.reported = p.GetLatencySamples()
		_ = host.RestartComponent(plugin.RestartLatencyChanged)
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *vocalStripProcessor) GetLatencySamples() int32 { return int32(p.limiter.Latency()) }

func (p *vocalStripProcessor) GetTailSamples() int32 { return int32(0.2 * p.SampleRate()) }

func (p *vocalStripProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	outL, outR := ctx.Output[0][:n], ctx.Output[1][:n]
	copy(outL, ctx.Input[0])
	copy(outR, ctx.Input[1])
	on := func(id uint32) bool { return ctx.ParamPlain(id) >= 0.5 }
	sr := p.SampleRate()

	if on(paramGateOn) {
		g := p.gate
		g.SetThreshold(ctx.ParamPlain(paramGateThreshold))
		g.SetRange(ctx.ParamPlain(paramGateRange))
		g.ProcessStereo(outL, outR, outL, outR)
	}

	compReduction := 0.0
	if on(paramCompOn) {
		c := p.compressor
		c.SetThreshold(ctx.ParamPlain(paramCompThreshold))
		c.SetRatio(ctx.ParamPlain(paramCompRatio))
		c.SetAttack(ctx.ParamPlain(paramCompAttack) / 1000)
		c.SetRelease(ctx.ParamPlain(paramCompRelease) / 1000)
		c.ProcessStereo(outL, outR, outL, outR)
		compReduction = c.GetGainReduction()
	}

	if on(paramEQOn) {
		p.low.SetLowShelf(sr, ctx.ParamPlain(paramLowFreq), math.Sqrt2/2, ctx.ParamPlain(paramLowGain))
		p.mid.SetPeakingEQ(sr, ctx.ParamPlain(paramMidFreq), ctx.ParamPlain(paramMidQ), ctx.ParamPlain(paramMidGain))
		p.high.SetHighShelf(sr, ctx.ParamPlain(paramHighFreq), math.Sqrt2/2, ctx.ParamPlain(paramHighGain))
		for _, f := range []*filter.Biquad{p.low, p.mid, p.high} {
			f.Process(outL, 0)
			f.Process(outR, 1)
		}
	}

	// The limiter always runs so its state is current when it is switched
	// on; the bypass delay keeps the timing identical when it is off.
	l := p.limiter
	l.SetThreshold(ctx.ParamPlain(paramCeiling))
	limL, limR := p.limited[0][:n], p.limited[1][:n]
	l.ProcessStereo(outL, outR, limL, limR)
	limiterOn := on(paramLimiterOn)
	output := ctx.Smoothed(paramOutput)
	size := len(p.bypass[0])
	for i := range n {
		y0, y1 := limL[i], limR[i]
		if size > 0 {
			d0, d1 := p.bypass[0][p.writePos], p.bypass[1][p.writePos]
			p.bypass[0][p.writePos], p.bypass[1][p.writePos] = outL[i], outR[i]
			p.writePos = (p.writePos + 1) % size
			if !limiterOn {
				y0, y1 = d0, d1
			}
		} else if !limiterOn {
			y0, y1 = outL[i], outR[i]
		}
		g := float32(math.Pow(10, output.Next()/20))
		outL[i], outR[i] = y0*g, y1*g
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	limiterReduction := 0.0
	if limiterOn {
		limiterReduction = l.GetGainReduction()
	}
	params := p.Parameters()
	ctx.SetOutputParameter(paramCompReduction, params.Get(paramCompReduction).Normalize(compReduction))
	ctx.SetOutputParameter(paramLimiterReduction, params.Get(paramLimiterReduction).Normalize(limiterReduction))
}

func init() {
	plugin.Register(vocalStripPlugin{})
}

func main() { standalone.Main() }
