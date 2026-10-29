// Command simplesynth is a 16-voice sine synthesizer: an ADSR envelope per
// voice, sample-accurate notes, sustain pedal and voice stealing through the
// framework voice allocator (pkg/framework/voice).
//
// Build: scripts/build_vst3.sh examples/simplesynth SimpleSynth
package main

import (
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/framework/voice"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramAttack uint32 = iota
	paramDecay
	paramSustain
	paramRelease
	paramVolume
)

const numVoices = 16

type synthPlugin struct{}

func (synthPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.simplesynth",
		Name:     "Simple Synth",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryInstrumentSynth,
	}
}

func (synthPlugin) CreateProcessor() plugin.Processor {
	p := &synthProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewGenerator())}
	p.Parameters().Add(
		param.New(paramAttack, "Attack").Range(0.001, 2).Default(0.01).Unit("s").Build(),
		param.New(paramDecay, "Decay").Range(0.001, 2).Default(0.1).Unit("s").Build(),
		param.New(paramSustain, "Sustain").Range(0, 100).Default(70).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.New(paramRelease, "Release").Range(0.001, 5).Default(0.3).Unit("s").Build(),
		param.New(paramVolume, "Volume").Range(0, 100).Default(80).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Smooth(0.01).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type synthProcessor struct {
	*frameworkplugin.BaseProcessor

	voices    [numVoices]*synthVoice
	allocator *voice.Allocator
	scratch   []float32

	// Envelope settings the voices were last configured with.
	attack, decay, sustain, release float64
}

func (p *synthProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	vs := make([]voice.Voice, numVoices)
	for i := range p.voices {
		p.voices[i] = newSynthVoice(sampleRate)
		vs[i] = p.voices[i]
	}
	p.allocator = voice.NewAllocator(vs)
	p.allocator.SetStealingMode(voice.StealOldest)
	p.scratch = make([]float32, maxBlockSize)
	p.attack = -1 // configure the voices on the first block
	return nil
}

func (p *synthProcessor) reset() {
	if p.allocator != nil {
		p.allocator.Reset()
	}
}

// MIDIControllerChannels delivers controller events (sustain pedal, All
// Notes Off) on MIDI channel 1; VST3 hosts send them as mapped parameters.
func (p *synthProcessor) MIDIControllerChannels() int { return 1 }

// GetTailSamples covers the longest release.
func (p *synthProcessor) GetTailSamples() int32 {
	return int32(5 * p.SampleRate())
}

func (p *synthProcessor) updateEnvelopes(ctx *process.Context) {
	attack := ctx.ParamPlain(paramAttack)
	decay := ctx.ParamPlain(paramDecay)
	sustain := ctx.ParamPlain(paramSustain) / 100
	release := ctx.ParamPlain(paramRelease)
	if attack == p.attack && decay == p.decay && sustain == p.sustain && release == p.release {
		return
	}
	p.attack, p.decay, p.sustain, p.release = attack, decay, sustain, release
	for _, v := range p.voices {
		v.SetADSR(attack, decay, sustain, release)
	}
}

func (p *synthProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) == 0 || n == 0 {
		return
	}
	ctx.Clear()
	p.updateEnvelopes(ctx)

	// Render the voices between events so every note starts and stops on
	// its sample offset.
	left := ctx.Output[0]
	events := ctx.GetAllInputEvents()
	offset := ctx.BlockOffset()
	next := 0
	for start := 0; start < n; {
		for next < len(events) && int(events[next].Offset)-offset <= start {
			p.allocator.ProcessEvent(events[next])
			next++
		}
		end := n
		if next < len(events) {
			end = min(int(events[next].Offset)-offset, n)
		}
		p.render(left[start:end])
		start = end
	}
	for ; next < len(events); next++ {
		p.allocator.ProcessEvent(events[next])
	}

	volume := ctx.Smoothed(paramVolume)
	for i := range left {
		left[i] *= float32(volume.Next() / 100)
	}
	for ch := 1; ch < len(ctx.Output); ch++ {
		copy(ctx.Output[ch], left)
	}
	if p.allocator.GetActiveVoiceCount() == 0 && silent(left) {
		ctx.SetOutputSilenceFlags(0, (1<<len(ctx.Output))-1)
	}
}

// render mixes every sounding voice into out.
func (p *synthProcessor) render(out []float32) {
	if len(out) == 0 {
		return
	}
	buf := p.scratch[:len(out)]
	for _, v := range p.voices {
		if !v.IsActive() {
			continue
		}
		v.Process(buf)
		for i, x := range buf {
			out[i] += x
		}
	}
}

func silent(x []float32) bool {
	for _, v := range x {
		if v != 0 {
			return false
		}
	}
	return true
}

func init() {
	plugin.Register(synthPlugin{})
}

func main() { standalone.Main() }
