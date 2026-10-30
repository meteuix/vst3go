package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func normalizedGain(db float64) float64 { return (db - minGainDB) / (maxGainDB - minGainDB) }

func TestGainWithoutSmoothingJumps(t *testing.T) {
	p := plugintest.New(t, gainPlugin{})
	p.Set(paramGain, 0)
	if err := p.Automate(paramGain, 1000, normalizedGain(-12)); err != nil {
		t.Fatal(err)
	}
	out := p.Render(plugintest.Channels(2, plugintest.Constant(2048, 0.5)), 2)
	want := float32(0.5 * math.Pow(10, -12.0/20))
	if out[0][999] != 0.5 || math.Abs(float64(out[1][1000]-want)) > 1e-6 {
		t.Fatalf("out[999]=%v out[1000]=%v, want 0.5 then %v", out[0][999], out[1][1000], want)
	}

	meters := p.OutputParameters()
	if len(meters) == 0 || meters[len(meters)-1].ParamID != paramOutputLevel {
		t.Fatalf("meter output = %+v", meters)
	}
	// -12 dB below 0.5 is about -18 dBFS on a -60..0 dB meter.
	if got := meters[len(meters)-1].Value; math.Abs(got-(60-18.02)/60) > 0.01 {
		t.Fatalf("meter = %v, want about %v", got, (60-18.02)/60)
	}
}

func TestGainSmoothingRampsOverSmoothingTime(t *testing.T) {
	p := plugintest.New(t, gainPlugin{})
	p.Set(paramGain, 0)
	p.Set(paramSmoothingEnabled, 1)
	p.Set(paramSmoothingTime, 10) // 480 samples at 48 kHz
	p.Render(plugintest.Channels(2, plugintest.Constant(512, 0.5)), 2)

	if err := p.Automate(paramGain, 1000, normalizedGain(-12)); err != nil {
		t.Fatal(err)
	}
	out := p.Render(plugintest.Channels(2, plugintest.Constant(2048, 0.5)), 2)
	target := 0.5 * math.Pow(10, -12.0/20)

	if out[0][999] != 0.5 {
		t.Fatalf("gain changed before the automation point: %v", out[0][999])
	}
	// Halfway through the ramp the gain is -6 dB (the ramp is linear in dB).
	if mid := float64(out[0][1000+239]); math.Abs(mid-0.5*math.Pow(10, -6.0/20)) > 0.01 {
		t.Fatalf("mid-ramp sample = %v, want about %v", mid, 0.5*math.Pow(10, -6.0/20))
	}
	for i := 1000; i < 1000+479; i++ {
		if !(out[0][i] < out[0][i-1]) {
			t.Fatalf("ramp not monotonic at %d: %v then %v", i, out[0][i-1], out[0][i])
		}
	}
	for i := 1000 + 480; i < 2048; i++ {
		if math.Abs(float64(out[0][i])-target) > 1e-6 || out[1][i] != out[0][i] {
			t.Fatalf("sample %d = %v/%v after the ramp, want %v", i, out[0][i], out[1][i], target)
		}
	}
}

func TestGainBypass(t *testing.T) {
	p := plugintest.New(t, gainPlugin{})
	p.Set(paramGain, 12)
	p.Set(paramBypass, 1)
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 1024, 0.3))
	out := p.Render(in, 2)
	for ch := range out {
		for i := range out[ch] {
			if out[ch][i] != in[ch][i] {
				t.Fatalf("bypass changed channel %d sample %d: %v != %v", ch, i, out[ch][i], in[ch][i])
			}
		}
	}
}

func TestGainDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, gainPlugin{})
	p.Set(paramSmoothingEnabled, 1)
	in := plugintest.Channels(2, plugintest.Constant(512, 0.5))
	out := plugintest.Silence(2, 512)
	gain := 0.0
	allocs := p.AllocsPerProcess(20, in, out, func() {
		gain = 1 - gain
		_ = p.Automate(paramGain, 100, gain)
		_ = p.Automate(paramSmoothingTime, 300, gain)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
