package voice

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/midi"
)

// TestVoice is a simple voice implementation for testing
type TestVoice struct {
	active    bool
	note      uint8
	velocity  uint8
	amplitude float64
	age       int64
	triggers  int
	releases  int
}

func (v *TestVoice) IsActive() bool        { return v.active }
func (v *TestVoice) GetNote() uint8        { return v.note }
func (v *TestVoice) GetVelocity() uint8    { return v.velocity }
func (v *TestVoice) GetAmplitude() float64 { return v.amplitude }
func (v *TestVoice) GetAge() int64         { return v.age }
func (v *TestVoice) TriggerNote(note uint8, velocity uint8) {
	v.active = true
	v.note = note
	v.velocity = velocity
	v.age = 0
	v.amplitude = float64(velocity) / 127.0
	v.triggers++
}
func (v *TestVoice) ChangeNote(note uint8) { v.note = note }
func (v *TestVoice) ReleaseNote()          { v.active = false; v.releases++ }
func (v *TestVoice) Stop()                 { v.active = false; v.note = 0 }
func (v *TestVoice) Process(output []float32) {
	v.age++
	// Simulate amplitude decay
	if v.amplitude > 0.01 {
		v.amplitude *= 0.999
	}
}

func createTestVoices(count int) []Voice {
	voices := make([]Voice, count)
	for i := range voices {
		voices[i] = &TestVoice{}
	}
	return voices
}

func TestAllocatorPolyMode(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModePoly)

	// Test basic note allocation
	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)
	allocator.NoteOn(67, 100)

	activeCount := allocator.GetActiveVoiceCount()
	if activeCount != 3 {
		t.Errorf("Expected 3 active voices, got %d", activeCount)
	}

	// Test note off
	allocator.NoteOff(64, 0)
	activeCount = allocator.GetActiveVoiceCount()
	if activeCount != 2 {
		t.Errorf("Expected 2 active voices after note off, got %d", activeCount)
	}

	// Test retriggering same note
	allocator.NoteOn(60, 80)

	// Find which voice is playing note 60
	var foundVoice *TestVoice
	for _, v := range voices {
		tv := v.(*TestVoice)
		if tv.IsActive() && tv.GetNote() == 60 {
			foundVoice = tv
			break
		}
	}

	if foundVoice == nil {
		t.Error("Note 60 should be playing after retrigger")
	} else if foundVoice.velocity != 80 {
		t.Errorf("Expected velocity 80 for retriggered note, got %d", foundVoice.velocity)
	}
}

func TestAllocatorMonoMode(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModeMono)

	// Play multiple notes - only one should be active
	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)
	allocator.NoteOn(67, 100)

	activeCount := allocator.GetActiveVoiceCount()
	if activeCount != 1 {
		t.Errorf("Expected 1 active voice in mono mode, got %d", activeCount)
	}

	// Check that the last note is the active one
	v0 := voices[0].(*TestVoice)
	if v0.note != 67 {
		t.Errorf("Expected note 67 in mono mode, got %d", v0.note)
	}
}

func TestAllocatorLegatoMode(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModeLegato)

	// First note should trigger
	allocator.NoteOn(60, 100)
	v0 := voices[0].(*TestVoice)
	if !v0.active || v0.note != 60 {
		t.Error("First note should trigger in legato mode")
	}

	// Second note should change pitch without retriggering
	v0.age = 100 // Simulate some age
	allocator.NoteOn(64, 100)

	if !v0.active {
		t.Error("Voice should remain active in legato mode")
	}
	if v0.note != 64 {
		t.Errorf("Expected voice 0 to move to note 64, got %d", v0.note)
	}
	if v0.triggers != 1 || v0.age != 100 {
		t.Errorf("Legato note retriggered the voice: triggers=%d age=%d", v0.triggers, v0.age)
	}

	// Releasing the first key no longer affects the voice; releasing the
	// sounding key does.
	allocator.NoteOff(60, 0)
	if !v0.active {
		t.Error("Releasing a key that is no longer sounding should not release the voice")
	}
	allocator.NoteOff(64, 0)
	if v0.active {
		t.Error("Releasing the sounding key should release the voice")
	}
}

func TestAllocatorUnisonMode(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModeUnison)

	// Play a note - all voices should be active
	allocator.NoteOn(60, 100)

	activeCount := allocator.GetActiveVoiceCount()
	if activeCount != 4 {
		t.Errorf("Expected 4 active voices in unison mode, got %d", activeCount)
	}

	// Check all voices play the same note
	for i, v := range voices {
		tv := v.(*TestVoice)
		if tv.note != 60 {
			t.Errorf("Voice %d: expected note 60, got %d", i, tv.note)
		}
	}
}

