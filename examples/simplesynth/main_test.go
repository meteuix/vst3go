package main

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/midi"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

func TestSynthNoteIsSampleAccurateAndInTune(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Note(0, 69, 1, 700, 0) // A4 at sample 700, mid-block
	out := p.RenderSilence(48000, 0, 2)

	// The sine starts at phase 0, so sample 700 is 0 and 701 the first
	// non-zero sample.
	if at := plugintest.FirstNonZero(out[0], 0); at != 701 {
		t.Fatalf("note starts at %d, want 701", at)
	}
	if f := plugintest.ZeroCrossingFrequency(out[0][4800:], 48000); math.Abs(f-440) > 0.5 {
		t.Fatalf("frequency %.2f Hz, want 440", f)
	}
	for i := range out[0] {
		if out[1][i] != out[0][i] {
			t.Fatalf("right channel differs at %d", i)
		}
	}
	// Sustain 70 % times volume 80 % at full velocity.
	if peak := plugintest.Peak(out[0][24000:]); math.Abs(peak-0.7*0.8) > 0.01 {
		t.Fatalf("sustain level %.3f, want %.3f", peak, 0.7*0.8)
	}
}

func TestSynthNoteOffReleasesToSilence(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Set(paramRelease, 0.05)
	p.Note(0, 60, 1, 0, 4800)
	out := p.RenderSilence(48000, 0, 2)
	if plugintest.Peak(out[0][4000:4800]) < 0.3 {
		t.Fatal("note not sounding before its note-off")
	}
	// 50 ms release time constant: well under -60 dB after 0.5 s.
	if peak := plugintest.Peak(out[0][4800+24000:]); peak > 0.001 {
		t.Fatalf("still sounding after release: %f", peak)
	}
}

func TestSynthPolyphonyAndSustainPedal(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Set(paramRelease, 0.02)
	if err := p.SendController(0, int16(midi.CCSustain), 0, 1); err != nil {
		t.Fatal(err)
	}
	for i, note := range []uint8{60, 64, 67} {
		p.Note(0, note, 1, i*10, 2400)
	}
	if err := p.SendController(0, int16(midi.CCSustain), 24000, 0); err != nil {
		t.Fatal(err)
	}
	out := p.RenderSilence(48000, 0, 2)

	proc := p.Processor().(*synthProcessor)
	if n := proc.allocator.GetActiveVoiceCount(); n != 0 {
		t.Fatalf("%d voices still active after the pedal lifted", n)
	}
	// While the pedal is down the chord keeps sounding at its sustain level
	// (three voices: louder than one).
	held := plugintest.Peak(out[0][12000:24000])
	if held < 0.8 {
		t.Fatalf("pedal did not hold the chord: peak %f", held)
	}
	if peak := plugintest.Peak(out[0][36000:]); peak > 0.001 {
		t.Fatalf("chord still sounding after the pedal lifted: %f", peak)
	}
}

func TestSynthDoesNotAllocateWhileProcessing(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	out := plugintest.Silence(2, 512)
	allocs := p.AllocsPerProcess(20, nil, out, func() {
		p.Note(0, 60, 0.8, 10, 100)
		p.Note(0, 72, 0.8, 200, 0)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
