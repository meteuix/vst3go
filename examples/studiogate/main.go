// Command studiogate is a stereo noise gate with hysteresis, hold, range,
// a high-pass filter on the detection signal and an optional external key
// from the sidechain input. Both channels open and close together. Read-only
// parameters report the gate state, gain reduction and output peak.
//
// Build: scripts/build_vst3.sh examples/studiogate StudioGate
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramThreshold uint32 = iota
	paramHysteresis
	paramAttack
	paramHold
	paramRelease
	paramRange
	paramKeyHPF
	paramKeyHPFOn
	paramExternalKey
	paramGateOpen
	paramReduction
	paramOutputPeak
)

var offOn = []param.ChoiceOption{{Value: 0, Name: "Off"}, {Value: 1, Name: "On"}}

type studioGatePlugin struct{}

func (studioGatePlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.studiogate",
		Name:     "Studio Gate",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDynamics,
	}
}

func (studioGatePlugin) CreateProcessor() plugin.Processor {
	p := &studioGateProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereoSidechain())}
	p.Parameters().Add(
		param.ThresholdParameter(paramThreshold, "Threshold", -80, 0, -40).Build(),
		param.New(paramHysteresis, "Hysteresis").Range(0, 20).Default(5).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
		param.TimeParameter(paramAttack, "Attack", 0, 100, 1).Build(),
		param.TimeParameter(paramHold, "Hold", 0, 500, 10).Build(),
		param.ReleaseParameter(paramRelease, "Release", 2000).Build(),
		param.ThresholdParameter(paramRange, "Range", -80, 0, -80).Build(),
		param.FrequencyParameter(paramKeyHPF, "Key HPF", 20, 500, 80).Build(),
		param.Choice(paramKeyHPFOn, "Key HPF On", offOn).Build(),
		param.Choice(paramExternalKey, "External Key", offOn).Build(),
		param.Choice(paramGateOpen, "Gate", []param.ChoiceOption{
			{Value: 0, Name: "Closed"},
			{Value: 1, Name: "Open"},
		}).ReadOnly().Build(),
		param.New(paramReduction, "Gain Reduction").Range(-80, 0).Default(0).Unit("dB").ReadOnly().Build(),
		param.OutputLevelMeter(paramOutputPeak, "Output Peak").Build(),
	)
	p.OnInitialize(func(sampleRate float64, _ int32) error {
		p.gate = dynamics.NewGate(sampleRate)
		return nil
	})
	p.OnReset(func() { p.gate.Reset() })
	return p
}

type studioGateProcessor struct {
	*frameworkplugin.BaseProcessor
	gate *dynamics.Gate
}

func (p *studioGateProcessor) GetTailSamples() int32 {
	ms := p.Parameters().Get(paramHold).GetPlainValue() + p.Parameters().Get(paramRelease).GetPlainValue()
	return int32(math.Ceil(ms / 1000 * p.SampleRate()))
}

func (p *studioGateProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	g := p.gate
	g.SetThreshold(ctx.ParamPlain(paramThreshold))
	g.SetHysteresis(ctx.ParamPlain(paramHysteresis))
	g.SetAttack(ctx.ParamPlain(paramAttack) / 1000)
	g.SetHold(ctx.ParamPlain(paramHold) / 1000)
	g.SetRelease(ctx.ParamPlain(paramRelease) / 1000)
	g.SetRange(ctx.ParamPlain(paramRange))
	g.SetSidechainFilter(ctx.ParamPlain(paramKeyHPFOn) >= 0.5, ctx.ParamPlain(paramKeyHPF))

	key := ctx.Input
	if side := ctx.Sidechain(); len(side) > 0 && ctx.ParamPlain(paramExternalKey) >= 0.5 {
		key = side
	}
	keyR := key[0]
	if len(key) > 1 {
		keyR = key[1]
	}
	// Each output sample depends only on the same input sample, so this
	// is safe when the host processes in place.
	g.ProcessStereoSidechain(ctx.Input[0], ctx.Input[1], key[0], keyR, ctx.Output[0], ctx.Output[1])
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	peak := 0.0
	for _, out := range ctx.Output[:2] {
		for _, x := range out {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
	}
	open := 0.0
	if g.IsOpen() {
		open = 1
	}
	params := p.Parameters()
	ctx.SetOutputParameter(paramGateOpen, open)
	ctx.SetOutputParameter(paramReduction, params.Get(paramReduction).Normalize(g.GetGainReduction()))
	ctx.SetOutputParameter(paramOutputPeak, params.Get(paramOutputPeak).Normalize(20*math.Log10(math.Max(peak, 1e-3))))
}

func init() {
	plugin.Register(studioGatePlugin{})
}

func main() { standalone.Main() }
