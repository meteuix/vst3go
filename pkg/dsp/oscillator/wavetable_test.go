package oscillator

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fft"
)

const rate = 48000.0

// amplitudes returns the amplitude spectrum of x (flat-top window, so
// amplitudes between bins read true; power-of-two length) with bin width
// rate/len(x).
func amplitudes(t *testing.T, x []float32) []float64 {
	t.Helper()
	n := len(x)
	f, err := fft.NewReal(n)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float64, n)
	sum := 0.0
	for i := range in {
		p := 2 * math.Pi * float64(i) / float64(n)
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

// peakNear returns the largest amplitude within 2 bins of freq.
func peakNear(amp []float64, freq, binHz float64) float64 {
	c := int(math.Round(freq / binHz))
	best := 0.0
	for k := max(0, c-2); k <= min(len(amp)-1, c+2); k++ {
		best = max(best, amp[k])
	}
	return best
}

func render(w *Wavetable, n int) []float32 {
	x := make([]float32, n)
	w.Process(x)
	return x
}

func TestWavetableSineIsPure(t *testing.T) {
	bank, err := NewWavetableBank(BasicShapes())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWavetable(bank, rate)
	w.SetFrequency(1000)
	amp := amplitudes(t, render(w, 32768))
	bin := rate / 32768
	if a := peakNear(amp, 1000, bin); math.Abs(a-1) > 0.01 {
		t.Fatalf("fundamental %.4f, want 1", a)
	}
	for h := 2; h <= 20; h++ {
		if a := peakNear(amp, 1000*float64(h), bin); a > 1e-4 {
			t.Errorf("harmonic %d at %.1f dB", h, 20*math.Log10(a))
		}
	}
}

func TestWavetableSawHasNoAliases(t *testing.T) {
	bank, _ := NewWavetableBank(BasicShapes())
	w := NewWavetable(bank, rate)
	w.SetPosition(2.0 / 3) // sawtooth
	for _, f0 := range []float64{110, 1234.5, 4567.8, 9000} {
		w.SetFrequency(f0)
		w.Reset()
		amp := amplitudes(t, render(w, 32768))
		bin := rate / 32768
		// Harmonic 1/h amplitudes of a sawtooth (peak 1) up to Nyquist.
		if a := peakNear(amp, f0, bin); math.Abs(a-2/math.Pi) > 0.02 {
			t.Errorf("%.1f Hz: fundamental %.3f, want %.3f", f0, a, 2/math.Pi)
		}
		// Everything off the harmonic series is an alias.
		worst := 0.0
		for k := 1; k < len(amp)-1; k++ {
			f := float64(k) * bin
			h := math.Round(f / f0)
			if math.Abs(f-h*f0) > 8*bin {
				worst = max(worst, amp[k])
			}
		}
		if db := 20 * math.Log10(worst); db > -80 {
			t.Errorf("%.1f Hz: alias at %.1f dB, want below -80", f0, db)
		}
	}
}

func TestWavetableMorphsBetweenFrames(t *testing.T) {
	bank, _ := NewWavetableBank(BasicShapes())
	w := NewWavetable(bank, rate)
	w.SetFrequency(100)
	// Position 1 is the square: odd harmonics at 4/(πh).
	w.SetPosition(1)
	amp := amplitudes(t, render(w, 32768))
	bin := rate / 32768
	if a := peakNear(amp, 300, bin); math.Abs(a-4/(3*math.Pi)) > 0.02 {
		t.Errorf("square third harmonic %.3f, want %.3f", a, 4/(3*math.Pi))
	}
	if a := peakNear(amp, 200, bin); a > 1e-3 {
		t.Errorf("square second harmonic %.4f, want 0", a)
	}
	// Halfway between the sawtooth (frame 2) and the square (frame 3):
	// the second harmonic is half the sawtooth's 1/π.
	w.SetPosition(2.5 / 3)
	w.Reset()
	amp = amplitudes(t, render(w, 32768))
	if a := peakNear(amp, 200, bin); math.Abs(a-0.5/math.Pi) > 0.01 {
		t.Errorf("morphed second harmonic %.3f, want %.3f", a, 0.5/math.Pi)
	}
}

func TestWavetableBankAcceptsAnyFrameLength(t *testing.T) {
	sine := make([]float32, 600)
	for i := range sine {
		sine[i] = float32(0.25 * math.Sin(2*math.Pi*float64(i)/600))
	}
	bank, err := NewWavetableBank([][]float32{sine})
	if err != nil {
		t.Fatal(err)
	}
	w := NewWavetable(bank, rate)
	w.SetFrequency(500)
	amp := amplitudes(t, render(w, 16384))
	// Normalized to a peak of 1.
	if a := peakNear(amp, 500, rate/16384); math.Abs(a-1) > 0.01 {
		t.Fatalf("fundamental %.4f, want 1", a)
	}
	if _, err := NewWavetableBank(nil); err == nil {
		t.Error("empty bank accepted")
	}
	if _, err := NewWavetableBank([][]float32{{1}}); err == nil {
		t.Error("one-sample frame accepted")
	}
}

func TestWavetableDoesNotAllocate(t *testing.T) {
	bank, _ := NewWavetableBank(BasicShapes())
	w := NewWavetable(bank, rate)
	buf := make([]float32, 512)
	allocs := testing.AllocsPerRun(20, func() {
		w.SetFrequency(440)
		w.SetPosition(0.3)
		w.Process(buf)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestOperatorPhaseModulationSidebands(t *testing.T) {
	// Carrier at 1 kHz modulated by a 3 kHz sine with index β: sidebands
	// at 1000+3000n Hz with amplitudes |J_n(β)|.
	const beta = 1.5
	carrier, modulator := NewOperator(rate), NewOperator(rate)
	modulator.SetRatio(3)
	x := make([]float32, 32768)
	for i := range x {
		x[i] = float32(carrier.Next(1000, beta*modulator.Next(1000, 0)))
	}
	amp := amplitudes(t, x)
	bin := rate / 32768
	for n, f := range map[int]float64{0: 1000, 1: 4000, -1: 2000, 2: 7000, -2: 5000} {
		want := math.Abs(math.Jn(n, beta))
		if a := peakNear(amp, f, bin); math.Abs(a-want) > 0.01 {
			t.Errorf("sideband %d at %.0f Hz: %.4f, want %.4f", n, f, a, want)
		}
	}
}

func TestOperatorFeedbackAddsHarmonics(t *testing.T) {
	o := NewOperator(rate)
	x := make([]float32, 32768)
	for i := range x {
		x[i] = float32(o.Next(500, 0))
	}
	if a := peakNear(amplitudes(t, x), 1000, rate/32768); a > 1e-4 {
		t.Fatalf("sine operator has a second harmonic of %.5f", a)
	}
	o.Reset()
	o.SetFeedback(1)
	for i := range x {
		x[i] = float32(o.Next(500, 0))
	}
	if a := peakNear(amplitudes(t, x), 1000, rate/32768); a < 0.1 {
		t.Fatalf("feedback second harmonic %.4f, want a sawtooth-like spectrum", a)
	}
}
