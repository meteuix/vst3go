package voice

import (
	"github.com/th-release/vst3go/v2/pkg/midi"
)

// AllocationMode defines how voices are allocated
type AllocationMode int

const (
	// Poly mode - each note gets its own voice
	ModePoly AllocationMode = iota
	// Mono mode - only one voice active at a time
	ModeMono
	// Legato mode - mono with no retriggering on overlapping notes
	ModeLegato
	// Unison mode - all voices play the same note
	ModeUnison
)

// StealingMode defines how voices are stolen when all are in use
type StealingMode int

const (
	// StealOldest steals the oldest playing voice
	StealOldest StealingMode = iota
	// StealQuietest steals the voice with lowest amplitude
	StealQuietest
	// StealHighest steals the highest pitched voice
	StealHighest
	// StealLowest steals the lowest pitched voice
	StealLowest
	// StealNone doesn't steal - new notes are ignored when full
	StealNone
)

// Voice represents a single voice in the synthesizer
type Voice interface {
	// IsActive returns true if the voice is currently playing
	IsActive() bool
	// GetNote returns the MIDI note number this voice is playing
	GetNote() uint8
	// GetVelocity returns the velocity of the note
	GetVelocity() uint8
	// GetAmplitude returns the current amplitude (for steal quietest)
	GetAmplitude() float64
	// GetAge returns how long this voice has been playing (in samples)
	GetAge() int64
	// TriggerNote starts playing a note
	TriggerNote(note uint8, velocity uint8)
	// ChangeNote moves a sounding voice to a new note without retriggering
	// its envelope (legato)
	ChangeNote(note uint8)
	// ReleaseNote releases the note
	ReleaseNote()
	// Stop immediately stops the voice
	Stop()
	// Process generates audio for this voice
	Process(output []float32)
}

// noNote marks a voice slot that is not assigned to a held key.
const noNote = -1

// Allocator manages voice allocation for polyphonic synthesis.
//
// All state is sized in NewAllocator; NoteOn, NoteOff, ProcessEvent,
// SetSustainPedal and Reset do not allocate and are safe to call from the
// audio thread.
type Allocator struct {
	voices        []Voice
	mode          AllocationMode
	stealingMode  StealingMode
	maxVoices     int
	lastTriggered int // For round-robin allocation

	// voiceNote[i] is the key voice i was triggered by, or noNote once the
	// key was released or the voice was stolen.
	voiceNote []int

	sustainPedal bool
	// sustained[n] is true for keys released while the pedal was down.
	sustained [128]bool

	// currentNote is the key sounding in mono, legato and unison modes, or
	// noNote.
	currentNote int
}

// NewAllocator creates a new voice allocator
func NewAllocator(voices []Voice) *Allocator {
	a := &Allocator{
		voices:       voices,
		mode:         ModePoly,
		stealingMode: StealOldest,
		maxVoices:    len(voices),
		voiceNote:    make([]int, len(voices)),
		currentNote:  noNote,
	}
	for i := range a.voiceNote {
		a.voiceNote[i] = noNote
	}
	return a
}

// SetMode sets the allocation mode
func (a *Allocator) SetMode(mode AllocationMode) {
	a.mode = mode
	// Reset all voices when changing mode
	a.Reset()
}

// SetStealingMode sets the voice stealing mode
func (a *Allocator) SetStealingMode(mode StealingMode) {
	a.stealingMode = mode
}

// SetMaxVoices sets the maximum number of active voices. Voices above the
// new limit are stopped.
func (a *Allocator) SetMaxVoices(max int) {
	if max > len(a.voices) {
		max = len(a.voices)
	}
	if max < 1 {
		max = 1
	}
	for i := max; i < a.maxVoices; i++ {
		a.voices[i].Stop()
		a.voiceNote[i] = noNote
	}
	a.maxVoices = max
	if a.lastTriggered >= max {
		a.lastTriggered = 0
	}
}

