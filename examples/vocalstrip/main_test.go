package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// bare returns a strip with every section off.
func bare(t *testing.T) *plugintest.Instance {
	p := plugintest.New(t, vocalStripPlugin{})
	for _, id := range []uint32{paramGateOn, paramCompOn, paramEQOn, paramLimiterOn} {
		p.Set(id, 0)
	}
	return p
}

func TestVocalStripBypassedSectionsOnlyDelay(t *testing.T) {
	p := bare(t)
	if p.Latency() != 240 || p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatalf("latency %d, want 5 ms = 240 samples, announced", p.Latency())
	}
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 4800, 0.9))
	out := p.Render(in, 2)
	for i := 240; i < 4800; i++ {
		if out[0][i] != in[0][i-240] || out[1][i] != in[1][i-240] {
			t.Fatalf("sample %d: %v, want the input delayed by 240", i, out[0][i])
		}
	}
}

func TestVocalStripLimiterHoldsTheCeiling(t *testing.T) {
	p := bare(t)
	p.Set(paramLimiterOn, 1)
	p.Set(paramCeiling, -6)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(440, 48000, 9600, 1)), 2)
	if peak := plugintest.PeakDB(out[0]); peak > -6+1e-4 {
		t.Fatalf("limited peak %.3f dB, over the -6 dB ceiling", peak)
	}
	// Same timing as the bypass: the delayed sine's first peak lands at
	// 240 + 27 samples, limited to the ceiling.
	if at := plugintest.FirstNonZero(out[0], 0.1); at < 240 || at > 250 {
		t.Fatalf("limited signal starts at %d, want just after 240", at)
	}
}

func TestVocalStripGateAndCompressor(t *testing.T) {
	p := bare(t)
	p.Set(paramGateOn, 1)
	p.Set(paramGateThreshold, -40)
	p.Set(paramGateRange, -30)
	quiet := p.Render(plugintest.Channels(2, plugintest.Sine(440, 48000, 9600, 0.003)), 2) // -50 dB
	if db := plugintest.PeakDB(quiet[0][4800:]) - plugintest.DB(0.003); math.Abs(db+30) > 0.1 {
		t.Fatalf("gated by %.2f dB, want the -30 dB range", db)
	}

	p = bare(t)
	p.Set(paramCompOn, 1)
	p.Set(paramCompThreshold, -20)
	p.Set(paramCompRatio, 4)
	out := p.Render(plugintest.Channels(2, plugintest.Constant(24000, 1)), 2)
	// 20 dB over at 4:1, past the 2 dB knee: 15 dB.
	if db := plugintest.DB(float64(out[1][23999])); math.Abs(db+15) > 0.1 {
		t.Fatalf("compressed to %.2f dB, want -15", db)
	}
	changes := p.OutputParameters()
	var meter float64
	for _, c := range changes {
		if c.ParamID == paramCompReduction {
			meter = c.Value
		}
	}
	if math.Abs(meter-p.Normalized(paramCompReduction, 15)) > 0.01 {
		t.Fatalf("compressor meter %v, want 15 dB", meter)
	}
}

func TestVocalStripEQ(t *testing.T) {
	gainAt := func(freq float64) float64 {
		p := bare(t)
		p.Set(paramEQOn, 1)
		p.Set(paramMidFreq, 2500)
		p.Set(paramMidGain, 9)
		p.Set(paramHighGain, -6)
		in := plugintest.Channels(2, plugintest.Sine(freq, 48000, 24000, 0.1))
		out := p.Render(in, 2)
		return plugintest.DB(plugintest.RMS(out[0][12000:]) / plugintest.RMS(in[0][12000:]))
	}
	if g := gainAt(2500); math.Abs(g-9) > 0.2 {
		t.Errorf("mid band at its center: %.2f dB, want +9", g)
	}
	if g := gainAt(18000); math.Abs(g+6) > 0.5 {
		t.Errorf("high shelf at 18 kHz: %.2f dB, want -6", g)
	}
	if g := gainAt(100); math.Abs(g) > 0.5 {
		t.Errorf("100 Hz with a flat low shelf: %.2f dB, want 0", g)
	}
}

func TestVocalStripDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, vocalStripPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.9))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
