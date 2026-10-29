package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/dsp/fft"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/plugintest"
)

// dominant returns the strongest frequency of x[:16384].
func dominant(t *testing.T, x []float32) float64 {
	t.Helper()
	const n = 16384
	f, _ := fft.NewReal(n)
	in := make([]float64, n)
	for i := range in {
		in[i] = float64(x[i]) * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/n))
	}
	spec := make([]complex128, f.Bins())
	f.Forward(in, spec)
	mag := func(k int) float64 { return math.Log(math.Hypot(real(spec[k]), imag(spec[k])) + 1e-30) }
	best := 1
	for k := 1; k < len(spec)-1; k++ {
		if mag(k) > mag(best) {
			best = k
		}
	}
	a, b, c := mag(best-1), mag(best), mag(best+1)
	return (float64(best) + 0.5*(a-c)/(a-2*b+c)) * 48000 / n
}

func TestPitchShifterModesShiftATone(t *testing.T) {
	for _, tc := range []struct {
		mode, semis float64
	}{{modeSpectral, 12}, {modeSpectral, -5}, {modeGranular, 7}, {modeGranular, -12}} {
		p := plugintest.New(t, pitchShifterPlugin{})
		p.Set(paramMode, tc.mode)
		p.Set(paramSemitones, tc.semis)
		out := p.Render(plugintest.Channels(2, plugintest.Sine(440, 48000, 48000, 0.5)), 2)
		want := 440 * math.Exp2(tc.semis/12)
		for ch := range 2 {
			if got := dominant(t, out[ch][24000:]); math.Abs(got-want) > want*0.003 {
				t.Errorf("mode %v %+v st channel %d: %.2f Hz, want %.2f", tc.mode, tc.semis, ch, got, want)
			}
		}
	}
}

func TestPitchShifterFineTuning(t *testing.T) {
	p := plugintest.New(t, pitchShifterPlugin{})
	p.Set(paramSemitones, 1)
	p.Set(paramFine, -50)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(440, 48000, 48000, 0.5)), 2)
	want := 440 * math.Exp2(0.5/12)
	if got := dominant(t, out[0][24000:]); math.Abs(got-want) > 1 {
		t.Fatalf("%.2f Hz, want %.2f", got, want)
	}
}

func TestPitchShifterKeepsTimingAligned(t *testing.T) {
	// Unshifted, both engines and the dry path line a tone up at the
	// reported latency, which grows with the sample rate so the longest
	// granular window still fits.
	for _, tc := range []struct {
		rate    float64
		latency int
	}{{44100, 2048}, {48000, 2048}, {96000, 4096}, {192000, 8192}} {
		for _, mode := range []float64{modeSpectral, modeGranular} {
			p := plugintest.New(t, pitchShifterPlugin{}, plugin.OfflineSetup{SampleRate: tc.rate})
			p.Set(paramMode, mode)
			p.Set(paramWindow, maxWindow*1000)
			d := p.Latency()
			if d != tc.latency {
				t.Fatalf("%v Hz: latency %d, want %d", tc.rate, d, tc.latency)
			}
			n := int(tc.rate / 2)
			in := plugintest.Channels(2, plugintest.Sine(1000, tc.rate, n, 0.5))
			out := p.Render(in, 2)
			var errSum, sum float64
			for i := n / 2; i < n; i++ {
				e := float64(out[0][i] - in[0][i-d])
				errSum += e * e
				sum += float64(in[0][i]) * float64(in[0][i])
			}
			if rel := math.Sqrt(errSum / sum); rel > 0.05 {
				t.Errorf("%v Hz mode %v: output differs from the input delayed by %d (relative error %.3f)", tc.rate, mode, d, rel)
			}
		}
	}
}

func TestPitchShifterDoesNotAllocate(t *testing.T) {
	for _, mode := range []float64{modeSpectral, modeGranular} {
		p := plugintest.New(t, pitchShifterPlugin{})
		p.Set(paramMode, mode)
		p.Set(paramSemitones, 5)
		in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
		if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
			t.Fatalf("mode %v: %v allocations per block", mode, allocs)
		}
	}
}