// SustainPedal reports whether the sustain pedal is down
func (a *Allocator) SustainPedal() bool {
	return a.sustainPedal
}

// ProcessEvent handles a MIDI event
func (a *Allocator) ProcessEvent(event midi.Event) {
	switch event.Type {
	case midi.EventTypeNoteOn:
		if v := event.Velocity7(); v > 0 {
			a.NoteOn(event.Note, v)
		} else {
			// Note on with velocity 0 is treated as note off
			a.NoteOff(event.Note, 0)
		}
	case midi.EventTypeNoteOff:
		a.NoteOff(event.Note, event.Velocity7())
	case midi.EventTypeControlChange:
		switch event.Controller {
		case midi.CCSustain:
			a.SetSustainPedal(event.Value7() >= 64)
		case midi.CCAllNotesOff:
			a.ReleaseAll()
		case midi.CCAllSoundOff:
			a.Reset()
		}
	}
}

// ReleaseAll releases every held and sustained note and lifts the sustain
// pedal; the voices finish their release stage.
func (a *Allocator) ReleaseAll() {
	a.sustainPedal = false
	a.sustained = [128]bool{}
	for i := 0; i < a.maxVoices; i++ {
		if a.voiceNote[i] != noNote {
			a.voices[i].ReleaseNote()
			a.voiceNote[i] = noNote
		}
	}
	a.currentNote = noNote
}

// NoteOn handles a note on event
func (a *Allocator) NoteOn(note uint8, velocity uint8) {
	if note > 127 {
		return
	}
	// A key pressed again while the pedal is down is held by the key, not
	// the pedal.
	a.sustained[note] = false

	switch a.mode {
	case ModePoly:
		a.noteOnPoly(note, velocity)
	case ModeMono:
		a.noteOnMono(note, velocity)
	case ModeLegato:
		a.noteOnLegato(note, velocity)
	case ModeUnison:
		a.noteOnUnison(note, velocity)
	}
}

// NoteOff handles a note off event
func (a *Allocator) NoteOff(note uint8, velocity uint8) {
	if note > 127 {
		return
	}
	if a.sustainPedal {
		a.sustained[note] = true
		return
	}
	a.release(note)
}

// release releases every voice the key holds.
func (a *Allocator) release(note uint8) {
	switch a.mode {
	case ModePoly:
		a.noteOffPoly(note)
	case ModeMono, ModeLegato:
		a.noteOffMono(note)
	case ModeUnison:
		a.noteOffUnison(note)
	}
}

// SetSustainPedal sets the sustain pedal state. Lifting the pedal releases
// every key that was let go while it was down.
func (a *Allocator) SetSustainPedal(on bool) {
	a.sustainPedal = on
	if on {
		return
	}
	for note := range a.sustained {
		if a.sustained[note] {
			a.sustained[note] = false
			a.release(uint8(note))
		}
	}
}

// Reset stops all voices and clears allocations
func (a *Allocator) Reset() {
	for i, voice := range a.voices {
		voice.Stop()
		a.voiceNote[i] = noNote
	}
	a.sustained = [128]bool{}
	a.sustainPedal = false
	a.currentNote = noNote
	a.lastTriggered = 0
}

// GetActiveVoiceCount returns the number of active voices
func (a *Allocator) GetActiveVoiceCount() int {
	count := 0
	for _, voice := range a.voices[:a.maxVoices] {
		if voice.IsActive() {
			count++
		}
	}
	return count
}

// noteOnPoly handles poly mode note on
func (a *Allocator) noteOnPoly(note uint8, velocity uint8) {
	// Retrigger the voice(s) already holding this key
	retriggered := false
	for i := 0; i < a.maxVoices; i++ {
		if a.voiceNote[i] == int(note) {
			a.voices[i].TriggerNote(note, velocity)
			retriggered = true
		}
	}
	if retriggered {
		return
	}

	voiceIdx := a.findFreeVoice()
	if voiceIdx == -1 {
		voiceIdx = a.stealVoice()
		if voiceIdx == -1 {
			return
		}
	}

	a.voices[voiceIdx].TriggerNote(note, velocity)
	a.voiceNote[voiceIdx] = int(note)
}

