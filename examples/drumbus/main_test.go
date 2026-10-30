package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func lastMeter(p *plugintest.Instance, id uint32) float64 {
	v := -1.0
	for _, c := range p.OutputParameters() {
		if c.ParamID == id {
			v = c.Value
		}
	}
	return v
}

func TestDrumBusQuietSignalPaths(t *testing.T) {
	in := plugintest.Channels(2, plugintest.Sine(200, 48000, 9600, 0.01)) // -40 dB: under both thresholds

	p := plugintest.New(t, drumBusPlugin{})
	p.Set(paramParallelMix, 0)
	out := p.Render(in, 2)
	for i := 4800; i < 9600; i++ {
		if math.Abs(float64(out[0][i]-in[0][i])) > 1e-7 {
			t.Fatalf("sample %d: %v, want the untouched %v", i, out[0][i], in[0][i])
		}
	}

	// At 100% the parallel path adds the input with its 10 dB makeup.
	p = plugintest.New(t, drumBusPlugin{})
	p.Set(paramParallelMix, 100)
	out = p.Render(in, 2)
	want := 1 + math.Pow(10, parallelMakeupDB/20)
	if ratio := plugintest.RMS(out[1][4800:]) / plugintest.RMS(in[1][4800:]); math.Abs(ratio-want) > 0.01 {
		t.Fatalf("output/input %v, want 1 + 10 dB = %v", ratio, want)
	}
}

func TestDrumBusGlueCompresses(t *testing.T) {
	p := plugintest.New(t, drumBusPlugin{})
	p.Set(paramParallelMix, 0)
	p.Set(paramGlueThreshold, -20)
	p.Set(paramGlueRatio, 4)
	out := p.Render(plugintest.Channels(2, plugintest.Constant(48000, 1)), 2)
	// 20 dB over at 4:1 (past the 4 dB soft knee): 15 dB of reduction.
	if db := plugintest.DB(float64(out[0][47999])); math.Abs(db+15) > 0.1 {
		t.Fatalf("glue output %.2f dB, want -15", db)
	}
	if got, want := lastMeter(p, paramGlueReduction), p.Normalized(paramGlueReduction, 15); math.Abs(got-want) > 0.01 {
		t.Fatalf("glue meter %v, want %v", got, want)
	}
}

func TestDrumBusParallelDetectorIgnoresLowEnd(t *testing.T) {
	kick := plugintest.Channels(2, plugintest.Sine(40, 48000, 48000, 0.5))
	reduction := func(hpf float64) float64 {
		p := plugintest.New(t, drumBusPlugin{})
		p.Set(paramParallelHPF, hpf)
		p.Render(kick, 2)
		return lastMeter(p, paramParallelReduction)
	}
	open, filtered := reduction(20), reduction(500)
	if open-filtered < 0.2 { // 8 dB on the 0-40 dB meter
		t.Fatalf("parallel meter %v with the HPF at 20 Hz, %v at 500 Hz: the HPF should keep 40 Hz out", open, filtered)
	}
}

func TestDrumBusTransientAttack(t *testing.T) {
	hit := make([]float32, 9600)
	for i := 4800; i < len(hit); i++ {
		hit[i] = float32(0.1 * math.Sin(2*math.Pi*100*float64(i-4800)/48000))
	}
	peak := func(attack float64) float64 {
		p := plugintest.New(t, drumBusPlugin{})
		p.Set(paramParallelMix, 0)
		p.Set(paramTransientAttack, attack)
		out := p.Render(plugintest.Channels(2, hit), 2)
		return plugintest.PeakDB(out[0][4800:5280])
	}
	if punchy, flat := peak(100), peak(0); punchy-flat < 3 {
		t.Fatalf("Transient Attack +100%%: onset %.2f dB, neutral %.2f dB", punchy, flat)
	}
}

func TestDrumBusDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, drumBusPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(60, 48000, 512, 0.8))
	i := 0
	before := func() {
		p.Set(paramParallelHPF, float64(50+i%100))
		i++
	}
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), before); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
