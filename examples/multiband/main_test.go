package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

const sr = 48000

// level renders a stereo sine and returns the output RMS in dB after the
// filters and compressors settle.
func level(p *plugintest.Instance, freq float64, amplitude float32) float64 {
	out := p.Render(plugintest.Channels(2, plugintest.Sine(freq, sr, sr, amplitude)), 2)
	return plugintest.DB(plugintest.RMS(out[0][sr/2:]) * math.Sqrt2)
}

func lastOutput(p *plugintest.Instance, id uint32) float64 {
	v := math.NaN()
	for _, c := range p.OutputParameters() {
		if c.ParamID == id {
			v = c.Value
		}
	}
	return v
}

func TestMultibandUncompressedIsTransparent(t *testing.T) {
	for _, freq := range []float64{50, 200, 900, 3000, 9000} {
		p := plugintest.New(t, multibandPlugin{})
		if db := level(p, freq, 0.5); math.Abs(db-plugintest.DB(0.5)) > 0.02 {
			t.Errorf("%v Hz came out at %.3f dB, want %.3f", freq, db, plugintest.DB(0.5))
		}
	}
}

func TestMultibandCompressesOnlyItsBand(t *testing.T) {
	p := plugintest.New(t, multibandPlugin{})
	p.Set(bandParam(0, bandThreshold), -30)
	p.Set(bandParam(0, bandRatio), 10)
	// With Low/Mid at 1 kHz, 60 Hz lies entirely in the low band (the mid
	// band passes it at -98 dB), so it comes out as the band compressor
	// alone would leave it. Near a crossover the uncompressed neighbour's
	// share would dominate the heavily compressed band.
	p.Set(paramLowMid, 1000)
	c := dynamics.NewCompressor(sr)
	c.SetThreshold(-30)
	c.SetRatio(10)
	c.SetAttack(0.01)
	c.SetRelease(0.1)
	x := plugintest.Sine(60, sr, sr, 1)
	c.ProcessBuffer(x, x)
	want := plugintest.DB(plugintest.RMS(x[sr/2:]) * math.Sqrt2)
	if want > -20 {
		t.Fatalf("reference compressor left %.2f dB", want)
	}
	if db := level(p, 60, 1); math.Abs(db-want) > 0.1 {
		t.Fatalf("low band at %.2f dB, want %.2f", db, want)
	}
	if r := lastOutput(p, bandParam(0, bandReduction)); math.Abs(r-p.Normalized(bandParam(0, bandReduction), c.GetGainReduction())) > 0.02 {
		t.Fatalf("low reduction meter %v, want %.1f dB", r, c.GetGainReduction())
	}
	if r := lastOutput(p, bandParam(2, bandReduction)); r != 0 {
		t.Fatalf("high band reports %v reduction", r)
	}

	p = plugintest.New(t, multibandPlugin{})
	p.Set(bandParam(0, bandThreshold), -30)
	p.Set(bandParam(0, bandRatio), 10)
	if db := level(p, 8000, 1); math.Abs(db) > 0.1 {
		t.Fatalf("an 8 kHz tone was changed to %.2f dB by the low band", db)
	}
}

func TestMultibandMakeupAndOutput(t *testing.T) {
	p := plugintest.New(t, multibandPlugin{})
	p.Set(bandParam(2, bandMakeup), 6)
	p.Set(paramOutput, -3)
	if db := level(p, 10000, 0.25); math.Abs(db-(plugintest.DB(0.25)+3)) > 0.1 {
		t.Fatalf("10 kHz at %.2f dB, want +3 dB", db)
	}
}

func TestMultibandCrossoverFollowsItsParameters(t *testing.T) {
	// With Low/Mid at 1 kHz a 500 Hz tone falls in the low band, so only
	// the low compressor touches it.
	p := plugintest.New(t, multibandPlugin{})
	p.Set(paramLowMid, 1000)
	p.Set(bandParam(1, bandThreshold), -40)
	p.Set(bandParam(1, bandRatio), 20)
	if db := level(p, 300, 1); db < -1 {
		t.Fatalf("300 Hz below a 1 kHz crossover was compressed to %.2f dB by the mid band", db)
	}
}

func TestMultibandDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, multibandPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, sr, 256, 0.8))
	out := plugintest.Silence(2, 256)
	if allocs := p.AllocsPerProcess(20, in, out, func() {
		p.Set(paramLowMid, 300)
		p.Set(bandParam(1, bandThreshold), -20)
	}); allocs != 0 {
		t.Fatalf("%v allocations per process", allocs)
	}
}