func TestVoiceStealing(t *testing.T) {
	voices := createTestVoices(2)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModePoly)

	// Fill all voices
	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)

	// Age the voices differently
	voices[0].(*TestVoice).age = 100
	voices[1].(*TestVoice).age = 50

	// Test steal oldest
	allocator.SetStealingMode(StealOldest)
	allocator.NoteOn(67, 100)

	// Voice 0 should have been stolen (oldest)
	v0 := voices[0].(*TestVoice)
	if v0.note != 67 {
		t.Errorf("Expected voice 0 to be stolen and play note 67, got %d", v0.note)
	}

	// Test steal quietest
	voices[0].(*TestVoice).amplitude = 0.1
	voices[1].(*TestVoice).amplitude = 0.5
	allocator.SetStealingMode(StealQuietest)
	allocator.NoteOn(70, 100)

	// Voice 0 should have been stolen (quietest)
	if v0.note != 70 {
		t.Errorf("Expected voice 0 to be stolen and play note 70, got %d", v0.note)
	}

	// Test steal none
	allocator.SetStealingMode(StealNone)
	allocator.NoteOn(72, 100)
	allocator.NoteOn(74, 100)

	// No new notes should have been allocated
	activeNotes := make(map[uint8]bool)
	for _, v := range voices {
		if v.IsActive() {
			activeNotes[v.GetNote()] = true
		}
	}
	if activeNotes[72] || activeNotes[74] {
		t.Error("StealNone mode should not allocate new notes when full")
	}
}

func TestSustainPedal(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	allocator.SetMode(ModePoly)

	// Play and hold some notes
	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)

	// Press sustain pedal
	allocator.SetSustainPedal(true)

	// Release notes - they should stay active
	allocator.NoteOff(60, 0)
	allocator.NoteOff(64, 0)

	activeCount := allocator.GetActiveVoiceCount()
	if activeCount != 2 {
		t.Errorf("Expected 2 active voices with sustain pedal, got %d", activeCount)
	}

	// Release sustain pedal - notes should stop
	allocator.SetSustainPedal(false)

	activeCount = allocator.GetActiveVoiceCount()
	if activeCount != 0 {
		t.Errorf("Expected 0 active voices after releasing sustain, got %d", activeCount)
	}
}

func TestProcessEvent(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)

	// Test note on event
	noteOn := midi.NoteOn(0, 60, 100.0/127)
	allocator.ProcessEvent(noteOn)

	if allocator.GetActiveVoiceCount() != 1 {
		t.Error("ProcessEvent should handle note on")
	}

	// Test note off event
	noteOff := midi.NoteOff(0, 60, 0).At(100)
	allocator.ProcessEvent(noteOff)

	if allocator.GetActiveVoiceCount() != 0 {
		t.Error("ProcessEvent should handle note off")
	}

	// Test sustain pedal
	sustainOn := midi.ControlChange(0, midi.CCSustain, 1)
	allocator.ProcessEvent(sustainOn)

	if !allocator.SustainPedal() {
		t.Error("ProcessEvent should handle sustain pedal")
	}
	allocator.ProcessEvent(midi.NoteOn(0, 62, 1))
	allocator.ProcessEvent(midi.NoteOff(0, 62, 0))
	if allocator.GetActiveVoiceCount() != 1 {
		t.Error("Sustain pedal from ProcessEvent should hold released notes")
	}
	allocator.ProcessEvent(midi.ControlChange(0, midi.CCSustain, 0))
	if allocator.SustainPedal() || allocator.GetActiveVoiceCount() != 0 {
		t.Error("Sustain pedal off from ProcessEvent should release held notes")
	}
}

func TestMaxVoices(t *testing.T) {
	voices := createTestVoices(8)
	allocator := NewAllocator(voices)
	allocator.SetMaxVoices(4)

	// Try to play more than max voices
	for i := uint8(60); i < 68; i++ {
		allocator.NoteOn(i, 100)
	}

	activeCount := allocator.GetActiveVoiceCount()
	if activeCount > 4 {
		t.Errorf("Should not exceed max voices (4), got %d", activeCount)
	}
}

