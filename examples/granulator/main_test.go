package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func TestGranulatorDelaysByPosition(t *testing.T) {
	p := plugintest.New(t, granulatorPlugin{})
	p.Set(paramMix, 100)
	p.Set(paramPosition, 300)
	p.Set(paramSize, 100) // two grains overlap at 20 per second
	// A 100 ms burst at the start, then silence.
	in := plugintest.Silence(2, 48000)
	copy(in[0], plugintest.Sine(500, 48000, 4800, 0.5))
	copy(in[1], in[0])
	out := p.Render(in, 2)
	// Every grain reads 300 ms behind the input, so the burst comes back
	// between 300 and 400 ms and nothing sounds outside that.
	if peak := plugintest.Peak(out[0][:14398]); peak > 1e-6 {
		t.Errorf("output %.4f before the delay", peak)
	}
	if rms := plugintest.RMS(out[0][14400:19200]); math.Abs(plugintest.DB(rms/plugintest.RMS(in[0][:4800]))) > 3 {
		t.Errorf("delayed burst at %.1f dB of the input", plugintest.DB(rms/plugintest.RMS(in[0][:4800])))
	}
	if peak := plugintest.Peak(out[0][19204:]); peak > 1e-6 {
		t.Errorf("output %.4f after the burst has passed", peak)
	}
}

func TestGranulatorFreezeHoldsTheBuffer(t *testing.T) {
	for _, freeze := range []bool{false, true} {
		p := plugintest.New(t, granulatorPlugin{})
		p.Set(paramMix, 100)
		p.Render(plugintest.Channels(2, plugintest.Sine(440, 48000, 48000, 0.5)), 2)
		if freeze {
			p.Set(paramFreeze, 1)
		}
		out := p.RenderSilence(96000, 2, 2)
		rms := plugintest.RMS(out[0][48000:])
		if freeze && rms < 0.2 {
			t.Errorf("frozen output faded to %.3f RMS", rms)
		}
		if !freeze && rms > 1e-6 {
			t.Errorf("unfrozen output %.6f RMS long after the input stopped", rms)
		}
	}
}

func TestGranulatorPitchAndSpread(t *testing.T) {
	p := plugintest.New(t, granulatorPlugin{})
	p.Set(paramMix, 100)
	p.Set(paramPitch, 12)
	p.Set(paramSpread, 100)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(300, 48000, 96000, 0.5)), 2)
	if f := plugintest.ZeroCrossingFrequency(out[0][48000:], 48000); math.Abs(f-600) > 15 {
		t.Errorf("grains at %.1f Hz, want 600", f)
	}
	var diff float64
	for i := 48000; i < 96000; i++ {
		diff += math.Abs(float64(out[0][i] - out[1][i]))
	}
	if diff/48000 < 0.01 {
		t.Errorf("spread grains are not panned: mean |L-R| %.4f", diff/48000)
	}
}

func TestGranulatorDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, granulatorPlugin{})
	p.Set(paramDensity, 100)
	p.Set(paramSpray, 100)
	p.Set(paramPitchSpray, 5)
	p.Set(paramJitter, 50)
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	if allocs := p.AllocsPerProcess(50, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
