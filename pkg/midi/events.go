// Package midi provides the note and controller event type exchanged between
// the VST3 runtime and plugin processors, plus helpers for MIDI values.
package midi

import (
	"fmt"
	"math"
	"unicode/utf16"
	"unicode/utf8"
)

// EventType identifies the kind of an Event and which of its fields apply.
type EventType uint8

const (
	EventTypeNoteOff EventType = iota
	EventTypeNoteOn
	EventTypePolyPressure
	EventTypeControlChange
	EventTypeProgramChange
	EventTypeChannelPressure
	EventTypePitchBend
	EventTypeSysEx
	EventTypeNoteExpression     // per-note float value
	EventTypeNoteExpressionInt  // per-note integer value
	EventTypeNoteExpressionText // per-note text (for example a phoneme)
	EventTypeChord
	EventTypeScale
)

var eventTypeNames = [...]string{
	EventTypeNoteOff:            "NoteOff",
	EventTypeNoteOn:             "NoteOn",
	EventTypePolyPressure:       "PolyPressure",
	EventTypeControlChange:      "ControlChange",
	EventTypeProgramChange:      "ProgramChange",
	EventTypeChannelPressure:    "ChannelPressure",
	EventTypePitchBend:          "PitchBend",
	EventTypeSysEx:              "SysEx",
	EventTypeNoteExpression:     "NoteExpression",
	EventTypeNoteExpressionInt:  "NoteExpressionInt",
	EventTypeNoteExpressionText: "NoteExpressionText",
	EventTypeChord:              "Chord",
	EventTypeScale:              "Scale",
}

func (t EventType) String() string {
	if int(t) < len(eventTypeNames) {
		return eventTypeNames[t]
	}
	return fmt.Sprintf("EventType(%d)", t)
}

// NoteIDNone marks events whose host did not assign a note ID.
const NoteIDNone int32 = -1

// Event is one note, controller or data event.
//
// It is a plain value type so the runtime can deliver events without heap
// allocation. Type selects which fields are meaningful:
//
//	NoteOn, NoteOff        Note, NoteID, Velocity, Tuning, Length (NoteOn)
//	PolyPressure           Note, NoteID, Value (pressure 0-1)
//	ControlChange          Controller, Value (0-1)
//	ChannelPressure        Value (0-1)
//	PitchBend              Value (-1 to +1)
//	ProgramChange          Program
//	NoteExpression         NoteID, ExpressionType, Value (0-1)
//	NoteExpressionInt      NoteID, ExpressionType, IntValue
//	NoteExpressionText     NoteID, ExpressionType, Data (UTF-8 text)
//	Chord                  Root, Bass, Mask, Data (UTF-8 name)
//	Scale                  Root, Mask, Data (UTF-8 name)
//	SysEx                  Data (complete message including F0/F7)
//
// Data on input events is only valid during the ProcessAudio call that
// delivered it; copy it to keep it.
type Event struct {
	Type       EventType
	Channel    uint8 // MIDI channel 0-15
	Note       uint8 // MIDI note number 0-127
	Controller uint8 // controller number 0-127
	Program    uint8 // program number 0-127

	// Offset is the sample position inside the host block.
	Offset int32
	// NoteID is the host note ID, NoteIDNone when unassigned.
	NoteID int32
	// Length is the note length in samples when the host knows it, else 0.
	Length int32

	Velocity float32 // 0-1
	Tuning   float32 // pitch offset in cents

	ExpressionType uint32
	Value          float64
	IntValue       uint64

	Root int16  // chord/scale root, 0 = C ... 11 = B, -1 = none
	Bass int16  // chord bass note, -1 = none
	Mask uint16 // chord/scale notes, bit 0 = root, bit 1 = minor second, ...

	Data []byte
}

// At returns a copy of e placed at a sample offset.
func (e Event) At(offset int32) Event {
	e.Offset = offset
	return e
}

