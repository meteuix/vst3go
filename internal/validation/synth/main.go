// Command synth is an example instrument: an 8-voice polyphonic synth with
// sample-accurate notes, velocity, pitch bend, mod-wheel vibrato, a release
// envelope, factory presets, per-note volume/tuning expressions, saw/square
// keyswitches, MIDI learn for the volume and a custom editor page
// (editor.html) on the vst3go SDK, with typed bindings in ui/.
//
// Build: scripts/build_vst3.sh internal/validation/synth Synth
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/midi"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramVolume uint32 = iota
	paramRelease
	paramShape // 0 = saw, 1 = square
)

const (
	numVoices = 8
	unitVoice = 1

	// Keyswitches below the playable range select the oscillator shape.
	keySaw    = 24 // C1
	keySquare = 25 // C#1
)

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
	release := param.ReleaseParameter(paramRelease, "Release", 2000).Build()
	release.UnitID = unitVoice
	p.Parameters().Add(
		param.GainParameter(paramVolume, "Volume").Default(-12).Build(),
		release,
		param.Choice(paramShape, "Shape", []param.ChoiceOption{{Value: 0, Name: "Saw"}, {Value: 1, Name: "Square"}}).Build(),
	)
	p.learnedCC.Store(-1)
	return p
}

type voice struct {
	active   bool
	released bool
	note     uint8
	noteID   int32
	velocity float32
	gain     float32 // note expression volume
	tuning   float64 // semitones: note-on tuning plus note expression
	phase    float64
	level    float32
}

type synthProcessor struct {
	*frameworkplugin.BaseProcessor
	voices   [numVoices]voice
	bend     float64 // semitones
	modWheel float64 // 0-1
	vibPhase float64

	host      atomic.Pointer[hostRef]
	learnedCC atomic.Int32 // CC mapped to Volume by MIDI learn, -1 for none
	playing   atomic.Int32 // sounding voices, for the editor
}

//go:embed editor.html
var editorHTML string

// Editor is the custom editor page.
func (p *synthProcessor) Editor() plugin.Editor {
	return plugin.Editor{HTML: editorHTML, Width: 640, Height: 400, MinWidth: 480, MinHeight: 320, MaxWidth: 1200, MaxHeight: 900}
}

// synthStatus is the reply to the editor's "status" message.
type synthStatus struct {
	Voices    int32 `json:"voices"`
	LearnedCC int32 `json:"learnedCC"`
}

// EditorMessage answers the editor page's status polls.
func (p *synthProcessor) EditorMessage(name string, data json.RawMessage) {
	if name != "status" {
		return
	}
	if host := p.host.Load(); host != nil {
		_ = host.PostEditorMessage("status", synthStatus{Voices: p.playing.Load(), LearnedCC: p.learnedCC.Load()})
	}
}

type hostRef struct{ plugin.Host }

// MIDIControllerChannels enables pitch bend and CC events on channel 1.
func (p *synthProcessor) MIDIControllerChannels() int { return 1 }

// ProgramList publishes factory presets in the host program menu.
func (p *synthProcessor) ProgramList() plugin.ProgramList {
	return plugin.ProgramList{Name: "Factory", Programs: []plugin.Program{
		{Name: "Init"},
		{Name: "Pluck", Values: map[uint32]float64{paramRelease: 80, paramVolume: -6}},
		{Name: "Square Pad", Values: map[uint32]float64{paramRelease: 1500, paramShape: 1}},
		{Name: "Keyswitched", PitchNames: map[int16]string{keySaw: "Saw", keySquare: "Square"}},
	}}
}

// Units groups the envelope parameters.
func (p *synthProcessor) Units() []plugin.Unit {
	return []plugin.Unit{{ID: unitVoice, ParentID: 0, Name: "Voice"}}
}

// NoteExpressions advertises per-note volume and tuning.
func (p *synthProcessor) NoteExpressions(busIndex int32, channel int16) []plugin.NoteExpressionInfo {
	if busIndex != 0 {
		return nil
	}
	return []plugin.NoteExpressionInfo{
		{TypeID: midi.NoteExpressionVolume, Title: "Volume", ShortTitle: "Vol", Default: 0.25, Flags: plugin.NoteExpressionAbsolute},
		{TypeID: midi.NoteExpressionTuning, Title: "Tuning", ShortTitle: "Tun", Units: "st", Default: 0.5, Flags: plugin.NoteExpressionBipolar,
			Format: func(v float64) string { return formatSemitones(tuningSemitones(v)) },
			Parse:  parseSemitones},
	}
}

// PhysicalUIMappings drives tuning from X movement and volume from pressure.
func (p *synthProcessor) PhysicalUIMappings(busIndex int32, channel int16) []plugin.PhysicalUIMapping {
	return []plugin.PhysicalUIMapping{
		{PhysicalUI: plugin.PhysicalUIXMovement, TypeID: midi.NoteExpressionTuning},
		{PhysicalUI: plugin.PhysicalUIPressure, TypeID: midi.NoteExpressionVolume},
	}
}

