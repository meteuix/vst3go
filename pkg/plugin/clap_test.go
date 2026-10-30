package plugin

import (
	"math"
	"slices"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/midi"
)

type featurePlugin struct {
	testPlugin
	features []string
}

func (p featurePlugin) CLAPFeatures() []string { return p.features }

func TestCLAPFeatures(t *testing.T) {
	cases := []struct {
		category string
		want     []string
	}{
		{"", []string{"audio-effect"}},
		{frameworkplugin.CategoryFxEQ, []string{"audio-effect", "equalizer"}},
		{frameworkplugin.CategoryFxDynamics, []string{"audio-effect", "compressor"}},
		{frameworkplugin.CategoryInstrumentSynth, []string{"instrument", "synthesizer"}},
		{frameworkplugin.CategoryFxInstrument, []string{"note-effect"}},
		{frameworkplugin.CategoryAnalyzer, []string{"analyzer"}},
	}
	for _, tc := range cases {
		info := frameworkplugin.Info{Category: tc.category}
		if got := clapFeatures(testPlugin{}, info); !slices.Equal(got, tc.want) {
			t.Errorf("category %q: features %v, want %v", tc.category, got, tc.want)
		}
	}
	custom := featurePlugin{features: []string{"audio-effect", "stereo"}}
	if got := clapFeatures(custom, frameworkplugin.Info{}); !slices.Equal(got, custom.features) {
		t.Errorf("custom features %v", got)
	}
}

func TestCLAPParamDomains(t *testing.T) {
	continuous := &param.Parameter{ID: 1, Min: -24, Max: 24, Unit: "dB"}
	stepped := &param.Parameter{ID: 2, Min: -24, Max: 24, StepCount: 48, Unit: "st"}
	unit := &param.Parameter{ID: 3}

	if lo, hi := clapParamRange(continuous); lo != -24 || hi != 24 {
		t.Fatalf("continuous range %v..%v", lo, hi)
	}
	if lo, hi := clapParamRange(stepped); lo != 0 || hi != 48 {
		t.Fatalf("stepped range %v..%v, want step indexes 0..48", lo, hi)
	}
	if lo, hi := clapParamRange(unit); lo != 0 || hi != 1 {
		t.Fatalf("empty range %v..%v, want 0..1", lo, hi)
	}
	for _, plain := range []float64{-24, -3.5, 0, 24} {
		if got := clapPlain(continuous, clapNormalized(continuous, plain)); math.Abs(got-plain) > 1e-12 {
			t.Errorf("continuous %v round-trips to %v", plain, got)
		}
	}
	for step := 0.0; step <= 48; step++ {
		n := clapNormalized(stepped, step)
		if got := clapPlain(stepped, n); got != step {
			t.Fatalf("step %v round-trips to %v", step, got)
		}
		if plain := stepped.Denormalize(n); plain != step-24 {
			t.Fatalf("step %v is %v st, want %v", step, plain, step-24)
		}
	}
	if got := clapNormalized(stepped, 36.4); got != 36.0/48 {
		t.Fatalf("fractional step not rounded: %v", got)
	}
	if got := clapNormalized(continuous, math.NaN()); got != 0 {
		t.Fatalf("NaN plain value = %v, want the default", got)
	}

	if text := clapParamText(stepped, 36); text != "12 st" {
		t.Fatalf("step 36 text %q", text)
	}
	if plain, err := clapParamValue(stepped, "12 st"); err != nil || plain != 36 {
		t.Fatalf("parse 12 st = %v, %v", plain, err)
	}
	if plain, err := clapParamValue(continuous, " -6.00 dB "); err != nil || plain != -6 {
		t.Fatalf("parse -6.00 dB = %v, %v", plain, err)
	}
}

func TestCLAPScaledParamsUseTheirNormalizedValue(t *testing.T) {
	cutoff := param.FrequencyParameter(1, "Cutoff", 20, 20000, 1000).Build()
	if lo, hi := clapParamRange(cutoff); lo != 0 || hi != 1 {
		t.Fatalf("log range exposed as %v..%v, want 0..1", lo, hi)
	}
	n := cutoff.Normalize(1000)
	if got := clapNormalized(cutoff, clapPlain(cutoff, n)); math.Abs(got-n) > 1e-12 {
		t.Fatalf("round trip %v -> %v", n, got)
	}
	if text := clapParamText(cutoff, 0.5); text != "632.5 Hz" {
		t.Fatalf("midpoint text %q, want the geometric mean", text)
	}
	if v, err := clapParamValue(cutoff, "1 kHz"); err != nil || math.Abs(v-n) > 1e-12 {
		t.Fatalf("parse 1 kHz = %v, %v; want %v", v, err, n)
	}
}