// IsNote reports whether e is a note-on or note-off.
func (e Event) IsNote() bool {
	return e.Type == EventTypeNoteOn || e.Type == EventTypeNoteOff
}

// Velocity7 returns the velocity as a 7-bit MIDI value.
func (e Event) Velocity7() uint8 { return unitTo7Bit(float64(e.Velocity)) }

// Value7 returns Value (pressure, CC value) as a 7-bit MIDI value.
func (e Event) Value7() uint8 { return unitTo7Bit(e.Value) }

// PitchBend14 returns a pitch bend Value as a signed 14-bit value
// (-8192 to 8191, 0 is center).
func (e Event) PitchBend14() int16 {
	v := math.Round(e.Value * 8192)
	return int16(max(-8192, min(8191, v)))
}

// Text returns Data as a string. It allocates; use Data on the audio thread.
func (e Event) Text() string { return string(e.Data) }

func (e Event) String() string {
	switch e.Type {
	case EventTypeNoteOn, EventTypeNoteOff:
		return fmt.Sprintf("%s{ch:%d note:%d vel:%.3f id:%d offset:%d}", e.Type, e.Channel, e.Note, e.Velocity, e.NoteID, e.Offset)
	case EventTypePolyPressure:
		return fmt.Sprintf("PolyPressure{ch:%d note:%d value:%.3f offset:%d}", e.Channel, e.Note, e.Value, e.Offset)
	case EventTypeControlChange:
		return fmt.Sprintf("ControlChange{ch:%d cc:%d value:%.3f offset:%d}", e.Channel, e.Controller, e.Value, e.Offset)
	case EventTypeProgramChange:
		return fmt.Sprintf("ProgramChange{ch:%d program:%d offset:%d}", e.Channel, e.Program, e.Offset)
	case EventTypeNoteExpression, EventTypeNoteExpressionInt, EventTypeNoteExpressionText:
		return fmt.Sprintf("%s{type:%d id:%d value:%g int:%d offset:%d}", e.Type, e.ExpressionType, e.NoteID, e.Value, e.IntValue, e.Offset)
	case EventTypeSysEx:
		return fmt.Sprintf("SysEx{len:%d offset:%d}", len(e.Data), e.Offset)
	default:
		return fmt.Sprintf("%s{ch:%d value:%g offset:%d}", e.Type, e.Channel, e.Value, e.Offset)
	}
}

// Constructors for output events. Use At to set the sample offset.

// NoteOn creates a note-on with a 0-1 velocity.
func NoteOn(channel, note uint8, velocity float32) Event {
	return Event{Type: EventTypeNoteOn, Channel: channel, Note: note, Velocity: velocity, NoteID: NoteIDNone}
}

// NoteOff creates a note-off with a 0-1 release velocity.
func NoteOff(channel, note uint8, velocity float32) Event {
	return Event{Type: EventTypeNoteOff, Channel: channel, Note: note, Velocity: velocity, NoteID: NoteIDNone}
}

// PolyPressure creates a polyphonic aftertouch event with a 0-1 pressure.
func PolyPressure(channel, note uint8, pressure float64) Event {
	return Event{Type: EventTypePolyPressure, Channel: channel, Note: note, Value: pressure, NoteID: NoteIDNone}
}

// ControlChange creates a controller event with a 0-1 value.
func ControlChange(channel, controller uint8, value float64) Event {
	return Event{Type: EventTypeControlChange, Channel: channel, Controller: controller, Value: value}
}

// ChannelPressure creates a channel aftertouch event with a 0-1 pressure.
func ChannelPressure(channel uint8, pressure float64) Event {
	return Event{Type: EventTypeChannelPressure, Channel: channel, Value: pressure}
}

// PitchBend creates a pitch bend event, value -1 to +1 (0 is center).
func PitchBend(channel uint8, value float64) Event {
	return Event{Type: EventTypePitchBend, Channel: channel, Value: value}
}