// noteOffPoly handles poly mode note off
func (a *Allocator) noteOffPoly(note uint8) {
	for i := 0; i < a.maxVoices; i++ {
		if a.voiceNote[i] == int(note) {
			a.voices[i].ReleaseNote()
			a.voiceNote[i] = noNote
		}
	}
}

// noteOnMono handles mono mode note on: voice 0 is retriggered every time
func (a *Allocator) noteOnMono(note uint8, velocity uint8) {
	if a.voices[0].IsActive() {
		a.voices[0].Stop()
	}
	a.currentNote = int(note)
	a.voiceNote[0] = int(note)
	a.voices[0].TriggerNote(note, velocity)
}

// noteOnLegato handles legato mode note on: an overlapping note moves the
// sounding voice to the new pitch without retriggering it
func (a *Allocator) noteOnLegato(note uint8, velocity uint8) {
	if a.currentNote == noNote || !a.voices[0].IsActive() {
		a.noteOnMono(note, velocity)
		return
	}
	a.currentNote = int(note)
	a.voiceNote[0] = int(note)
	a.voices[0].ChangeNote(note)
}

// noteOffMono handles mono/legato mode note off
func (a *Allocator) noteOffMono(note uint8) {
	if int(note) == a.currentNote {
		a.voices[0].ReleaseNote()
		a.voiceNote[0] = noNote
		a.currentNote = noNote
	}
}

// noteOnUnison handles unison mode note on: every voice plays the note
func (a *Allocator) noteOnUnison(note uint8, velocity uint8) {
	for i := 0; i < a.maxVoices; i++ {
		a.voices[i].TriggerNote(note, velocity)
		a.voiceNote[i] = int(note)
	}
	a.currentNote = int(note)
}

// noteOffUnison handles unison mode note off
func (a *Allocator) noteOffUnison(note uint8) {
	if int(note) == a.currentNote {
		for i := 0; i < a.maxVoices; i++ {
			a.voices[i].ReleaseNote()
			a.voiceNote[i] = noNote
		}
		a.currentNote = noNote
	}
}

// findFreeVoice finds an inactive voice
func (a *Allocator) findFreeVoice() int {
	// Use round-robin to distribute voices evenly
	start := a.lastTriggered
	for i := 0; i < a.maxVoices; i++ {
		idx := (start + i + 1) % a.maxVoices
		if !a.voices[idx].IsActive() {
			a.lastTriggered = idx
			return idx
		}
	}
	return -1
}

// stealVoice stops and returns the voice chosen by the stealing mode, or -1
func (a *Allocator) stealVoice() int {
	if a.stealingMode == StealNone {
		return -1
	}

	bestIdx := -1
	var bestValue float64

	for i := 0; i < a.maxVoices; i++ {
		if !a.voices[i].IsActive() {
			continue
		}

		switch a.stealingMode {
		case StealOldest:
			age := float64(a.voices[i].GetAge())
			if bestIdx == -1 || age > bestValue {
				bestIdx = i
				bestValue = age
			}
		case StealQuietest:
			amp := a.voices[i].GetAmplitude()
			if bestIdx == -1 || amp < bestValue {
				bestIdx = i
				bestValue = amp
			}
		case StealHighest:
			note := float64(a.voices[i].GetNote())
			if bestIdx == -1 || note > bestValue {
				bestIdx = i
				bestValue = note
			}
		case StealLowest:
			note := float64(a.voices[i].GetNote())
			if bestIdx == -1 || note < bestValue {
				bestIdx = i
				bestValue = note
			}
		}
	}

	if bestIdx != -1 {
		a.voiceNote[bestIdx] = noNote
		a.voices[bestIdx].Stop()
	}

	return bestIdx
}
