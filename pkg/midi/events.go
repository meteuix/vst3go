// Package midi provides MIDI event types, queues, and buffers for VST3 transport handling.
package midi

import (
	"fmt"
)

type EventType uint8

const (
	EventTypeNoteOff EventType = iota
	EventTypeNoteOn
	EventTypePolyPressure
	EventTypeControlChange
	EventTypeProgramChange
	EventTypeChannelPressure
	EventTypePitchBend
	EventTypeSystemExclusive
	EventTypeClock
	EventTypeStart
	EventTypeStop
	EventTypeContinue
	EventTypeReset
	EventTypeActiveSensing
	EventTypeNoteExpression
)

// NoteIDNone marks events whose host did not assign a note ID.
const NoteIDNone int32 = -1

type Event interface {
	Type() EventType
	Channel() uint8
	SampleOffset() int32
	String() string
}

type BaseEvent struct {
	EventChannel uint8
	Offset       int32
}

func (e BaseEvent) Channel() uint8 {
	return e.EventChannel
}

func (e BaseEvent) SampleOffset() int32 {
	return e.Offset
}

type NoteOnEvent struct {
	BaseEvent
	NoteNumber uint8
	Velocity   uint8 // 0-127, rounded from VelocityNorm
	// VelocityNorm is the full-precision host velocity in [0, 1]. When zero on
	// output events, Velocity/127 is sent instead.
	VelocityNorm float32
	Tuning       float32 // pitch offset in cents
	Length       int32   // note length in samples when known, otherwise 0
	NoteID       int32   // host note ID, NoteIDNone when unassigned
}

func (e NoteOnEvent) Type() EventType {
	return EventTypeNoteOn
}

func (e NoteOnEvent) String() string {
	return fmt.Sprintf("NoteOn{ch:%d, note:%d, vel:%d, offset:%d}",
		e.EventChannel, e.NoteNumber, e.Velocity, e.Offset)
}

type NoteOffEvent struct {
	BaseEvent
	NoteNumber   uint8
	Velocity     uint8 // 0-127, rounded from VelocityNorm
	VelocityNorm float32
	Tuning       float32 // pitch offset in cents
	NoteID       int32   // host note ID, NoteIDNone when unassigned
}

func (e NoteOffEvent) Type() EventType {
	return EventTypeNoteOff
}

func (e NoteOffEvent) String() string {
	return fmt.Sprintf("NoteOff{ch:%d, note:%d, vel:%d, offset:%d}",
		e.EventChannel, e.NoteNumber, e.Velocity, e.Offset)
}

type ControlChangeEvent struct {
	BaseEvent
	Controller uint8
	Value      uint8
}

func (e ControlChangeEvent) Type() EventType {
	return EventTypeControlChange
}

func (e ControlChangeEvent) String() string {
	return fmt.Sprintf("CC{ch:%d, ctrl:%d, val:%d, offset:%d}",
		e.EventChannel, e.Controller, e.Value, e.Offset)
}

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

type PitchBendEvent struct {
	BaseEvent
	Value int16 // -8192 to 8191, 0 is center
}

func (e PitchBendEvent) Type() EventType {
	return EventTypePitchBend
}

func (e PitchBendEvent) String() string {
	return fmt.Sprintf("PitchBend{ch:%d, val:%d, offset:%d}",
		e.EventChannel, e.Value, e.Offset)
}

func (e PitchBendEvent) NormalizedValue() float64 {
	return float64(e.Value) / 8192.0
}

type PolyPressureEvent struct {
	BaseEvent
	NoteNumber   uint8
	Pressure     uint8 // 0-127, rounded from PressureNorm
	PressureNorm float32
	NoteID       int32 // host note ID, NoteIDNone when unassigned
}

func (e PolyPressureEvent) Type() EventType {
	return EventTypePolyPressure
}

func (e PolyPressureEvent) String() string {
	return fmt.Sprintf("PolyPressure{ch:%d, note:%d, pressure:%d, offset:%d}",
		e.EventChannel, e.NoteNumber, e.Pressure, e.Offset)
}

type ChannelPressureEvent struct {
	BaseEvent
	Pressure uint8
}

func (e ChannelPressureEvent) Type() EventType {
	return EventTypeChannelPressure
}

func (e ChannelPressureEvent) String() string {
	return fmt.Sprintf("ChannelPressure{ch:%d, pressure:%d, offset:%d}",
		e.EventChannel, e.Pressure, e.Offset)
}

type ProgramChangeEvent struct {
	BaseEvent
	Program uint8
}

func (e ProgramChangeEvent) Type() EventType {
	return EventTypeProgramChange
}

