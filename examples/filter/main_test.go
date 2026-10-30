package main

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

// gainDB renders a sine at freq through p and returns the steady-state gain.
func gainDB(t *testing.T, p *plugintest.Instance, freq float64) float64 {
	t.Helper()
	in := plugintest.Channels(2, plugintest.Sine(freq, 48000, 24000, 0.5))
	out := p.Render(in, 2)
	return plugintest.DB(plugintest.RMS(out[0][12000:]) / plugintest.RMS(in[0][12000:]))
}

func TestFilterModes(t *testing.T) {
	cases := []struct {
		name         string
		mode         float64
		freq         float64
		minDB, maxDB float64
	}{
		{"lowpass passes lows", typeLowpass, 100, -0.5, 0.5},
		{"lowpass cuts highs", typeLowpass, 8000, math.Inf(-1), -30},
		{"highpass cuts lows", typeHighpass, 100, math.Inf(-1), -30},
		{"highpass passes highs", typeHighpass, 8000, -0.5, 0.5},
		{"bandpass is unity at cutoff", typeBandpass, 1000, -0.5, 0.5},
		{"bandpass cuts away from cutoff", typeBandpass, 8000, math.Inf(-1), -15},
		{"notch removes the cutoff", typeNotch, 1000, math.Inf(-1), -30},
		{"notch passes away from cutoff", typeNotch, 8000, -0.5, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := plugintest.New(t, filterPlugin{})
			p.Set(paramFilterType, tc.mode)
			p.Set(paramCutoff, 1000)
			p.Set(paramResonance, 2)
			if tc.mode == typeLowpass || tc.mode == typeHighpass {
				p.Set(paramResonance, math.Sqrt2/2)
			}
			if db := gainDB(t, p, tc.freq); db < tc.minDB || db > tc.maxDB {
				t.Fatalf("gain %.2f dB, want %.1f..%.1f", db, tc.minDB, tc.maxDB)
			}
		})
	}
}

func TestFilterMixZeroIsDry(t *testing.T) {
	p := plugintest.New(t, filterPlugin{})
	p.Set(paramCutoff, 100)
	p.Set(paramMix, 0)
	in := plugintest.Channels(2, plugintest.Sine(5000, 48000, 4096, 0.5))
	out := p.Render(in, 2)
	for i := 2048; i < 4096; i++ {
		if math.Abs(float64(out[1][i]-in[1][i])) > 1e-6 {
			t.Fatalf("sample %d = %v, want dry %v", i, out[1][i], in[1][i])
		}
	}
}

func TestFilterDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, filterPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	v := 0.0
	if allocs := p.AllocsPerProcess(20, in, out, func() {
		v = 1 - v
		_ = p.Automate(paramCutoff, 100, v)
	}); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
