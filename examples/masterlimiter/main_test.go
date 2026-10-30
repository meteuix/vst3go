package main

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

func TestMasterLimiterNeverExceedsCeiling(t *testing.T) {
	p := plugintest.New(t, masterLimiterPlugin{})
	p.Set(paramCeiling, -1)
	ceiling := math.Pow(10, -1.0/20)

	// +12 dB peaks: a hot sine plus single-sample spikes.
	in := plugintest.Channels(2, plugintest.Sine(997, 48000, 48000, 4))
	for i := 1000; i < len(in[0]); i += 3001 {
		in[0][i], in[1][i+1] = 4, -4
	}
	out := p.Render(in, 2)
	for ch := range out {
		if peak := plugintest.Peak(out[ch]); peak > ceiling+1e-6 {
			t.Fatalf("channel %d peaked at %.4f, over the %.4f ceiling", ch, peak, ceiling)
		}
	}
	reduction := p.OutputParameters()
	var gr float64
	for _, c := range reduction {
		if c.ParamID == paramReduction {
			gr = c.Value
		}
	}
	if gr < p.Normalized(paramReduction, 12) {
		t.Fatalf("reduction meter %v, want at least the 13 dB the sine needs", gr)
	}
}

func TestMasterLimiterLeavesQuietSignalAlone(t *testing.T) {
	p := plugintest.New(t, masterLimiterPlugin{})
	latency := p.Latency()
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 9600, 0.5))
	out := p.Render(in, 2)
	for i := latency; i < 9600; i++ {
		if math.Abs(float64(out[0][i]-in[0][i-latency])) > 1e-6 {
			t.Fatalf("sample %d changed: %v, want %v", i, out[0][i], in[0][i-latency])
		}
	}
}

func TestMasterLimiterLookaheadSetsLatencyOnActivation(t *testing.T) {
	p := plugintest.New(t, masterLimiterPlugin{})
	if p.Latency() != 240 {
		t.Fatalf("latency %d, want 5 ms = 240 samples", p.Latency())
	}
	if p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatal("latency not announced")
	}

	p.Set(paramLookahead, 1)
	if p.Latency() != 240 {
		t.Fatal("Lookahead changed the latency while active")
	}
	proc := p.Processor().(*masterLimiterProcessor)
	if err := proc.SetActive(false); err != nil {
		t.Fatal(err)
	}
	if err := proc.SetActive(true); err != nil {
		t.Fatal(err)
	}
	if p.Latency() != 48 || p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatalf("after reactivation latency %d, want 48 and announced", p.Latency())
	}
	out := p.Render(plugintest.Channels(2, plugintest.Impulse(1000, 100)), 2)
	if at := plugintest.FirstNonZero(out[1], 0); at != 148 {
		t.Fatalf("impulse came out at %d, want 148", at)
	}
}

func TestMasterLimiterDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, masterLimiterPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(100, 48000, 512, 2))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