// Keyswitches publishes the shape keyswitches for host expression maps.
func (p *synthProcessor) Keyswitches(busIndex int32, channel int16) []plugin.KeyswitchInfo {
	if busIndex != 0 {
		return nil
	}
	return []plugin.KeyswitchInfo{
		{Type: plugin.KeyswitchNoteOn, Title: "Saw", ShortTitle: "Saw", Min: keySaw, Max: keySaw, KeyRemapped: -1},
		{Type: plugin.KeyswitchNoteOn, Title: "Square", ShortTitle: "Sq", Min: keySquare, Max: keySquare, KeyRemapped: -1},
	}
}

// SetHost keeps the host handle for MIDI learn notifications.
func (p *synthProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

// LearnMIDIController maps the first moved controller to Volume.
func (p *synthProcessor) LearnMIDIController(busIndex int32, channel, controller int16) bool {
	if busIndex != 0 || channel != 0 || controller < 0 || controller > 127 || controller == int16(midi.CCModWheel) {
		return false
	}
	if p.learnedCC.Swap(int32(controller)) != int32(controller) {
		if host := p.host.Load(); host != nil {
			_ = host.RestartComponent(plugin.RestartMidiCCAssignmentChange)
		}
	}
	return true
}

// MIDIControllerParameter routes the learned controller to Volume.
func (p *synthProcessor) MIDIControllerParameter(busIndex int32, channel, controller int16) (uint32, bool) {
	if busIndex == 0 && channel == 0 && int32(controller) == p.learnedCC.Load() {
		return paramVolume, true
	}
	return 0, false
}

// tuningSemitones maps the normalized tuning expression to semitones
// (VST3: 0.5 is no change, the range is +/-120 semitones).
func tuningSemitones(v float64) float64 { return 240 * (v - 0.5) }

// parseSemitones converts "+60.00 st" back to a normalized tuning value.
func parseSemitones(text string) (float64, error) {
	var st float64
	if _, err := fmt.Sscanf(text, "%g", &st); err != nil {
		return 0, err
	}
	return st/240 + 0.5, nil
}

func formatSemitones(st float64) string {
	return fmt.Sprintf("%+.2f st", st)
}

func (p *synthProcessor) SetActive(active bool) error {
	if !active {
		p.voices = [numVoices]voice{}
		p.playing.Store(0)
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
	releaseSec := max(ctx.ParamPlain(paramRelease)/1000, 0.001)
	releaseStep := float32(1 / (releaseSec * sampleRate))
	square := ctx.Param(paramShape) >= 0.5

	events := ctx.GetAllInputEvents()
	next := 0
	offset := ctx.BlockOffset()
	left := ctx.Output[0]

	for i := 0; i < n; i++ {
		for next < len(events) && int(events[next].Offset)-offset <= i {
			p.handleEvent(ctx, &events[next])
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
			semis := float64(vc.note) - 69 + vc.tuning + p.bend + vibrato
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
			osc := float32(2*vc.phase - 1)
			if square {
				osc = 1
				if vc.phase >= 0.5 {
					osc = -1
				}
			}
			sum += osc * vc.velocity * vc.gain * vc.level
		}
		left[i] = sum * volume * 0.25
	}
	for i := next; i < len(events); i++ {
		p.handleEvent(ctx, &events[i])
	}
	for ch := 1; ch < len(ctx.Output); ch++ {
		copy(ctx.Output[ch], left)
	}
	p.playing.Store(p.countVoices())
	if !p.anyVoice() {
		ctx.SetOutputSilenceFlags(0, (1<<len(ctx.Output))-1)
	}
}

func (p *synthProcessor) countVoices() int32 {
	var n int32
	for v := range p.voices {
		if p.voices[v].active {
			n++
		}
	}
	return n
}

func (p *synthProcessor) anyVoice() bool {
	for v := range p.voices {
		if p.voices[v].active {
			return true
		}
	}
	return false
}

func (p *synthProcessor) handleEvent(ctx *process.Context, e *midi.Event) {
	switch e.Type {
	case midi.EventTypeNoteOn:
		if e.Note == keySaw || e.Note == keySquare {
			// Keyswitch: switch the shape like a host edit would.
			if shape, ok := p.Parameters().GetOK(paramShape); ok {
				shape.SetNormalized(float64(e.Note - keySaw))
				ctx.SetOutputParameter(paramShape, shape.GetNormalized())
			}
			return
		}
		if e.Velocity == 0 {
			p.release(e.Note, e.NoteID)
			return
		}
		vc := p.allocate()
		*vc = voice{active: true, note: e.Note, noteID: e.NoteID, velocity: e.Velocity, gain: 1, tuning: float64(e.Tuning) / 100, level: 1}
	case midi.EventTypeNoteOff:
		p.release(e.Note, e.NoteID)
	case midi.EventTypeNoteExpression:
		for v := range p.voices {
			vc := &p.voices[v]
			if !vc.active || vc.noteID != e.NoteID || e.NoteID == midi.NoteIDNone {
				continue
			}
			switch e.ExpressionType {
			case midi.NoteExpressionVolume:
				vc.gain = float32(4 * e.Value) // 0.25 = unity
			case midi.NoteExpressionTuning:
				vc.tuning = tuningSemitones(e.Value)
			}
		}
	case midi.EventTypePitchBend:
		p.bend = 2 * e.Value
	case midi.EventTypeControlChange:
		switch e.Controller {
		case midi.CCModWheel:
			p.modWheel = e.Value
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

func main() { standalone.Main() }
