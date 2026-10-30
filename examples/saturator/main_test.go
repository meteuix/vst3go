package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/dsp/fft"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/plugintest"
)

// aliasLevel returns, in dB relative to the fundamental, the strongest
// component of x below 18 kHz that is not a harmonic of f0. (The
// oversampler's transition band folds a little into 19-24 kHz by design.)
func aliasLevel(t *testing.T, x []float32, f0 float64) float64 {
	t.Helper()
	const n = 32768
	f, err := fft.NewReal(n)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float64, n)
	for i := range in {
		// Blackman-Harris: sidelobes below -92 dB.
		ph := 2 * math.Pi * float64(i) / n
		w := 0.35875 - 0.48829*math.Cos(ph) + 0.14128*math.Cos(2*ph) - 0.01168*math.Cos(3*ph)
		in[i] = float64(x[i]) * w
	}
	spec := make([]complex128, f.Bins())
	f.Forward(in, spec)
	bin := 48000.0 / n
	fund, worst := 0.0, 0.0
	for k, z := range spec[1:] {
		k++
		a := math.Hypot(real(z), imag(z))
		freq := float64(k) * bin
		h := math.Round(freq / f0)
		switch {
		case math.Abs(freq-f0) < 6*bin:
			fund = max(fund, a)
		case freq < 18000 && (h == 0 || math.Abs(freq-h*f0) > 6*bin):
			worst = max(worst, a)
		}
	}
	return 20 * math.Log10(worst/fund)
}

func TestSaturatorOversamplingRemovesAliases(t *testing.T) {
	render := func(choice float64) []float32 {
		p := plugintest.New(t, saturatorPlugin{})
		p.Set(paramOversampling, choice)
		p.Set(paramDrive, 24)
		proc := p.Processor().(*saturatorProcessor)
		if err := proc.SetActive(false); err != nil {
			t.Fatal(err)
		}
		if err := proc.SetActive(true); err != nil {
			t.Fatal(err)
		}
		out := p.Render(plugintest.Channels(2, plugintest.Sine(5003, 48000, 48000, 0.9)), 2)
		return out[0][8000 : 8000+32768]
	}
	// Hard tanh clipping of a 5 kHz tone: harmonics up to 35 kHz fold
	// back without oversampling.
	if db := aliasLevel(t, render(0), 5003); db < -40 {
		t.Fatalf("without oversampling aliases are at %.1f dB; the test signal is too clean", db)
	}
	if db := aliasLevel(t, render(3), 5003); db > -80 {
		t.Fatalf("8x oversampling leaves aliases at %.1f dB, want below -80", db)
	}
}

func TestSaturatorLatencyFollowsOversamplingOnActivation(t *testing.T) {
	p := plugintest.New(t, saturatorPlugin{})
	four := p.Latency()
	if four <= 0 || p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatalf("4x latency %d not announced", four)
	}
	p.Set(paramOversampling, 0)
	if p.Latency() != four {
		t.Fatal("latency changed while active")
	}
	proc := p.Processor().(*saturatorProcessor)
	_ = proc.SetActive(false)
	_ = proc.SetActive(true)
	if p.Latency() != 0 || p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatalf("latency %d without oversampling, want 0 and announced", p.Latency())
	}
}

func TestSaturatorDryMixIsTheDelayedInput(t *testing.T) {
	p := plugintest.New(t, saturatorPlugin{})
	p.Set(paramMix, 0)
	p.Set(paramOutput, 0)
	d := p.Latency()
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 9600, 0.5))
	out := p.Render(in, 2)
	// After the smoothing ramp of the first block.
	for i := 2048; i < 9600; i++ {
		if math.Abs(float64(out[0][i]-in[0][i-d])) > 1e-6 {
			t.Fatalf("sample %d: %v, want %v", i, out[0][i], in[0][i-d])
		}
	}
}

func TestSaturatorDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, saturatorPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(100, 48000, 512, 0.9))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
