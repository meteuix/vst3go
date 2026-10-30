// Command gain is an example audio effect: gain with bypass and an output
// level meter. It runs in mono or stereo, whichever the host asks for, in
// 32-bit or 64-bit, and follows the host channel name.
//
// Build: scripts/build_vst3.sh internal/validation/gain Gain
package main

import (
	"math"
	"sync"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
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

	mu      sync.Mutex
	channel plugin.ChannelInfo
}

// SupportsDoublePrecision enables 64-bit processing (ctx.Output64).
func (p *gainProcessor) SupportsDoublePrecision() bool { return true }

// SetChannelInfo receives the mixer channel the plugin is inserted on.
func (p *gainProcessor) SetChannelInfo(info plugin.ChannelInfo) {
	p.mu.Lock()
	p.channel = info
	p.mu.Unlock()
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

	gain := 0.0 // the -80 dB floor is -inf
	if db := ctx.ParamPlain(paramGain); db > -80 {
		gain = math.Pow(10, db/20)
	}
	var peak float64
	if ctx.Is64Bit() {
		peak = applyGain(ctx.Input64, ctx.Output64, gain)
	} else {
		peak = applyGain(ctx.Input, ctx.Output, float32(gain))
	}
	ctx.SetOutputSilenceFlags(0, ctx.InputSilenceFlags(0))

	// Meter in dB, mapped onto the meter parameter's range.
	db := -60.0
	if peak > 0 {
		db = max(-60, 20*math.Log10(float64(peak)))
	}
	if meter, ok := p.Parameters().GetOK(paramLevel); ok {
		ctx.SetOutputParameter(paramLevel, meter.Normalize(db))
	}
}

// applyGain scales in into out and returns the output peak.
func applyGain[T float32 | float64](in, out [][]T, gain T) float64 {
	var peak float64
	for ch := range out {
		if ch >= len(in) {
			clear(out[ch])
			continue
		}
		for i, x := range in[ch] {
			y := x * gain
			out[ch][i] = y
			peak = max(peak, math.Abs(float64(y)))
		}
	}
	return peak
}

func init() {
	plugin.Register(gainPlugin{})
}

func main() { standalone.Main() }
