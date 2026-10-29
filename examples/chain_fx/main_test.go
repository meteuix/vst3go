package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/plugintest"
)

func renderDB(t *testing.T, p *plugintest.Instance, amp float32) float64 {
	t.Helper()
	in := plugintest.Channels(2, plugintest.Sine(1000, 48000, 48000, amp))
	out := p.Render(in, 2)
	return plugintest.DB(plugintest.RMS(out[1][24000:]) / plugintest.RMS(in[1][24000:]))
}

func TestChainFXDynamicsChain(t *testing.T) {
	p := plugintest.New(t, chainFXPlugin{})
	p.Set(paramGateThreshold, -50)
	p.Set(paramCompThreshold, -20)
	p.Set(paramCompRatio, 4)

	// Between gate and compressor thresholds: untouched.
	if db := renderDB(t, p, 0.03); math.Abs(db) > 0.2 {
		t.Fatalf("-30 dB signal changed by %.2f dB", db)
	}
	// Above the compressor threshold: compressed.
	if db := renderDB(t, p, 0.9); db > -6 {
		t.Fatalf("loud signal reduced by only %.2f dB", db)
	}

	// Below the gate threshold a fresh (closed) gate stays shut: gated by
	// the gate's 80 dB range.
	p = plugintest.New(t, chainFXPlugin{})
	p.Set(paramGateThreshold, -50)
	if db := renderDB(t, p, 0.001); math.Abs(db+80) > 0.5 {
		t.Fatalf("-60 dB signal changed by %.2f dB, want -80", db)
	}
}

func TestChainFXSimpleChainAddsNoise(t *testing.T) {
	p := plugintest.New(t, chainFXPlugin{})
	p.Set(paramChainSelect, chainSimple)
	p.Set(paramNoiseAmount, 100)
	out := p.RenderSilence(48000, 2, 2)
	rmsL, rmsR := plugintest.RMS(out[0]), plugintest.RMS(out[1])
	if rmsL < maxNoiseLevel*0.05 || rmsL > maxNoiseLevel {
		t.Fatalf("noise RMS %g, want a level below %g", rmsL, maxNoiseLevel)
	}
	same := true
	for i := range out[0] {
		if out[0][i] != out[1][i] {
			same = false
			break
		}
	}
	if same || rmsR == 0 {
		t.Fatal("both channels carry the same noise")
	}

	// No noise: the simple chain only removes DC, and a 1 kHz tone passes.
	p.Set(paramNoiseAmount, 0)
	if db := renderDB(t, p, 0.001); math.Abs(db) > 0.1 {
		t.Fatalf("simple chain changed a tone by %.2f dB", db)
	}
}

func TestChainFXBypass(t *testing.T) {
	p := plugintest.New(t, chainFXPlugin{})
	p.Set(paramBypass, 1)
	in := plugintest.Channels(2, plugintest.Constant(1024, 0.5))
	out := p.Render(in, 2)
	if out[0][1023] != 0.5 || out[1][0] != 0.5 {
		t.Fatalf("bypass changed the signal: %v %v", out[0][1023], out[1][0])
	}
}

func TestChainFXDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, chainFXPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	v := 0.0
	if allocs := p.AllocsPerProcess(20, in, out, func() {
		v = 1 - v
		_ = p.Automate(paramChainSelect, 100, v)
	}); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
