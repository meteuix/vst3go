package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/plugintest"
)

func render(t *testing.T, p *plugintest.Instance, main, key [][]float32) [][]float32 {
	t.Helper()
	out := plugintest.Silence(2, len(main[0]))
	if err := p.ProcessBuses([][][]float32{main, key}, [][][]float32{out}); err != nil {
		t.Fatal(err)
	}
	return out
}

func setup(t *testing.T) *plugintest.Instance {
	p := plugintest.New(t, sidechainPlugin{})
	p.Set(paramThreshold, -20)
	p.Set(paramRatio, 4)
	p.Set(paramKnee, 0)
	p.Set(paramAttack, 0.1)
	return p
}

func TestSidechainKeyDucksQuietMain(t *testing.T) {
	p := setup(t)
	n := 4800
	main := plugintest.Channels(2, plugintest.Constant(n, 0.05)) // -26 dB, under threshold
	key := plugintest.Channels(2, plugintest.Constant(n, 1))     // 0 dB: 20 dB over

	out := render(t, p, main, plugintest.Silence(2, n))
	if math.Abs(float64(out[0][n-1])-0.05) > 1e-6 {
		t.Fatalf("quiet key changed the signal: %v", out[0][n-1])
	}

	out = render(t, p, main, key)
	// 20 dB over at 4:1 is 15 dB of reduction on both channels.
	for ch := range out {
		if db := plugintest.DB(float64(out[ch][n-1]) / 0.05); math.Abs(db+15) > 0.2 {
			t.Fatalf("channel %d reduced by %.2f dB, want 15", ch, -db)
		}
	}
	reduction := p.OutputParameters()
	last := reduction[len(reduction)-1]
	if last.ParamID != paramReduction || math.Abs(last.Value-p.Normalized(paramReduction, 15)) > 0.01 {
		t.Fatalf("meter %+v, want 15 dB", last)
	}
}

func TestSidechainWithoutExternalKeyUsesMain(t *testing.T) {
	p := setup(t)
	p.Set(paramExternalKey, 0)
	n := 4800
	main := plugintest.Channels(2, plugintest.Constant(n, 1))
	out := render(t, p, main, plugintest.Silence(2, n))
	if db := plugintest.DB(float64(out[1][n-1])); math.Abs(db+15) > 0.2 {
		t.Fatalf("self-keyed reduction %.2f dB, want 15", -db)
	}
}

func TestSidechainMixAndMakeup(t *testing.T) {
	p := setup(t)
	p.Set(paramMakeup, 6)
	n := 4800
	main := plugintest.Channels(2, plugintest.Constant(n, 0.5))
	key := plugintest.Channels(2, plugintest.Constant(n, 1))
	out := render(t, p, main, plugintest.Silence(2, n))
	if db := plugintest.DB(float64(out[0][n-1]) / 0.5); math.Abs(db-6) > 0.05 {
		t.Fatalf("makeup %.2f dB, want 6", db)
	}

	p.Set(paramMix, 0)
	if out := render(t, p, main, key); out[0][n-1] != 0.5 {
		t.Fatalf("0%% mix output %v, want the dry 0.5", out[0][n-1])
	}
}

func TestSidechainDoesNotAllocate(t *testing.T) {
	p := setup(t)
	in := plugintest.Channels(2, plugintest.Sine(100, 48000, 512, 1))
	out := plugintest.Silence(2, 512)
	if allocs := p.AllocsPerProcess(20, in, out, nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
