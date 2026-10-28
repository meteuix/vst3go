// Command midifx is an example MIDI effect: it transposes notes and passes
// other events through, with no audio buses.
//
// Build: scripts/build_vst3.sh internal/validation/midifx MidiFx
package main

import (
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/midi"
	"github.com/th-release/vst3go/pkg/plugin"
)

const paramTranspose uint32 = 0

type midiFxPlugin struct{}

func (midiFxPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.midifx",
		Name:     "VST3Go Transpose",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxInstrument,
	}
}

func (midiFxPlugin) CreateProcessor() plugin.Processor {
	p := &midiFxProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewMIDIEffect())}
	p.Parameters().Add(
		param.New(paramTranspose, "Transpose").Range(-24, 24).Default(0).Steps(48).Unit("st").Build(),
	)
	return p
}

type midiFxProcessor struct {
	*frameworkplugin.BaseProcessor
	// sounding maps an input pitch to the transposed pitch it started, so
	// note-offs still match after the transpose amount changes.
	sounding [16][128]int16
}

// MIDIControllerChannels forwards CC, pitch bend and aftertouch too.
func (p *midiFxProcessor) MIDIControllerChannels() int { return 16 }

func (p *midiFxProcessor) Initialize(sampleRate float64, maxBlockSize int32) error {
	for ch := range p.sounding {
		for n := range p.sounding[ch] {
			p.sounding[ch][n] = -1
		}
	}
	return p.BaseProcessor.Initialize(sampleRate, maxBlockSize)
}

func (p *midiFxProcessor) ProcessAudio(ctx *process.Context) {
	shift := int(ctx.ParamPlain(paramTranspose))
	for _, event := range ctx.GetAllInputEvents() {
		switch e := event.(type) {
		case midi.NoteOnEvent:
			pitch := int(e.NoteNumber) + shift
			if pitch < 0 || pitch > 127 {
				continue
			}
			p.sounding[e.EventChannel&15][e.NoteNumber] = int16(pitch)
			e.NoteNumber = uint8(pitch)
			ctx.AddOutputEvent(e)
		case midi.NoteOffEvent:
			pitch := p.sounding[e.EventChannel&15][e.NoteNumber]
			if pitch < 0 {
				continue
			}
			p.sounding[e.EventChannel&15][e.NoteNumber] = -1
			e.NoteNumber = uint8(pitch)
			ctx.AddOutputEvent(e)
		default:
			ctx.AddOutputEvent(event)
		}
	}
}

func init() {
	plugin.Register(midiFxPlugin{})
}

func main() {}
