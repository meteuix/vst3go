package main

import (
	"math"
	"testing"
	"time"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func TestConvReverbImpulseIsTheGeneratedResponse(t *testing.T) {
	p := plugintest.New(t, convReverbPlugin{})
	p.Set(paramMix, 100)
	proc := p.Processor().(*convReverbProcessor)
	ir := proc.built.Load().ir
	// A 10 ms pre-delay at 48 kHz.
	if at := plugintest.FirstNonZero(ir[0], 0); at != 480 {
		t.Fatalf("response starts at %d, want 480", at)
	}
	out := p.Render(plugintest.Channels(2, plugintest.Impulse(48000, 0)), 2)
	// Past the mix ramp of the first block the output is the response.
	for ch := range 2 {
		for i := 1024; i < 48000; i++ {
			if math.Abs(float64(out[ch][i]-ir[ch][i])) > 1e-5 {
				t.Fatalf("channel %d sample %d: %v, want %v", ch, i, out[ch][i], ir[ch][i])
			}
		}
	}
}

// level returns the RMS in dB of 50 ms of x starting at second t.
func level(x []float32, t float64) float64 {
	start := int(t * 48000)
	return plugintest.DB(plugintest.RMS(x[start : start+2400]))
}

func TestConvReverbDecayAndDamping(t *testing.T) {
	p := plugintest.New(t, convReverbPlugin{})
	p.Set(paramDecay, 1)
	p.Set(paramDamping, 0)
	p.Set(paramPreDelay, 0)
	waitForBuild(t, p, 1)
	ir := p.Processor().(*convReverbProcessor).built.Load().ir[0]
	// 60 dB per second: about 24 dB between 0.1 s and 0.5 s.
	if drop := level(ir, 0.1) - level(ir, 0.5); math.Abs(drop-24) > 3 {
		t.Errorf("dropped %.1f dB from 0.1 to 0.5 s, want 24", drop)
	}
	// Undamped, both bands decay together: the tail is as bright as the
	// onset.
	if hf := highFraction(ir[:4800]) / highFraction(ir[24000:28800]); math.Abs(hf-1) > 0.2 {
		t.Errorf("undamped tail changed colour: high-band share fell %.2fx", hf)
	}

	// Full damping: the high band decays in 15% of the time, so the tail
	// is darker than the onset.
	p.Set(paramDamping, 100)
	waitForBuild(t, p, 1)
	ir = p.Processor().(*convReverbProcessor).built.Load().ir[0]
	if hf := highFraction(ir[:4800]) / highFraction(ir[24000:28800]); hf < 2.5 {
		t.Errorf("damped tail is not darker: high-band share fell only %.1fx", hf)
	}
}

// highFraction returns the share of energy in the sample-to-sample
// difference, a rough high-frequency measure.
func highFraction(x []float32) float64 {
	var e, d float64
	for i := 1; i < len(x); i++ {
		e += float64(x[i]) * float64(x[i])
		v := float64(x[i] - x[i-1])
		d += v * v
	}
	return d / e
}

// waitForBuild processes blocks until the builder publishes a response for
// the given decay.
func waitForBuild(t *testing.T, p *plugintest.Instance, decay float64) {
	t.Helper()
	proc := p.Processor().(*convReverbProcessor)
	want := proc.requested()
	deadline := time.Now().Add(5 * time.Second)
	for proc.built.Load().settings != want {
		if time.Now().After(deadline) {
			t.Fatalf("no response built for decay %v", decay)
		}
		p.Render(plugintest.Silence(2, 512), 2)
		time.Sleep(5 * time.Millisecond)
	}
	p.Render(plugintest.Silence(2, fadeSamples+512), 2)
}

func TestConvReverbSwapsResponsesWithoutAGap(t *testing.T) {
	p := plugintest.New(t, convReverbPlugin{})
	p.Set(paramMix, 100)
	// A steady noise input keeps the reverb sounding through the swap.
	noise := make([]float32, 512)
	state := uint32(7)
	for i := range noise {
		state = state*1664525 + 1013904223
		noise[i] = float32(state>>8)/float32(1<<24) - 0.5
	}
	in := plugintest.Channels(2, noise)
	for range 200 {
		p.Render(in, 2)
	}
	before := p.Processor().(*convReverbProcessor).built.Load()
	p.Set(paramDecay, 0.5)
	deadline := time.Now().Add(5 * time.Second)
	for p.Processor().(*convReverbProcessor).built.Load() == before {
		if time.Now().After(deadline) {
			t.Fatal("response not rebuilt")
		}
		out := p.Render(in, 2)
		if rms := plugintest.RMS(out[0]); rms < 0.05 {
			t.Fatalf("reverb dropped to %v while rebuilding", rms)
		}
		time.Sleep(2 * time.Millisecond)
	}
	for range 20 {
		out := p.Render(in, 2)
		if rms := plugintest.RMS(out[0]); rms < 0.05 {
			t.Fatalf("reverb dropped to %v during the crossfade", rms)
		}
	}
}

func TestConvReverbDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, convReverbPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