func TestReset(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)

	// Play some notes
	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)
	allocator.SetSustainPedal(true)

	// Reset
	allocator.Reset()

	if allocator.GetActiveVoiceCount() != 0 {
		t.Error("Reset should stop all voices")
	}
	if allocator.SustainPedal() {
		t.Error("Reset should clear sustain pedal")
	}

	// Stale key mappings would release voices on NoteOff for keys played
	// before the reset.
	allocator.NoteOff(60, 0)
	allocator.NoteOff(64, 0)
	for i, v := range voices {
		if r := v.(*TestVoice).releases; r != 0 {
			t.Errorf("Voice %d released %d times after Reset; note mappings were not cleared", i, r)
		}
	}

	// The pedal is up, so a new note is released immediately.
	allocator.NoteOn(67, 100)
	allocator.NoteOff(67, 0)
	if allocator.GetActiveVoiceCount() != 0 {
		t.Error("Note should release immediately after Reset cleared the pedal")
	}
}

func TestSustainedKeyPressedAgainSurvivesPedalRelease(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)

	allocator.SetSustainPedal(true)
	allocator.NoteOn(60, 100)
	allocator.NoteOff(60, 0)
	allocator.NoteOn(60, 100) // key held again
	allocator.SetSustainPedal(false)

	if allocator.GetActiveVoiceCount() != 1 {
		t.Fatal("A key held down when the pedal lifts must keep sounding")
	}
	allocator.NoteOff(60, 0)
	if allocator.GetActiveVoiceCount() != 0 {
		t.Fatal("Releasing the key after the pedal lifted must release the voice")
	}
}

func TestStolenVoiceIsNotReleasedByOldKey(t *testing.T) {
	voices := createTestVoices(2)
	allocator := NewAllocator(voices)

	allocator.NoteOn(60, 100)
	allocator.NoteOn(64, 100)
	voices[0].(*TestVoice).age = 100
	voices[1].(*TestVoice).age = 50
	allocator.NoteOn(67, 100) // steals voice 0 from key 60

	allocator.NoteOff(60, 0)
	if v0 := voices[0].(*TestVoice); !v0.active || v0.note != 67 {
		t.Fatalf("Releasing the stolen key released the new note: active=%v note=%d", v0.active, v0.note)
	}
}

func TestSetMaxVoicesStopsVoicesAboveLimit(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	for n := uint8(60); n < 64; n++ {
		allocator.NoteOn(n, 100)
	}
	allocator.SetMaxVoices(2)
	for i := 2; i < 4; i++ {
		if voices[i].IsActive() {
			t.Errorf("Voice %d above the new limit is still active", i)
		}
	}
}

func TestAllocatorDoesNotAllocate(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)
	events := []midi.Event{
		midi.NoteOn(0, 60, 0.8),
		midi.NoteOn(0, 64, 0.8),
		midi.ControlChange(0, midi.CCSustain, 1),
		midi.NoteOff(0, 60, 0),
		midi.NoteOn(0, 67, 0.8),
		midi.NoteOn(0, 71, 0.8),
		midi.NoteOn(0, 74, 0.8), // forces a steal
		midi.NoteOff(0, 64, 0),
		midi.ControlChange(0, midi.CCSustain, 0),
		midi.NoteOff(0, 67, 0),
		midi.NoteOff(0, 71, 0),
		midi.NoteOff(0, 74, 0),
	}
	for _, mode := range []AllocationMode{ModePoly, ModeMono, ModeLegato, ModeUnison} {
		allocator.SetMode(mode)
		allocs := testing.AllocsPerRun(100, func() {
			for _, e := range events {
				allocator.ProcessEvent(e)
			}
			allocator.Reset()
		})
		if allocs != 0 {
			t.Errorf("mode %d: %v allocations per event sequence", mode, allocs)
		}
	}
}

func TestAllNotesOffAndAllSoundOff(t *testing.T) {
	voices := createTestVoices(4)
	allocator := NewAllocator(voices)

	allocator.NoteOn(60, 100)
	allocator.SetSustainPedal(true)
	allocator.NoteOn(64, 100)
	allocator.NoteOff(64, 0)
	allocator.ProcessEvent(midi.ControlChange(0, midi.CCAllNotesOff, 0))
	if n := allocator.GetActiveVoiceCount(); n != 0 {
		t.Fatalf("All Notes Off left %d voices playing", n)
	}
	if allocator.SustainPedal() {
		t.Fatal("All Notes Off should lift the sustain pedal")
	}
	released := 0
	for _, v := range voices {
		released += v.(*TestVoice).releases
	}
	if released != 2 {
		t.Fatalf("All Notes Off released %d voices, want 2 (through ReleaseNote)", released)
	}

	allocator.NoteOn(67, 100)
	allocator.ProcessEvent(midi.ControlChange(0, midi.CCAllSoundOff, 0))
	for i, v := range voices {
		if tv := v.(*TestVoice); tv.active || tv.note != 0 {
			t.Fatalf("All Sound Off left voice %d active=%v note=%d", i, tv.active, tv.note)
		}
	}
}