// Text round trips the way clap-validator checks them: value to text, back
// to a value, and to the same text again, including formatters that switch
// units ("1.00 s" for a parameter in ms).
func TestCLAPParamTextRoundTrips(t *testing.T) {
	params := []*param.Parameter{
		param.TimeParameter(1, "Release", 1, 2000, 100).Build(),
		param.TimeParameter(2, "Delay", 0, 2000, 250).Build(),
		param.FrequencyParameter(3, "Cutoff", 20, 20000, 1000).Build(),
		param.New(4, "Depth").Range(0, 100).Unit("%").Build(),
	}
	for _, p := range params {
		lo, hi := clapParamRange(p)
		// 10/11 is the value clap-validator found where Release (1 to
		// 2000 ms) sits a hair over 1000 ms and reads back a hair under.
		for i := 0; i <= 21; i++ {
			value := lo + (hi-lo)*float64(i)/20
			if i == 21 {
				value = lo + (hi-lo)*10/11
			}
			text := clapParamText(p, value)
			parsed, err := clapParamValue(p, text)
			if err != nil {
				t.Fatalf("%s: %v -> %q does not parse: %v", p.Name, value, text, err)
			}
			if again := clapParamText(p, parsed); again != text {
				t.Fatalf("%s: %v -> %q -> %v -> %q", p.Name, value, text, parsed, again)
			}
		}
	}
	if text := clapParamText(params[0], 1); text != "2.00 s" {
		t.Fatalf("Release at its maximum reads %q, want 2.00 s", text)
	}
	if text := clapParamText(params[3], 50); text != "50.00 %" {
		t.Fatalf("bare number text %q, want the unit appended", text)
	}
}

func TestCLAPParamFlags(t *testing.T) {
	cases := []struct {
		p    param.Parameter
		want uint32
	}{
		{param.Parameter{Flags: param.CanAutomate}, clapParamIsAutomatable},
		{param.Parameter{Flags: param.CanAutomate | param.IsList, StepCount: 3}, clapParamIsAutomatable | clapParamIsStepped | clapParamIsEnum},
		{param.Parameter{Flags: param.CanAutomate | param.IsBypass, StepCount: 1}, clapParamIsAutomatable | clapParamIsStepped | clapParamIsBypass},
		{param.Parameter{Flags: param.CanAutomate | param.IsReadOnly}, clapParamIsReadonly},
		{param.Parameter{Flags: param.IsHidden | param.IsWrapAround}, clapParamIsHidden | clapParamIsPeriodic},
		{param.Parameter{Flags: param.IsBypass}, 0}, // CLAP bypass must be stepped
	}
	for i, tc := range cases {
		if got := clapParamFlags(&tc.p); got != tc.want {
			t.Errorf("case %d: flags %b, want %b", i, got, tc.want)
		}
	}
}

func TestCLAPExpressionMapping(t *testing.T) {
	cases := []struct {
		id         int32
		clap       float64
		typeID     uint32
		normalized float64
	}{
		{clapExprVolume, 1, midi.NoteExpressionVolume, 0.25},
		{clapExprPan, 0.5, midi.NoteExpressionPan, 0.5},
		{clapExprTuning, 12, midi.NoteExpressionTuning, 0.55},
		{clapExprTuning, -120, midi.NoteExpressionTuning, 0},
		{clapExprBrightness, 0.3, midi.NoteExpressionBrightness, 0.3},
	}
	for _, tc := range cases {
		typeID, normalized, pressure, ok := clapExpressionToVST3(tc.id, tc.clap)
		if !ok || pressure || typeID != tc.typeID || math.Abs(normalized-tc.normalized) > 1e-12 {
			t.Fatalf("CLAP %d=%v -> type %d %v (pressure %v ok %v)", tc.id, tc.clap, typeID, normalized, pressure, ok)
		}
		id, value, ok := vst3ExpressionToCLAP(typeID, normalized)
		if !ok || id != tc.id || math.Abs(value-tc.clap) > 1e-9 {
			t.Fatalf("VST3 type %d=%v -> CLAP %d %v", typeID, normalized, id, value)
		}
	}
	if _, v, pressure, ok := clapExpressionToVST3(clapExprPressure, 0.7); !ok || !pressure || v != 0.7 {
		t.Fatal("pressure must become poly pressure")
	}
	if _, _, _, ok := clapExpressionToVST3(99, 1); ok {
		t.Fatal("unknown expression accepted")
	}
	if _, _, ok := vst3ExpressionToCLAP(midi.NoteExpressionCustomStart, 1); ok {
		t.Fatal("custom VST3 expression has no CLAP equivalent")
	}
}

