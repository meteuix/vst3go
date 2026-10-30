package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/dsp/fft"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
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
		best = max(best, amp[k])
	}
	return best
}

// play renders note 57 (220 Hz) held for a second with the given settings
// and fast decays, so the analysed part sits at the sustain levels.
func play(t *testing.T, set map[uint32]float64) []float64 {
	t.Helper()
	p := plugintest.New(t, fmPlugin{})
	p.Set(paramDecay, 0.02)
	p.Set(paramModDecay, 0.02)
	for id, v := range set {
		p.Set(id, v)
	}
	p.Note(0, 57, 1, 0, 0)
	return spectrum(t, p.RenderSilence(48000, 0, 2)[0][12000:])
}

func TestFMSynthCarrierAlone(t *testing.T) {
	amp := play(t, map[uint32]float64{paramLevel2: 0})
	// Velocity 1, sustain 70 %, volume 50 %.
	if a := at(amp, 220); math.Abs(a-0.35) > 0.005 {
		t.Errorf("carrier %.4f, want 0.35", a)
	}
	if a := at(amp, 440); a > 1e-4 {
		t.Errorf("unmodulated carrier has a harmonic of %.5f", a)
	}
}

func TestFMSynthSidebandsFollowBessel(t *testing.T) {
	// Modulator at 5x, level 50 % with the mod envelope sustaining at
	// 40 %: index 0.5 * 0.4 * 8 = 1.6 radians. The carrier keeps J0 and
	// the first sidebands (220 ± 1100 Hz) carry J1.
	amp := play(t, map[uint32]float64{paramRatio2: 5, paramLevel2: 50})
	const beta = 1.6
	if a, want := at(amp, 220), 0.35*math.Abs(math.J0(beta)); math.Abs(a-want) > 0.005 {
		t.Errorf("carrier %.4f, want %.4f", a, want)
	}
	for _, f := range []float64{1320, 880} {
		if a, want := at(amp, f), 0.35*math.Abs(math.J1(beta)); math.Abs(a-want) > 0.005 {
			t.Errorf("sideband at %v Hz %.4f, want %.4f", f, a, want)
		}
	}
}

func TestFMSynthAlgorithms(t *testing.T) {
	// Organ sums the four operators at a quarter each.
	amp := play(t, map[uint32]float64{
		paramAlgorithm: algOrgan, paramRatio2: 2, paramRatio3: 3, paramRatio4: 4,
		paramLevel2: 100, paramLevel3: 100, paramLevel4: 100,
	})
	for h := 1; h <= 4; h++ {
		if a := at(amp, 220*float64(h)); math.Abs(a-0.35/4) > 0.003 {
			t.Errorf("organ harmonic %d at %.4f, want %.4f", h, a, 0.35/4)
		}
	}
	// In Pairs, operator 3 is a second carrier: at level 100 with
	// nothing modulating it, the two carriers are averaged.
	amp = play(t, map[uint32]float64{
		paramAlgorithm: algPairs, paramLevel2: 0, paramRatio3: 3, paramLevel3: 100,
	})
	if a := at(amp, 660); math.Abs(a-0.175) > 0.003 {
		t.Errorf("pairs second carrier %.4f, want 0.175", a)
	}
	// In Stack the same operator 3 is a modulator two steps away from the
	// output, so with operator 2 silent nothing of it is heard.
	amp = play(t, map[uint32]float64{paramLevel2: 0, paramRatio3: 3, paramLevel3: 100})
	if a := at(amp, 660); a > 1e-4 {
		t.Errorf("stack leaks operator 3: %.5f", a)
	}
}

func TestFMSynthDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, fmPlugin{})
	p.Set(paramFeedback, 50)
	out := plugintest.Silence(2, 512)
	allocs := p.AllocsPerProcess(20, nil, out, func() {
		p.Note(0, 60, 0.8, 10, 100)
		p.Note(0, 64, 0.8, 200, 0)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
