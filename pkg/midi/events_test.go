package midi

import (
	"math"
	"testing"
)

func TestConstructors(t *testing.T) {
	on := NoteOn(2, 60, 0.5).At(12)
	if on.Type != EventTypeNoteOn || on.Channel != 2 || on.Note != 60 || on.Offset != 12 || on.NoteID != NoteIDNone {
		t.Fatalf("NoteOn = %+v", on)
	}
	if on.Velocity7() != 64 {
		t.Fatalf("Velocity7 = %d, want 64", on.Velocity7())
	}
	if !on.IsNote() || !NoteOff(0, 1, 0).IsNote() || ControlChange(0, 1, 0).IsNote() {
		t.Fatal("IsNote mismatch")
	}

	cc := ControlChange(0, CCModWheel, 1)
	if cc.Value7() != 127 || cc.Controller != CCModWheel {
		t.Fatalf("ControlChange = %+v", cc)
	}
}

func TestPitchBend14(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  int16
	}{{-1, -8192}, {0, 0}, {1, 8191}, {0.5, 4096}, {-2, -8192}} {
		if got := PitchBend(0, tc.value).PitchBend14(); got != tc.want {
			t.Fatalf("PitchBend14(%v) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestAppendUTF16(t *testing.T) {
	text := []uint16{'a', 0x00E9, 0xD83D, 0xDE00, 0xD800}
	buf := make([]byte, 0, 32)
	got := AppendUTF16(buf, text)
	if string(got) != "aé😀\uFFFD" {
		t.Fatalf("AppendUTF16 = %q", got)
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = AppendUTF16(buf[:0], text) }); allocs != 0 {
		t.Fatalf("AppendUTF16 allocated %v times", allocs)
	}
}

func TestNoteHelpers(t *testing.T) {
	if f := NoteToFrequency(69, 0); f != 440 {
		t.Fatalf("A4 = %v", f)
	}
	if f := NoteToFrequency(81, 440); math.Abs(f-880) > 1e-9 {
		t.Fatalf("A5 = %v", f)
	}
	if n := FrequencyToNote(261.63, 0); n != 60 {
		t.Fatalf("FrequencyToNote(C4) = %d", n)
	}
	if n := FrequencyToNote(1e6, 0); n != 127 {
		t.Fatalf("FrequencyToNote clamps high, got %d", n)
	}
	if name := NoteNumberToName(60); name != "C4" {
		t.Fatalf("NoteNumberToName(60) = %q", name)
	}
}

func TestEventString(t *testing.T) {
	if s := NoteOn(0, 60, 1).At(3).String(); s != "NoteOn{ch:0 note:60 vel:1.000 id:-1 offset:3}" {
		t.Fatalf("String = %q", s)
	}
	if s := EventType(200).String(); s != "EventType(200)" {
		t.Fatalf("unknown type string = %q", s)
	}
}
