// Command synth is an example instrument: an 8-voice polyphonic saw synth
// with sample-accurate notes, velocity, pitch bend, mod-wheel vibrato and a
// release envelope.
//
// Build: scripts/build_vst3.sh internal/validation/synth Synth
package main

import (
	"math"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/midi"
	"github.com/th-release/vst3go/pkg/plugin"
)

const (
	paramVolume uint32 = iota
	paramRelease
)

const numVoices = 8

type synthPlugin struct{}

func (synthPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.synth",
		Name:     "VST3Go Synth",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryInstrumentSynth,
	}
}

func (synthPlugin) CreateProcessor() plugin.Processor {
	p := &synthProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewGenerator())}
	p.Parameters().Add(
		param.GainParameter(paramVolume, "Volume").Default(-12).Build(),
		param.ReleaseParameter(paramRelease, "Release", 2000).Build(),
	)
	return p
}

type voice struct {
	active   bool
	released bool
	note     uint8
	noteID   int32
	velocity float32
	phase    float64
	level    float32
}

type synthProcessor struct {
	*frameworkplugin.BaseProcessor
	voices   [numVoices]voice
	bend     float64 // semitones
	modWheel float64 // 0-1
	vibPhase float64
}

// MIDIControllerChannels enables pitch bend and CC events on channel 1.
func (p *synthProcessor) MIDIControllerChannels() int { return 1 }

func (p *synthProcessor) SetActive(active bool) error {
	if !active {
		p.voices = [numVoices]voice{}
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *synthProcessor) GetTailSamples() int32 {
	return int32(p.SampleRate() * 2) // longest release
}

func (p *synthProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) == 0 || n == 0 {
		return
	}
	ctx.Clear()

	sampleRate := p.SampleRate()
	volume := float32(math.Pow(10, ctx.ParamPlain(paramVolume)/20))
	releaseSec := math.Max(ctx.ParamPlain(paramRelease)/1000, 0.001)
	releaseStep := float32(1 / (releaseSec * sampleRate))

	events := ctx.GetAllInputEvents()
	next := 0
	offset := ctx.BlockOffset()
	left := ctx.Output[0]

	for i := 0; i < n; i++ {
		for next < len(events) && int(events[next].SampleOffset())-offset <= i {
			p.handleEvent(events[next])
			next++
		}

		vibrato := p.modWheel * 0.5 * math.Sin(2*math.Pi*p.vibPhase)
		p.vibPhase += 5.5 / sampleRate
		if p.vibPhase >= 1 {
			p.vibPhase--
		}

		var sum float32
		for v := range p.voices {
			vc := &p.voices[v]
			if !vc.active {
				continue
			}
			semis := float64(vc.note) - 69 + p.bend + vibrato
			vc.phase += 440 * math.Exp2(semis/12) / sampleRate
			if vc.phase >= 1 {
				vc.phase -= math.Floor(vc.phase)
			}
			if vc.released {
				vc.level -= releaseStep
				if vc.level <= 0 {
					vc.active = false
					continue
				}
			}
			sum += float32(2*vc.phase-1) * vc.velocity * vc.level
		}
		left[i] = sum * volume * 0.25
	}
	for _, event := range events[next:] {
		p.handleEvent(event)
	}
	for ch := 1; ch < len(ctx.Output); ch++ {
		copy(ctx.Output[ch], left)
	}
	if !p.anyVoice() {
		ctx.SetOutputSilenceFlags(0, (1<<len(ctx.Output))-1)
	}
}

func (p *synthProcessor) anyVoice() bool {
	for v := range p.voices {
		if p.voices[v].active {
			return true
		}
	}
	return false
}

func (p *synthProcessor) handleEvent(event midi.Event) {
	switch e := event.(type) {
	case midi.NoteOnEvent:
		if e.Velocity == 0 && e.VelocityNorm == 0 {
			p.release(e.NoteNumber, e.NoteID)
			return
		}
		vc := p.allocate()
		*vc = voice{active: true, note: e.NoteNumber, noteID: e.NoteID, velocity: e.VelocityNorm, level: 1}
	case midi.NoteOffEvent:
		p.release(e.NoteNumber, e.NoteID)
	case midi.PitchBendEvent:
		p.bend = 2 * float64(e.Value) / 8192
	case midi.ControlChangeEvent:
		switch e.Controller {
		case midi.CCModWheel:
			p.modWheel = float64(e.Value) / 127
		case midi.CCAllNotesOff, midi.CCAllSoundOff:
			for v := range p.voices {
				p.voices[v].released = true
			}
		}
	}
}

func (p *synthProcessor) allocate() *voice {
	for v := range p.voices {
		if !p.voices[v].active {
			return &p.voices[v]
		}
	}
	// Steal the quietest voice.
	quietest := 0
	for v := range p.voices {
		if p.voices[v].level < p.voices[quietest].level {
			quietest = v
		}
	}
	return &p.voices[quietest]
}

func (p *synthProcessor) release(note uint8, noteID int32) {
	for v := range p.voices {
		vc := &p.voices[v]
		if !vc.active || vc.released {
			continue
		}
		// Match on the host note ID when both sides carry one, else on pitch.
		if noteID != midi.NoteIDNone && vc.noteID != midi.NoteIDNone {
			if vc.noteID == noteID {
				vc.released = true
			}
			continue
		}
		if vc.note == note {
			vc.released = true
		}
	}
}

func init() {
	plugin.Register(synthPlugin{})
}

func main() {}