func (e ProgramChangeEvent) String() string {
	return fmt.Sprintf("ProgramChange{ch:%d, prog:%d, offset:%d}",
		e.EventChannel, e.Program, e.Offset)
}

// Note expression type IDs defined by VST3.
const (
	NoteExpressionVolume     uint32 = 0 // linear volume, 0.25 = -12 dB, 1 = +6 dB
	NoteExpressionPan        uint32 = 1 // 0 = left, 0.5 = center, 1 = right
	NoteExpressionTuning     uint32 = 2 // 0.5 = no change, +/-120 semitones over the range
	NoteExpressionVibrato    uint32 = 3
	NoteExpressionExpression uint32 = 4
	NoteExpressionBrightness uint32 = 5
)

// NoteExpressionEvent carries a per-note VST3 expression value addressed by
// note ID. Value is normalized to [0, 1].
type NoteExpressionEvent struct {
	BaseEvent
	TypeID uint32
	NoteID int32
	Value  float64
}

func (e NoteExpressionEvent) Type() EventType {
	return EventTypeNoteExpression
}

func (e NoteExpressionEvent) String() string {
	return fmt.Sprintf("NoteExpression{type:%d, note:%d, val:%.4f, offset:%d}",
		e.TypeID, e.NoteID, e.Value, e.Offset)
}

// SysExEvent carries a system exclusive message. Data is only valid during
// the ProcessAudio call that delivered it; copy it to keep it.
type SysExEvent struct {
	BaseEvent
	Data []byte
}

func (e SysExEvent) Type() EventType {
	return EventTypeSystemExclusive
}

func (e SysExEvent) String() string {
	return fmt.Sprintf("SysEx{len:%d, offset:%d}", len(e.Data), e.Offset)
}

type ClockEvent struct {
	BaseEvent
}

func (e ClockEvent) Type() EventType {
	return EventTypeClock
}

func (e ClockEvent) String() string {
	return fmt.Sprintf("Clock{offset:%d}", e.Offset)
}

type StartEvent struct {
	BaseEvent
}

func (e StartEvent) Type() EventType {
	return EventTypeStart
}

func (e StartEvent) String() string {
	return fmt.Sprintf("Start{offset:%d}", e.Offset)
}

type StopEvent struct {
	BaseEvent
}

func (e StopEvent) Type() EventType {
	return EventTypeStop
}

func (e StopEvent) String() string {
	return fmt.Sprintf("Stop{offset:%d}", e.Offset)
}

type ContinueEvent struct {
	BaseEvent
}

func (e ContinueEvent) Type() EventType {
	return EventTypeContinue
}

func (e ContinueEvent) String() string {
	return fmt.Sprintf("Continue{offset:%d}", e.Offset)
}

func NoteToFrequency(note uint8, tuningA4 float64) float64 {
	if tuningA4 == 0 {
		tuningA4 = 440.0
	}
	return tuningA4 * pow2((float64(note)-69.0)/12.0)
}

func pow2(x float64) float64 {
	// Fast approximation of 2^x
	if x >= 0 {
		whole := int(x)
		frac := x - float64(whole)
		// 2^whole * 2^frac
		// Use Taylor series approximation for fractional part
		fracPow := 1.0 + frac*(0.693147+frac*(0.240227+frac*0.055504))
		return float64(uint64(1)<<uint(whole)) * fracPow
	} else {
		// For negative x, use 2^x = 1 / 2^(-x)
		return 1.0 / pow2(-x)
	}
}

func FrequencyToNote(freq, tuningA4 float64) uint8 {
	if tuningA4 == 0 {
		tuningA4 = 440.0
	}
	note := 69.0 + 12.0*log2(freq/tuningA4)
	if note < 0 {
		return 0
	}
	if note > 127 {
		return 127
	}
	return uint8(note + 0.5)
}

func log2(x float64) float64 {
	// Fast approximation of log2(x)
	if x <= 0 {
		return -1000.0 // Return a very negative number for invalid input
	}

	// Normalize x to [1, 2) range
	exp := 0
	for x >= 2.0 {
		x /= 2.0
		exp++
	}
	for x < 1.0 {
		x *= 2.0
		exp--
	}

	// Now x is in [1, 2), use polynomial approximation
	// log2(x) ≈ (x-1) * (1.4427 - 0.7213*(x-1) + 0.4821*(x-1)^2)
	t := x - 1.0
	frac := t * (1.4427 - t*(0.7213-t*0.4821))

	return float64(exp) + frac
}

func NoteNumberToName(note uint8) string {
	noteNames := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	octave := int(note/12) - 1
	noteName := noteNames[note%12]
	return fmt.Sprintf("%s%d", noteName, octave)
}
