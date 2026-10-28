// Command gain is an example audio effect: gain with bypass and an output
// level meter. It runs in mono or stereo, whichever the host asks for.
//
// Build: scripts/build_vst3.sh internal/validation/gain Gain
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
)

const (
	paramGain uint32 = iota
	paramBypass
	paramLevel
)

type gainPlugin struct{}

func (gainPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.gain",
		Name:     "VST3Go Gain",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxTools,
	}
}

func (gainPlugin) CreateProcessor() plugin.Processor {
	p := &gainProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.GainParameter(paramGain, "Gain").Build(),
		param.BypassParameter(paramBypass, "Bypass").Build(),
		param.OutputLevelMeter(paramLevel, "Output Level").Build(),
	)
	return p
}

type gainProcessor struct {
	*frameworkplugin.BaseProcessor
}

// AcceptBusArrangements accepts any symmetric mono or stereo layout.
func (p *gainProcessor) AcceptBusArrangements(inputs, outputs []bus.SpeakerArrangement) bool {
	if len(inputs) != 1 || len(outputs) != 1 || inputs[0] != outputs[0] {
		return false
	}
	return inputs[0] == bus.ArrangementMono || inputs[0] == bus.ArrangementStereo
}

func (p *gainProcessor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(paramBypass) >= 0.5 {
		ctx.PassThrough()
		ctx.SetOutputSilenceFlags(0, ctx.InputSilenceFlags(0))
		return
	}

	gain := float32(0) // the -80 dB floor is -inf
	if db := ctx.ParamPlain(paramGain); db > -80 {
		gain = float32(math.Pow(10, db/20))
	}
	peak := float32(0)
	for ch := range ctx.Output {
		out := ctx.Output[ch]
		if ch >= len(ctx.Input) {
			clear(out)
			continue
		}
		for i, x := range ctx.Input[ch] {
			y := x * gain
			out[i] = y
			if a := float32(math.Abs(float64(y))); a > peak {
				peak = a
			}
		}
	}
	ctx.SetOutputSilenceFlags(0, ctx.InputSilenceFlags(0))

	// Meter in dB, mapped onto the meter parameter's range.
	db := -60.0
	if peak > 0 {
		db = math.Max(-60, 20*math.Log10(float64(peak)))
	}
	if meter, ok := p.Parameters().GetOK(paramLevel); ok {
		ctx.SetOutputParameter(paramLevel, meter.Normalize(db))
	}
}

func init() {
	plugin.Register(gainPlugin{})
}

func main() {}
