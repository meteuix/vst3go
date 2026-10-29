// Command masterlimiter is a stereo brick-wall limiter for the master bus:
// a linked lookahead limiter with optional true-peak (4x oversampled)
// detection keeps both channels at or under the ceiling. Lookahead sets
// the latency, so it takes effect when processing (re)starts and the new
// latency is announced to the host then; Ceiling, Release and True Peak
// follow automation immediately.
//
// Build: scripts/build_vst3.sh examples/masterlimiter MasterLimiter
package main

import (
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramCeiling uint32 = iota
	paramRelease
	paramTruePeak
	paramLookahead
	paramReduction
	paramOutputPeak
)

type masterLimiterPlugin struct{}

func (masterLimiterPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.masterlimiter",
		Name:     "Master Limiter",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (masterLimiterPlugin) CreateProcessor() plugin.Processor {
	p := &masterLimiterProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.ThresholdParameter(paramCeiling, "Ceiling", -3, 0, -0.3).Build(),
		param.TimeParameter(paramRelease, "Release", 1, 100, 50).Build(),
		param.Choice(paramTruePeak, "True Peak", []param.ChoiceOption{
			{Value: 0, Name: "Off"},
			{Value: 1, Name: "On"},
		}).Default(1).Build(),
		param.TimeParameter(paramLookahead, "Lookahead", 0, 10, 5).Build(),
		param.New(paramReduction, "Gain Reduction").Range(0, 20).Default(0).Unit("dB").ReadOnly().Build(),
		param.OutputLevelMeter(paramOutputPeak, "Output Peak").Build(),
	)
	p.OnInitialize(func(sampleRate float64, _ int32) error {
		p.limiter = dynamics.NewLimiter(sampleRate)
		return nil
	})
	p.OnReset(func() { p.limiter.Reset() })
	return p
}

type masterLimiterProcessor struct {
	*frameworkplugin.BaseProcessor
	limiter  *dynamics.Limiter
	reported int32 // latency last announced to the host
	host     atomic.Pointer[hostRef]
}

type hostRef struct{ plugin.Host }

func (p *masterLimiterProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

// SetActive applies Lookahead when processing starts and announces the
// latency if it changed.
func (p *masterLimiterProcessor) SetActive(active bool) error {
	if active {
		p.limiter.SetLookahead(p.Parameters().Get(paramLookahead).GetPlainValue() / 1000)
		if latency := p.GetLatencySamples(); latency != p.reported {
			p.reported = latency
			if host := p.host.Load(); host != nil {
				_ = host.RestartComponent(plugin.RestartLatencyChanged)
			}
		}
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *masterLimiterProcessor) GetLatencySamples() int32 { return int32(p.limiter.Latency()) }

// GetTailSamples covers the lookahead delay and the release.
func (p *masterLimiterProcessor) GetTailSamples() int32 {
	release := p.Parameters().Get(paramRelease).GetPlainValue() / 1000 * p.SampleRate()
	return p.GetLatencySamples() + int32(math.Ceil(5*release))
}

func (p *masterLimiterProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	l := p.limiter
	l.SetThreshold(ctx.ParamPlain(paramCeiling))
	l.SetRelease(ctx.ParamPlain(paramRelease) / 1000)
	l.SetTruePeak(ctx.ParamPlain(paramTruePeak) >= 0.5)

	// Each output sample depends only on earlier input, so in-place
	// processing is safe.
	l.ProcessStereo(ctx.Input[0], ctx.Input[1], ctx.Output[0], ctx.Output[1])
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	peak := 0.0
	for _, out := range ctx.Output[:2] {
		for _, x := range out {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
	}
	params := p.Parameters()
	ctx.SetOutputParameter(paramReduction, params.Get(paramReduction).Normalize(l.GetGainReduction()))
	ctx.SetOutputParameter(paramOutputPeak, params.Get(paramOutputPeak).Normalize(20*math.Log10(math.Max(peak, 1e-3))))
}

func init() {
	plugin.Register(masterLimiterPlugin{})
}

func main() { standalone.Main() }
