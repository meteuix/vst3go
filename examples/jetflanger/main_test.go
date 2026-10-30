package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// combResponse returns the steady-state gain in dB of a parked flanger at
// a frequency.
func combResponse(t *testing.T, freq float64, negative bool) float64 {
	p := plugintest.New(t, jetFlangerPlugin{})
	p.Set(paramManualMode, 1)
	p.Set(paramManual, 50) // the 5 ms center delay
	p.Set(paramFeedback, 50)
	if negative {
		p.Set(paramNegativeFeedback, 1)
	}
	out := p.Render(plugintest.Channels(2, plugintest.Sine(freq, 48000, 48000, 0.25)), 2)
	return plugintest.DB(plugintest.RMS(out[0][24000:]) / (0.25 / math.Sqrt2))
}

func TestJetFlangerCombFilter(t *testing.T) {
	// 50% mix of the dry signal and a 5 ms feedback comb with gain g:
	// H = 0.5 + 0.5 z^-D / (1 - g z^-D). z^-D is +1 at multiples of 200 Hz
	// (1000 Hz) and -1 halfway between (1100 Hz).
	for _, c := range []struct {
		freq     float64
		negative bool
		want     float64 // dB
	}{
		{1000, false, plugintest.DB(0.5 + 0.5/(1-0.5))},          // +3.5 dB peak
		{1100, false, plugintest.DB(0.5 - 0.5/(1+0.5))},          // -15.6 dB notch
		{1000, true, plugintest.DB(0.5 + 0.5/(1+0.5))},           // -1.6 dB
		{1100, true, plugintest.DB(math.Abs(0.5 - 0.5/(1-0.5)))}, // |0.5 - 1|: -6 dB
	} {
		if got := combResponse(t, c.freq, c.negative); math.Abs(got-c.want) > 0.3 {
			t.Errorf("%v Hz, negative feedback %v: %.2f dB, want %.2f", c.freq, c.negative, got, c.want)
		}
	}
}

func TestJetFlangerSweeps(t *testing.T) {
	p := plugintest.New(t, jetFlangerPlugin{})
	p.Set(paramRate, 2)
	p.Set(paramMix, 50)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(1000, 48000, 48000, 0.25)), 2)
	lo, hi := math.Inf(1), math.Inf(-1)
	for start := 4800; start+1200 <= 48000; start += 1200 {
		db := plugintest.DB(plugintest.RMS(out[0][start : start+1200]))
		lo, hi = min(lo, db), max(hi, db)
	}
	if hi-lo < 6 {
		t.Errorf("level only moved %.1f dB: the comb is not sweeping", hi-lo)
	}
}

func TestJetFlangerDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, jetFlangerPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	i := 0
	before := func() {
		p.Set(paramDelay, float64(1+i%9))
		i++
	}
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), before); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