func TestCLAPNotesResolveWildcards(t *testing.T) {
	var notes clapNotes
	hostID := notes.noteOn(0, 60, 7)
	synthetic := notes.noteOn(1, 64, -1)
	if hostID != 7 || synthetic < 0 || synthetic == 7 {
		t.Fatalf("note IDs %d %d", hostID, synthetic)
	}
	type hit struct{ ch, key, id int32 }
	collect := func(channel, key, noteID int32) []hit {
		var hits []hit
		notes.match(channel, key, noteID, func(ch, k, id int32) { hits = append(hits, hit{ch, k, id}) })
		return hits
	}
	if got := collect(-1, -1, -1); len(got) != 2 {
		t.Fatalf("full wildcard matched %v", got)
	}
	if got := collect(1, 64, -1); len(got) != 1 || got[0].id != synthetic {
		t.Fatalf("key match %v", got)
	}
	if got := collect(-1, -1, 7); len(got) != 1 || got[0] != (hit{0, 60, 7}) {
		t.Fatalf("note ID match %v", got)
	}
	notes.noteOff(0, 60)
	if got := collect(-1, -1, -1); len(got) != 1 || got[0].key != 64 {
		t.Fatalf("after note off %v", got)
	}
}

func TestCLAPRawMIDI(t *testing.T) {
	var notes clapNotes
	on := clapRawMIDI(&notes, [3]byte{0x92, 60, 127}, 5)
	if !on.isEvent || on.event.Type != midi.EventTypeNoteOn || on.event.Channel != 2 || on.event.Note != 60 || on.event.Velocity != 1 || on.event.Offset != 5 || on.event.NoteID < 0 {
		t.Fatalf("note on %+v", on.event)
	}
	pressure := clapRawMIDI(&notes, [3]byte{0xA2, 60, 127}, 6)
	if pressure.event.Type != midi.EventTypePolyPressure || pressure.event.NoteID != on.event.NoteID || pressure.event.Value != 1 {
		t.Fatalf("poly pressure %+v", pressure.event)
	}
	// Note on with velocity 0 is a note off of the same note ID.
	off := clapRawMIDI(&notes, [3]byte{0x92, 60, 0}, 7)
	if off.event.Type != midi.EventTypeNoteOff || off.event.NoteID != on.event.NoteID || off.event.Velocity != 0 {
		t.Fatalf("note off %+v", off.event)
	}
	if again := clapRawMIDI(&notes, [3]byte{0x82, 60, 64}, 8); again.event.NoteID != midi.NoteIDNone {
		t.Fatalf("note off without note on has ID %d", again.event.NoteID)
	}

	cc := clapRawMIDI(&notes, [3]byte{0xB0, 1, 127}, 0)
	if !cc.isCtrl || cc.controller != 1 || cc.value != 1 {
		t.Fatalf("cc %+v", cc)
	}
	bend := clapRawMIDI(&notes, [3]byte{0xE1, 0x7F, 0x7F}, 0)
	if !bend.isCtrl || bend.controller != MIDIControllerPitchBend || bend.value != 1 || bend.event.Channel != 1 {
		t.Fatalf("pitch bend %+v", bend)
	}
	at := clapRawMIDI(&notes, [3]byte{0xD0, 64, 0}, 0)
	if at.controller != MIDIControllerAfterTouch || math.Abs(at.value-64.0/127) > 1e-12 {
		t.Fatalf("aftertouch %+v", at)
	}
	if pc := clapRawMIDI(&notes, [3]byte{0xC0, 3, 0}, 0); pc.program != 3 {
		t.Fatalf("program change %+v", pc)
	}
	if sys := clapRawMIDI(&notes, [3]byte{0xF8, 0, 0}, 0); sys.isEvent || sys.isCtrl || sys.program >= 0 {
		t.Fatalf("clock produced %+v", sys)
	}
}

