package main

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

const sr = 48000

func newCompressor(t *testing.T) *plugintest.Instance {
	p := plugintest.New(t, masterCompressorPlugin{})
	p.Set(paramThreshold, -20)
	p.Set(paramRatio, 4)
	p.Set(paramKnee, 0)
	p.Set(paramAttack, 0.1)
	return p
}

// step is silence followed by full scale from sample at.
func step(n, at int) [][]float32 {
	x := plugintest.Constant(n, 1)
	clear(x[:at])
	return plugintest.Channels(2, x)
}

func TestMasterCompressorReportsConstantLatency(t *testing.T) {
	p := newCompressor(t)
	if p.Latency() != 480 {
		t.Fatalf("latency %d, want 10 ms = 480 samples", p.Latency())
	}
	if p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatal("latency not announced")
	}
	p.Set(paramLookahead, 0)
	out := p.Render(plugintest.Channels(2, plugintest.Impulse(2000, 100)), 2)
	if at := plugintest.FirstNonZero(out[0], 0); at != 100+480 {
		t.Fatalf("impulse came out at %d, want %d", at, 100+480)
	}
	if p.Latency() != 480 || p.TakeRestarts() != 0 {
		t.Fatal("Lookahead changed the latency")
	}
}

func TestMasterCompressorLookaheadReducesBeforeTheTransient(t *testing.T) {
	const at = 2000
	transientGain := func(lookaheadMs float64) float64 {
		p := newCompressor(t)
		p.Set(paramAttack, 5) // 90% of the way in 240 samples
		p.Set(paramLookahead, lookaheadMs)
		out := p.Render(step(4800, at), 2)
		return plugintest.DB(float64(out[0][at+480]))
	}
	// 20 dB over at 4:1 settles at -15 dB.
	if db := transientGain(10); math.Abs(db+15) > 0.5 {
		t.Fatalf("with 10 ms lookahead the first loud sample is at %.2f dB, want -15", db)
	}
	if db := transientGain(0); db < -0.1 {
		t.Fatalf("without lookahead the first loud sample is already at %.2f dB", db)
	}
}

func TestMasterCompressorAutoMakeupAndKeyHPF(t *testing.T) {
	p := newCompressor(t)
	p.Set(paramAutoMakeup, 1)
	out := p.Render(plugintest.Channels(2, plugintest.Constant(9600, 0.01)), 2) // below threshold
	if want := autoMakeup(-20, 4); math.Abs(plugintest.DB(float64(out[1][9599])/0.01)-want) > 0.01 {
		t.Fatalf("auto makeup %.2f dB, want %.2f", plugintest.DB(float64(out[1][9599])/0.01), want)
	}

	rumble := plugintest.Channels(2, plugintest.Sine(25, sr, 48000, 1))
	p = newCompressor(t)
	if gr := plugintest.PeakDB(p.Render(rumble, 2)[0][24000:]); gr > -10 {
		t.Fatalf("unfiltered rumble only compressed to %.2f dB", gr)
	}
	p = newCompressor(t)
	p.Set(paramKeyHPF, 500)
	if peak := plugintest.PeakDB(p.Render(rumble, 2)[0][24000:]); peak < -0.5 {
		t.Fatalf("key HPF still let 25 Hz rumble compress to %.2f dB", peak)
	}
}

func TestMasterCompressorMeters(t *testing.T) {
	p := newCompressor(t)
	p.Render(plugintest.Channels(2, plugintest.Constant(9600, 1)), 2)
	var reduction, peak float64
	for _, c := range p.OutputParameters() {
		switch c.ParamID {
		case paramReduction:
			reduction = c.Value
		case paramOutputPeak:
			peak = c.Value
		}
	}
	if math.Abs(reduction-p.Normalized(paramReduction, 15)) > 0.01 {
		t.Fatalf("reduction meter %v, want 15 dB", reduction)
	}
	// The 2 ms lookahead clamps the gain before the delayed onset arrives,
	// so the output never peaks above the compressed -15 dB.
	if math.Abs(peak-p.Normalized(paramOutputPeak, -15)) > 0.01 {
		t.Fatalf("peak meter %v, want -15 dB", peak)
	}
}

func TestMasterCompressorDoesNotAllocate(t *testing.T) {
	p := newCompressor(t)
	p.Set(paramKeyHPF, 100)
	in := plugintest.Channels(2, plugintest.Sine(100, sr, 512, 1))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
