package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/dsp/fft"
	"github.com/th-release/vst3go/pkg/plugintest"
)

// spectrum returns flat-top windowed amplitudes of x[:32768].
func spectrum(t *testing.T, x []float32) []float64 {
	t.Helper()
	const n = 32768
	f, _ := fft.NewReal(n)
	in := make([]float64, n)
	sum := 0.0
	for i := range in {
		p := 2 * math.Pi * float64(i) / n
		w := 0.21557895 - 0.41663158*math.Cos(p) + 0.277263158*math.Cos(2*p) -
			0.083578947*math.Cos(3*p) + 0.006947368*math.Cos(4*p)
		in[i] = float64(x[i]) * w
		sum += w
	}
	spec := make([]complex128, f.Bins())
	f.Forward(in, spec)
	amp := make([]float64, len(spec))
	for k, z := range spec {
		amp[k] = 2 * math.Hypot(real(z), imag(z)) / sum
	}
	return amp
}

func at(amp []float64, freq float64) float64 {
	c := int(math.Round(freq * 32768 / 48000))
	best := 0.0
	for k := c - 2; k <= c+2; k++ {
		best = math.Max(best, amp[k])
	}
	return best
}

func TestWavetableSynthSineAndSquare(t *testing.T) {
	p := plugintest.New(t, wavetablePlugin{})
	p.Set(paramDetune, 0)
	p.Note(0, 57, 1, 300, 0) // A3, 220 Hz, at sample 300
	out := p.RenderSilence(48000, 0, 2)
	if at := plugintest.FirstNonZero(out[0], 0); at < 300 || at > 302 {
		t.Fatalf("note starts at %d, want 300", at)
	}
	amp := spectrum(t, out[0][8000:])
	// Two oscillators at gain 1/2, sustain 80 %, volume 50 %.
	if a := at(amp, 220); math.Abs(a-0.4) > 0.01 {
		t.Errorf("sine at %.3f, want 0.4", a)
	}
	if a := at(amp, 660); a > 1e-3 {
		t.Errorf("sine has a third harmonic of %.4f", a)
	}

	p = plugintest.New(t, wavetablePlugin{})
	p.Set(paramDetune, 0)
	p.Set(paramPosition, 100)
	p.Note(0, 57, 1, 0, 0)
	amp = spectrum(t, p.RenderSilence(48000, 0, 2)[0][8000:])
	if a := at(amp, 660); math.Abs(a-0.4*4/(3*math.Pi)) > 0.01 {
		t.Errorf("square third harmonic %.4f, want %.4f", a, 0.4*4/(3*math.Pi))
	}
}

func TestWavetableSynthHighNotesDoNotAlias(t *testing.T) {
	p := plugintest.New(t, wavetablePlugin{})
	p.Set(paramDetune, 0)
	p.Set(paramPosition, 200.0/3) // sawtooth
	p.Note(0, 100, 1, 0, 0)       // 2637 Hz
	amp := spectrum(t, p.RenderSilence(48000, 0, 2)[0][8000:])
	f0 := 440 * math.Exp2(31.0/12)
	bin := 48000.0 / 32768
	fund, worst := at(amp, f0), 0.0
	for k := 10; k < len(amp); k++ {
		f := float64(k) * bin
		if math.Abs(f-math.Round(f/f0)*f0) > 8*bin {
			worst = math.Max(worst, amp[k])
		}
	}
	if db := 20 * math.Log10(worst/fund); db > -80 {
		t.Fatalf("alias at %.1f dB below the fundamental, want under -80", db)
	}
}

func TestWavetableSynthDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, wavetablePlugin{})
	out := plugintest.Silence(2, 512)
	allocs := p.AllocsPerProcess(20, nil, out, func() {
		p.Note(0, 60, 0.8, 10, 100)
		p.Note(0, 67, 0.8, 200, 0)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
