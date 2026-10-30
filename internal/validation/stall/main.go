// Command stall checks the deadline guard (plugin.DeadlineProcessor): a
// stereo effect with a MIDI input, processed on the guard's thread with
// half of each block's duration as its deadline. Its output is the input
// times one plus the number of note events received so far, so a host can
// count them in any format. While Stall is above zero it sleeps that many
// milliseconds per block, as a garbage collection pause would stall it.
// hostcheck and clapcheck stall it and check that process returns by the
// deadline with silence and that the note events of the blocks it skipped
// arrive with the next one.
//
// Build: scripts/build_vst3.sh internal/validation/stall stall
package main

import (
	"time"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const paramStall uint32 = 0

type stallPlugin struct{}

func (stallPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.stall",
		Name:     "VST3Go Stall",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFx,
	}
}

func (stallPlugin) CreateProcessor() plugin.Processor {
	buses := bus.NewBuilder().
		WithStereoInput("Input").
		WithStereoOutput("Output").
		WithEventInput("MIDI In").
		MustBuild()
	p := &stallProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(buses)}
	p.Parameters().Add(
		param.New(paramStall, "Stall").Range(0, 100).Default(0).Unit("ms").Build(),
	)
	p.SetProcessDeadline(0.5)
	p.OnReset(func() { p.notes = 0 })
	return p
}

type stallProcessor struct {
	*frameworkplugin.BaseProcessor
	notes int
}

func (p *stallProcessor) ProcessAudio(ctx *process.Context) {
	for _, e := range ctx.GetAllInputEvents() {
		if e.IsNote() {
			p.notes++
		}
	}
	if ms := ctx.ParamPlain(paramStall); ms > 0 {
		time.Sleep(time.Duration(ms * float64(time.Millisecond)))
	}
	gain := float32(1 + p.notes)
	for ch, out := range ctx.Output {
		if ch >= len(ctx.Input) {
			clear(out)
			continue
		}
		for i, x := range ctx.Input[ch] {
			out[i] = x * gain
		}
	}
}

func init() {
	plugin.Register(stallPlugin{})
}

func main() { standalone.Main() }
