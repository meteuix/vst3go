package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func TestSynthPlaysSampleAccurateNotes(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Note(0, 69, 1, 300, 0) // A4
	out := p.RenderSilence(4800, 0, 2)
	if onset := plugintest.FirstNonZero(out[0], 1e-6); onset != 300 {
		t.Fatalf("note starts at %d, want 300", onset)
	}
	if f := plugintest.ZeroCrossingFrequency(out[0][1000:], 48000); math.Abs(f-440) > 2 {
		t.Fatalf("pitch %.1f Hz, want 440", f)
	}
}

func TestSynthPitchBendThroughMIDIMapping(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Note(0, 69, 1, 0, 0)
	if err := p.SendController(0, plugin.MIDIControllerPitchBend, 0, 1); err != nil {
		t.Fatal(err)
	}
	out := p.RenderSilence(9600, 0, 2)
	f := plugintest.ZeroCrossingFrequency(out[0][2400:], 48000)
	if f < 460 {
		t.Fatalf("pitch with full bend up = %.1f Hz, want above 440", f)
	}
}

func TestSynthProgramsAndState(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	if err := p.SetParameterPlain(plugin.ProgramParamID, 1); err != nil { // "Pluck"
		t.Fatal(err)
	}
	if got := p.Plain(paramRelease); math.Abs(got-80) > 0.5 {
		t.Fatalf("Pluck release = %v, want 80", got)
	}
	state, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	q := plugintest.New(t, synthPlugin{})
	if err := q.SetState(state); err != nil {
		t.Fatal(err)
	}
	if got := q.Plain(paramVolume); math.Abs(got-(-6)) > 0.01 {
		t.Fatalf("restored volume = %v, want -6", got)
	}
}

func TestSynthEditorBindings(t *testing.T) {
	plugintest.EditorBindings(t, synthPlugin{}, "ui")
}

func TestSynthEditorStatus(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Note(0, 60, 1, 0, 0)
	p.Note(0, 64, 1, 0, 0)
	p.RenderSilence(512, 0, 2)
	if err := p.EditorMessage("status", nil); err != nil {
		t.Fatal(err)
	}
	out := p.TakeEditorMessages()
	if len(out) != 1 || out[0].Name != "status" || string(out[0].Data) != `{"voices":2,"learnedCC":-1}` {
		t.Fatalf("status %+v", out)
	}
}
