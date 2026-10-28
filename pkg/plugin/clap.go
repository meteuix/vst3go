package plugin

import (
	"math"
	"strings"

	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/midi"
)

// Format-independent parts of the CLAP front end (wrapper_clap.go).

// CLAPFeaturesPlugin overrides the CLAP feature list derived from
// Info.Category, e.g. to add "stereo" or "mastering".
type CLAPFeaturesPlugin interface {
	Plugin
	CLAPFeatures() []string
}

// clapFeatures maps VST3 sub-categories to CLAP plugin features; the main
// feature (instrument, audio-effect, note-effect, analyzer) comes first.
func clapFeatures(p Plugin, info frameworkplugin.Info) []string {
	if custom, ok := p.(CLAPFeaturesPlugin); ok {
		return custom.CLAPFeatures()
	}
	parts := strings.Split(info.SubCategories(), "|")
	var features []string
	add := func(f string) {
		for _, existing := range features {
			if existing == f {
				return
			}
		}
		features = append(features, f)
	}
	switch {
	case strings.HasPrefix(info.SubCategories(), frameworkplugin.CategoryFxInstrument):
		add("note-effect")
	case parts[0] == "Instrument":
		add("instrument")
	case parts[0] == "Analyzer":
		add("analyzer")
	default:
		add("audio-effect")
	}
	sub := map[string]string{
		"Analyzer":    "analyzer",
		"Delay":       "delay",
		"Distortion":  "distortion",
		"Dynamics":    "compressor",
		"EQ":          "equalizer",
		"Filter":      "filter",
		"Mastering":   "mastering",
		"Pitch Shift": "pitch-shifter",
		"Restoration": "restoration",
		"Reverb":      "reverb",
		"Surround":    "surround",
		"Tools":       "utility",
		"Spatial":     "mixing",
		"Modulation":  "chorus",
		"Drum":        "drum-machine",
		"Sampler":     "sampler",
		"Synth":       "synthesizer",
	}
	for _, part := range parts[1:] {
		if f, ok := sub[part]; ok {
			add(f)
		}
	}
	return features
}

// CLAP parameters use plain values: continuous parameters range over
// Min..Max, stepped ones over the step index 0..StepCount (their display
// text comes from the formatter).

func clapParamRange(p *param.Parameter) (lo, hi float64) {
	switch {
	case p.StepCount > 0:
		return 0, float64(p.StepCount)
	case p.Max <= p.Min:
		return 0, 1
	default:
		return p.Min, p.Max
	}
}

func clapPlain(p *param.Parameter, normalized float64) float64 {
	normalized = clampUnit(normalized)
	switch {
	case p.StepCount > 0:
		steps := float64(p.StepCount)
		return math.Min(steps, math.Floor(normalized*(steps+1)))
	case p.Max <= p.Min:
		return normalized
	default:
		return p.Min + normalized*(p.Max-p.Min)
	}
}

func clapNormalized(p *param.Parameter, plain float64) float64 {
	if math.IsNaN(plain) {
		return clampUnit(p.DefaultValue)
	}
	switch {
	case p.StepCount > 0:
		return clampUnit(math.Round(plain) / float64(p.StepCount))
	case p.Max <= p.Min:
		return clampUnit(plain)
	default:
		return p.Normalize(plain)
	}
}

// clapParamText formats a plain value with the parameter unit.
func clapParamText(p *param.Parameter, plain float64) string {
	text := p.FormatValue(clapNormalized(p, plain))
	if p.Unit != "" && !strings.HasSuffix(text, p.Unit) {
		text += " " + p.Unit
	}
	return text
}

// clapParamValue parses display text (with or without the unit) to a plain value.
func clapParamValue(p *param.Parameter, text string) (float64, error) {
	text = strings.TrimSpace(text)
	if p.Unit != "" {
		text = strings.TrimSpace(strings.TrimSuffix(text, p.Unit))
	}
	normalized, err := p.ParseValue(text)
	if err != nil {
		return 0, err
	}
	return clapPlain(p, normalized), nil
}

