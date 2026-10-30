package main

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

func TestVintageChorusDryAndDelayedWet(t *testing.T) {
	p := plugintest.New(t, vintageChorusPlugin{})
	p.Set(paramMix, 0)
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 4800, 0.5))
	out := p.Render(in, 2)
	for i := range out[0] {
		if out[0][i] != in[0][i] || out[1][i] != in[1][i] {
			t.Fatalf("0%% mix changed sample %d", i)
		}
	}

	// Fully wet, no modulation or feedback: the input delayed by 20 ms.
	p = plugintest.New(t, vintageChorusPlugin{})
	p.Set(paramMix, 100)
	p.Set(paramDepth, 0)
	p.Set(paramFeedback, 0)
	p.Set(paramSpread, 0)
	out = p.Render(plugintest.Channels(2, plugintest.Impulse(4800, 100)), 2)
	if at := plugintest.FirstNonZero(out[0], 0.5); at != 100+960 {
		t.Fatalf("wet impulse at %d, want %d", at, 100+960)
	}
}

func TestVintageChorusModulatesAndSpreads(t *testing.T) {
	p := plugintest.New(t, vintageChorusPlugin{})
	p.Set(paramMix, 100)
	p.Set(paramRate, 2)
	p.Set(paramDepth, 5)
	p.Set(paramVoices, 3)
	in := plugintest.Channels(2, plugintest.Sine(1000, 48000, 48000, 0.5))
	out := p.Render(in, 2)

	// Delay modulation shifts the pitch: zero crossings drift from 1 kHz
	// over 50 ms windows.
	lo, hi := math.Inf(1), math.Inf(-1)
	for start := 4800; start+2400 <= 48000; start += 2400 {
		f := plugintest.ZeroCrossingFrequency(out[0][start:start+2400], 48000)
		lo, hi = min(lo, f), max(hi, f)
	}
	if hi-lo < 5 {
		t.Errorf("frequency stayed within %.1f-%.1f Hz: no modulation", lo, hi)
	}

	// Spread pans the voices: the channels differ.
	diff := 0.0
	for i := 4800; i < 48000; i++ {
		diff += math.Abs(float64(out[0][i] - out[1][i]))
	}
	if diff/43200 < 0.01 {
		t.Errorf("channels are nearly identical (mean difference %v)", diff/43200)
	}
}

func TestVintageChorusDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, vintageChorusPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	steps := []float64{20, 30, 40}
	i := 0
	before := func() {
		// Changing delay and voices on the audio thread must not allocate.
		p.Set(paramDelay, steps[i%3])
		p.Set(paramVoices, float64(1+i%4))
		i++
	}
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), before); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