func TestCLAPMIDI2(t *testing.T) {
	var notes clapNotes
	// MIDI 2.0 note on, group 0, channel 3, key 60, velocity 0x8000 with
	// the pitch 7.9 attribute at 60.5 (50 cents up).
	on := clapMIDI2(&notes, [4]uint32{0x4093_3C03, 0x8000_0000 | 60<<9 | 256}, 4)
	if !on.isEvent || on.event.Type != midi.EventTypeNoteOn || on.event.Channel != 3 || on.event.Note != 60 || on.event.Offset != 4 || on.event.NoteID < 0 {
		t.Fatalf("note on %+v", on.event)
	}
	if math.Abs(float64(on.event.Velocity)-32768.0/65535) > 1e-7 || on.event.Tuning != 50 {
		t.Fatalf("velocity %v tuning %v", on.event.Velocity, on.event.Tuning)
	}
	// Velocity 0 is still a note on.
	if soft := clapMIDI2(&notes, [4]uint32{0x4093_4000, 0}, 0); soft.event.Type != midi.EventTypeNoteOn || soft.event.Velocity <= 0 {
		t.Fatalf("velocity 0 %+v", soft.event)
	}
	// 32-bit poly pressure keeps its resolution.
	pressure := clapMIDI2(&notes, [4]uint32{0x40A3_3C00, 0x4000_0000}, 0)
	if pressure.event.Type != midi.EventTypePolyPressure || pressure.event.NoteID != on.event.NoteID || math.Abs(pressure.event.Value-0.25) > 1e-9 {
		t.Fatalf("poly pressure %+v", pressure.event)
	}
	// Per-note pitch bend: a quarter of the range up is 12 semitones.
	bend := clapMIDI2(&notes, [4]uint32{0x4063_3C00, 0xA000_0000}, 0)
	if bend.event.Type != midi.EventTypeNoteExpression || bend.event.ExpressionType != midi.NoteExpressionTuning ||
		bend.event.NoteID != on.event.NoteID || math.Abs(bend.event.Value-(0.5+12.0/240)) > 1e-9 {
		t.Fatalf("per-note bend %+v", bend.event)
	}
	// Registered per-note controller 74 is brightness.
	bright := clapMIDI2(&notes, [4]uint32{0x4003_3C4A, 0xFFFF_FFFF}, 0)
	if bright.event.ExpressionType != midi.NoteExpressionBrightness || bright.event.Value != 1 {
		t.Fatalf("brightness %+v", bright.event)
	}
	// Per-note messages for a key that is not sounding are dropped.
	if none := clapMIDI2(&notes, [4]uint32{0x4063_3D00, 0xA000_0000}, 0); none.isEvent {
		t.Fatalf("bend of a silent key %+v", none.event)
	}
	off := clapMIDI2(&notes, [4]uint32{0x4083_3C00, 0xFFFF_0000}, 9)
	if off.event.Type != midi.EventTypeNoteOff || off.event.NoteID != on.event.NoteID || off.event.Velocity != 1 {
		t.Fatalf("note off %+v", off.event)
	}

	cc := clapMIDI2(&notes, [4]uint32{0x40B0_0700, 0x8000_0000}, 0)
	if !cc.isCtrl || cc.controller != 7 || math.Abs(cc.value-0.5) > 1e-9 {
		t.Fatalf("cc %+v", cc)
	}
	chanBend := clapMIDI2(&notes, [4]uint32{0x40E1_0000, 0xFFFF_FFFF}, 0)
	if chanBend.controller != MIDIControllerPitchBend || chanBend.value != 1 || chanBend.event.Channel != 1 {
		t.Fatalf("pitch bend %+v", chanBend)
	}
	if at := clapMIDI2(&notes, [4]uint32{0x40D0_0000, 0}, 0); at.controller != MIDIControllerAfterTouch || at.value != 0 {
		t.Fatalf("channel pressure %+v", at)
	}
	if pc := clapMIDI2(&notes, [4]uint32{0x40C0_0000, 5 << 24}, 0); pc.program != 5 {
		t.Fatalf("program change %+v", pc)
	}
	// MIDI 1.0 in a universal packet goes through the MIDI 1.0 path.
	if one := clapMIDI2(&notes, [4]uint32{0x2090_407F}, 0); one.event.Type != midi.EventTypeNoteOn || one.event.Note != 64 || one.event.Velocity != 1 {
		t.Fatalf("MIDI 1.0 packet %+v", one.event)
	}
	// Utility and data messages are ignored.
	if util := clapMIDI2(&notes, [4]uint32{0x0010_0000}, 0); util.isEvent || util.isCtrl || util.program >= 0 {
		t.Fatalf("utility message produced %+v", util)
	}
}

func TestCLAPMIDIBytes(t *testing.T) {
	cases := []struct {
		event midi.Event
		want  [3]byte
	}{
		{midi.ControlChange(1, 7, 1), [3]byte{0xB1, 7, 127}},
		{midi.ChannelPressure(0, 0), [3]byte{0xD0, 0, 0}},
		{midi.PitchBend(2, 0), [3]byte{0xE2, 0x00, 0x40}},
		{midi.ProgramChange(0, 5), [3]byte{0xC0, 5, 0}},
	}
	for _, tc := range cases {
		got, ok := clapMIDIBytes(&tc.event)
		if !ok || got != tc.want {
			t.Errorf("%v -> %x, want %x", tc.event, got, tc.want)
		}
	}
	note := midi.NoteOn(0, 60, 1)
	if _, ok := clapMIDIBytes(&note); ok {
		t.Fatal("notes are CLAP note events, not MIDI bytes")
	}
}