// CLAP parameter flags (ext/params.h).
const (
	clapParamIsStepped     = 1 << 0
	clapParamIsPeriodic    = 1 << 1
	clapParamIsHidden      = 1 << 2
	clapParamIsReadonly    = 1 << 3
	clapParamIsBypass      = 1 << 4
	clapParamIsAutomatable = 1 << 5
	clapParamIsEnum        = 1 << 16
)

func clapParamFlags(p *param.Parameter) uint32 {
	var flags uint32
	if p.StepCount > 0 {
		flags |= clapParamIsStepped
		if p.Flags&param.IsList != 0 {
			flags |= clapParamIsEnum
		}
	}
	if p.Flags&param.IsWrapAround != 0 {
		flags |= clapParamIsPeriodic
	}
	if p.Flags&param.IsHidden != 0 {
		flags |= clapParamIsHidden
	}
	if p.Flags&param.IsReadOnly != 0 {
		flags |= clapParamIsReadonly
	} else if p.Flags&param.CanAutomate != 0 {
		flags |= clapParamIsAutomatable
	}
	if p.Flags&param.IsBypass != 0 && p.StepCount > 0 {
		flags |= clapParamIsBypass
	}
	return flags
}

// CLAP note expressions (events.h).
const (
	clapExprVolume = iota
	clapExprPan
	clapExprTuning
	clapExprVibrato
	clapExprExpression
	clapExprBrightness
	clapExprPressure
)

// clapExpressionToVST3 converts a CLAP note expression to the VST3 type
// and normalized value of a midi.Event. Pressure has no VST3 expression
// type and becomes poly pressure (ok and pressure are both reported).
func clapExpressionToVST3(id int32, value float64) (typeID uint32, normalized float64, pressure, ok bool) {
	switch id {
	case clapExprVolume: // linear gain 0..4
		return midi.NoteExpressionVolume, clampUnit(value / 4), false, true
	case clapExprPan:
		return midi.NoteExpressionPan, clampUnit(value), false, true
	case clapExprTuning: // semitones -120..120
		return midi.NoteExpressionTuning, clampUnit(0.5 + value/240), false, true
	case clapExprVibrato:
		return midi.NoteExpressionVibrato, clampUnit(value), false, true
	case clapExprExpression:
		return midi.NoteExpressionExpression, clampUnit(value), false, true
	case clapExprBrightness:
		return midi.NoteExpressionBrightness, clampUnit(value), false, true
	case clapExprPressure:
		return 0, clampUnit(value), true, true
	}
	return 0, 0, false, false
}

// vst3ExpressionToCLAP is the inverse of clapExpressionToVST3.
func vst3ExpressionToCLAP(typeID uint32, normalized float64) (id int32, value float64, ok bool) {
	normalized = clampUnit(normalized)
	switch typeID {
	case midi.NoteExpressionVolume:
		return clapExprVolume, normalized * 4, true
	case midi.NoteExpressionPan:
		return clapExprPan, normalized, true
	case midi.NoteExpressionTuning:
		return clapExprTuning, (normalized - 0.5) * 240, true
	case midi.NoteExpressionVibrato:
		return clapExprVibrato, normalized, true
	case midi.NoteExpressionExpression:
		return clapExprExpression, normalized, true
	case midi.NoteExpressionBrightness:
		return clapExprBrightness, normalized, true
	}
	return 0, 0, false
}

// clapNotes tracks sounding notes so that CLAP wildcards (key, channel or
// note ID -1) and raw MIDI notes resolve to the VST3-style note IDs that
// processors see. Notes without a host ID get a synthetic one.
type clapNotes struct {
	ids [16][128]int32
	on  [16][128]bool
}

const clapSyntheticNoteID = 1 << 30

func (n *clapNotes) reset() { *n = clapNotes{} }

// noteOn records a note and returns its note ID.
func (n *clapNotes) noteOn(channel, key, noteID int32) int32 {
	if noteID < 0 {
		noteID = clapSyntheticNoteID | channel<<7 | key
	}
	n.ids[channel][key] = noteID
	n.on[channel][key] = true
	return noteID
}

