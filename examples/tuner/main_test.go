package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// pluck is a harmonic-rich tone at freq, like a plucked string.
func pluck(freq float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		v := 0.0
		for h := 1; h <= 10; h++ {
			v += math.Sin(2*math.Pi*freq*float64(h)*float64(i)/48000) / float64(h*h)
		}
		x[i] = float32(0.4 * v)
	}
	return x
}

// last returns the final value the processor reported for id.
func last(p *plugintest.Instance, id uint32) (float64, bool) {
	v, ok := 0.0, false
	for _, c := range p.OutputParameters() {
		if c.ParamID == id {
			v, ok = c.Value, true
		}
	}
	return v, ok
}

func TestTunerReportsNoteAndCents(t *testing.T) {
	for _, tc := range []struct {
		freq, reference float64
		note            int
		cents           float64
	}{
		{110 * math.Exp2(12.0/1200), 440, 45, 12}, // A2 +12 ct
		{329.63, 440, 64, 0},                      // E4
		{196 * math.Exp2(-30.0/1200), 440, 55, -30},
		{432, 432, 69, 0},
	} {
		p := plugintest.New(t, tunerPlugin{})
		p.Set(paramReference, tc.reference)
		in := plugintest.Channels(2, pluck(tc.freq, 24000))
		out := p.Render(in, 2)
		for i := range in[0] {
			if out[0][i] != in[0][i] || out[1][i] != in[1][i] {
				t.Fatalf("audio changed at sample %d", i)
			}
		}
		note, _ := last(p, paramNote)
		cents, _ := last(p, paramCents)
		freq, _ := last(p, paramFrequency)
		if got := int(math.Round(note * 127)); got != tc.note {
			t.Errorf("%.2f Hz: note %d, want %d", tc.freq, got, tc.note)
		}
		if got := cents*100 - 50; math.Abs(got-tc.cents) > 1 {
			t.Errorf("%.2f Hz: %.2f cents, want %.0f", tc.freq, got, tc.cents)
		}
		if got := freq * 2000; math.Abs(got-tc.freq) > tc.freq*0.001 {
			t.Errorf("%.2f Hz: frequency %.2f", tc.freq, got)
		}
	}
}

func TestTunerReportsNothingForSilence(t *testing.T) {
	p := plugintest.New(t, tunerPlugin{})
	p.Render(plugintest.Silence(2, 9600), 2)
	if f, ok := last(p, paramFrequency); !ok || f != 0 {
		t.Fatalf("frequency %v for silence", f)
	}
	if _, ok := last(p, paramNote); ok {
		t.Fatal("note reported for silence")
	}
}

func TestTunerDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, tunerPlugin{})
	in := plugintest.Channels(2, pluck(220, 512))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
