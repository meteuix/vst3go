package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func TestAutoParamsAssignsIDsInRegistrationOrder(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	proc := p.Processor().(*autoParamsProcessor)
	want := map[string]uint32{
		"Bypass": 0, "Mix": 1, "Input Gain": 2, "Output Gain": 3,
		"Threshold": 4, "Ratio": 5, "Attack": 6, "Release": 7, "Knee": 8, "Auto Gain": 9,
		"Band 1 Enable": 10, "Band 1 Type": 14, "Band 3 Type": 24, "Warmth": 25,
	}
	for name, id := range want {
		if got, ok := proc.params.GetID(name); !ok || got != id {
			t.Errorf("%s: id %d (found %v), want %d", name, got, ok, id)
		}
	}
	if proc.bands[2].kind != 24 || proc.warmth != 25 {
		t.Fatalf("resolved ids: band 3 type %d, warmth %d", proc.bands[2].kind, proc.warmth)
	}
}

func steadyGainDB(t *testing.T, p *plugintest.Instance, freq float64, amp float32) float64 {
	t.Helper()
	in := plugintest.Channels(2, plugintest.Sine(freq, 48000, 48000, amp))
	out := p.Render(in, 2)
	return plugintest.DB(plugintest.RMS(out[0][24000:]) / plugintest.RMS(in[0][24000:]))
}

func TestAutoParamsDefaultsAreTransparentBelowThreshold(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	if db := steadyGainDB(t, p, 1000, 0.01); math.Abs(db) > 0.1 {
		t.Fatalf("gain %.2f dB for a -40 dB signal, want 0", db)
	}
}

func TestAutoParamsCompressesLoudSignals(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	p.Set(proc(p).threshold, -30)
	p.Set(proc(p).ratio, 4)
	// A 0.5 peak sine is about -9 dBFS RMS: 21 dB over, 15.75 dB reduced
	// at 4:1 once the detector settles (it follows the peak, so a little
	// more).
	db := steadyGainDB(t, p, 1000, 0.5)
	if db > -14 || db < -20 {
		t.Fatalf("gain %.2f dB, want about -16", db)
	}

	p.Set(proc(p).autoGain, 1)
	withMakeup := steadyGainDB(t, p, 1000, 0.5)
	if want := db + 30*0.75/2; math.Abs(withMakeup-want) > 0.5 {
		t.Fatalf("auto gain: %.2f dB, want %.2f", withMakeup, want)
	}
}

func TestAutoParamsEQBand(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	band := proc(p).bands[0]
	p.Set(band.freq, 1000)
	p.Set(band.gain, 12)
	if db := steadyGainDB(t, p, 1000, 0.01); math.Abs(db-12) > 0.2 {
		t.Fatalf("bell +12 dB at its frequency: %.2f dB", db)
	}
	if db := steadyGainDB(t, p, 100, 0.01); math.Abs(db) > 0.5 {
		t.Fatalf("bell at 1 kHz changed 100 Hz by %.2f dB", db)
	}
	p.Set(band.enable, 0)
	if db := steadyGainDB(t, p, 1000, 0.01); math.Abs(db) > 0.1 {
		t.Fatalf("disabled band still applies %.2f dB", db)
	}
	p.Set(band.enable, 1)
	p.Set(band.kind, bandHighPass)
	p.Set(band.q, 0.7)
	if db := steadyGainDB(t, p, 100, 0.01); db > -30 {
		t.Fatalf("high pass at 1 kHz leaves 100 Hz at %.2f dB", db)
	}
}

func TestAutoParamsBypassAndMix(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	p.Set(proc(p).inputGain, 6)
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 2048, 0.01))

	p.Set(proc(p).mix, 0)
	out := p.Render(in, 2)
	for i := range out[0] {
		if math.Abs(float64(out[0][i]-in[0][i])) > 1e-7 {
			t.Fatalf("mix 0: sample %d = %v, want dry %v", i, out[0][i], in[0][i])
		}
	}

	p.Set(proc(p).mix, 100)
	p.Set(proc(p).bypass, 1)
	out = p.Render(in, 2)
	for i := range out[1] {
		if out[1][i] != in[1][i] {
			t.Fatalf("bypass: sample %d = %v, want %v", i, out[1][i], in[1][i])
		}
	}
}

func TestAutoParamsDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, autoParamsPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	v := 0.0
	if allocs := p.AllocsPerProcess(20, in, out, func() {
		v = 1 - v
		_ = p.Automate(proc(p).bands[1].freq, 100, v)
	}); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}

func proc(p *plugintest.Instance) *autoParamsProcessor {
	return p.Processor().(*autoParamsProcessor)
}