// match calls visit for every sounding note matching the (possibly
// wildcard) channel, key and note ID.
func (n *clapNotes) match(channel, key, noteID int32, visit func(channel, key, noteID int32)) {
	for ch := int32(0); ch < 16; ch++ {
		if channel >= 0 && ch != channel {
			continue
		}
		for k := int32(0); k < 128; k++ {
			if key >= 0 && k != key {
				continue
			}
			if n.on[ch][k] && (noteID < 0 || n.ids[ch][k] == noteID) {
				visit(ch, k, n.ids[ch][k])
			}
		}
	}
}

func (n *clapNotes) noteOff(channel, key int32) {
	n.on[channel][key] = false
}

// clapRawMIDI converts a 3-byte MIDI message. Notes go through the note
// tracker; controllers are returned as (controller, normalized value) for
// the MIDI mapping (MIDIControllerAfterTouch, MIDIControllerPitchBend for
// pressure and bend); program changes return program >= 0.
type clapMIDIMessage struct {
	event      midi.Event
	isEvent    bool
	controller int16
	value      float64
	isCtrl     bool
	program    int32
}

func clapRawMIDI(notes *clapNotes, data [3]byte, offset int32) clapMIDIMessage {
	status := data[0] & 0xF0
	channel := data[0] & 0x0F
	d1, d2 := data[1]&0x7F, data[2]&0x7F
	msg := clapMIDIMessage{program: -1}
	switch status {
	case 0x90, 0x80:
		if status == 0x90 && d2 > 0 {
			id := notes.noteOn(int32(channel), int32(d1), -1)
			msg.event = midi.NoteOn(channel, d1, float32(d2)/127).At(offset)
			msg.event.NoteID = id
		} else {
			id := int32(midi.NoteIDNone)
			if notes.on[channel][d1] {
				id = notes.ids[channel][d1]
				notes.noteOff(int32(channel), int32(d1))
			}
			velocity := float32(0)
			if status == 0x80 {
				velocity = float32(d2) / 127
			}
			msg.event = midi.NoteOff(channel, d1, velocity).At(offset)
			msg.event.NoteID = id
		}
		msg.isEvent = true
	case 0xA0:
		msg.event = midi.PolyPressure(channel, d1, float64(d2)/127).At(offset)
		if notes.on[channel][d1] {
			msg.event.NoteID = notes.ids[channel][d1]
		}
		msg.isEvent = true
	case 0xB0:
		msg.controller, msg.value, msg.isCtrl = int16(d1), float64(d2)/127, true
	case 0xD0:
		msg.controller, msg.value, msg.isCtrl = MIDIControllerAfterTouch, float64(d1)/127, true
	case 0xE0:
		msg.controller, msg.value, msg.isCtrl = MIDIControllerPitchBend, float64(int(d2)<<7|int(d1))/16383, true
	case 0xC0:
		msg.program = int32(d1)
	}
	msg.event.Channel = channel
	return msg
}

// clapMIDIBytes converts an output event to a 3-byte MIDI message.
func clapMIDIBytes(event *midi.Event) (data [3]byte, ok bool) {
	channel := event.Channel & 0x0F
	switch event.Type {
	case midi.EventTypeControlChange:
		return [3]byte{0xB0 | channel, event.Controller & 0x7F, event.Value7()}, true
	case midi.EventTypeChannelPressure:
		return [3]byte{0xD0 | channel, event.Value7(), 0}, true
	case midi.EventTypePitchBend:
		value := int(event.PitchBend14()) + 8192
		return [3]byte{0xE0 | channel, byte(value & 0x7F), byte(value >> 7 & 0x7F)}, true
	case midi.EventTypeProgramChange:
		return [3]byte{0xC0 | channel, event.Program & 0x7F, 0}, true
	}
	return data, false
}

// clapEdit is a plugin-side parameter edit waiting to be reported to the
// host in the next process or flush call.
type clapEdit struct {
	kind  uint8 // editBegin, editPerform, editEnd
	id    uint32
	plain float64
}