// ProgramChange creates a program change event.
func ProgramChange(channel, program uint8) Event {
	return Event{Type: EventTypeProgramChange, Channel: channel, Program: program}
}

// NoteExpression creates a per-note expression value (0-1) for a note ID.
func NoteExpression(noteID int32, typeID uint32, value float64) Event {
	return Event{Type: EventTypeNoteExpression, NoteID: noteID, ExpressionType: typeID, Value: value}
}

// SysEx creates a system exclusive event. The runtime copies data when the
// event is sent, so the slice may be reused afterwards.
func SysEx(data []byte) Event {
	return Event{Type: EventTypeSysEx, Data: data}
}

// Note expression type IDs defined by VST3 (Steinberg::Vst::NoteExpressionTypeIDs).
const (
	NoteExpressionVolume     uint32 = 0 // gain = 4 * value: 0.25 = 0 dB, 0.5 = +6 dB, 1 = +12 dB
	NoteExpressionPan        uint32 = 1 // 0 = left, 0.5 = center, 1 = right
	NoteExpressionTuning     uint32 = 2 // 0.5 = no change, +/-120 semitones over the range
	NoteExpressionVibrato    uint32 = 3
	NoteExpressionExpression uint32 = 4
	NoteExpressionBrightness uint32 = 5
	NoteExpressionText       uint32 = 6 // text event
	NoteExpressionPhoneme    uint32 = 7 // text event
	// NoteExpressionCustomStart is the first ID free for plugin-defined types.
	NoteExpressionCustomStart uint32 = 100000
)

// Controller numbers.
const (
	CCModWheel       uint8 = 1
	CCBreath         uint8 = 2
	CCFoot           uint8 = 4
	CCPortamentoTime uint8 = 5
	CCVolume         uint8 = 7
	CCBalance        uint8 = 8
	CCPan            uint8 = 10
	CCExpression     uint8 = 11
	CCSustain        uint8 = 64
	CCPortamento     uint8 = 65
	CCSostenuto      uint8 = 66
	CCSoft           uint8 = 67
	CCLegato         uint8 = 68
	CCHold2          uint8 = 69
	CCAllSoundOff    uint8 = 120
	CCResetAll       uint8 = 121
	CCLocalControl   uint8 = 122
	CCAllNotesOff    uint8 = 123
)

func unitTo7Bit(v float64) uint8 {
	return uint8(math.Round(max(0, min(1, v)) * 127))
}

// AppendUTF16 appends UTF-16 text as UTF-8 to dst without allocating when
// dst has room. Invalid surrogates become U+FFFD.
func AppendUTF16(dst []byte, text []uint16) []byte {
	for i := 0; i < len(text); i++ {
		r := rune(text[i])
		if utf16.IsSurrogate(r) {
			if i+1 < len(text) {
				r = utf16.DecodeRune(r, rune(text[i+1]))
				if r != utf8.RuneError {
					i++
				}
			} else {
				r = utf8.RuneError
			}
		}
		dst = utf8.AppendRune(dst, r)
	}
	return dst
}

// NoteToFrequency returns the frequency of a MIDI note for an A4 tuning
// (0 means 440 Hz).
func NoteToFrequency(note uint8, tuningA4 float64) float64 {
	if tuningA4 == 0 {
		tuningA4 = 440.0
	}
	return tuningA4 * math.Exp2((float64(note)-69.0)/12.0)
}

// FrequencyToNote returns the nearest MIDI note for a frequency.
func FrequencyToNote(freq, tuningA4 float64) uint8 {
	if tuningA4 == 0 {
		tuningA4 = 440.0
	}
	if freq <= 0 {
		return 0
	}
	note := math.Round(69.0 + 12.0*math.Log2(freq/tuningA4))
	return uint8(max(0, min(127, note)))
}

var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// NoteNumberToName returns the note name, middle C (60) is "C4".
func NoteNumberToName(note uint8) string {
	return fmt.Sprintf("%s%d", noteNames[note%12], int(note/12)-1)
}
