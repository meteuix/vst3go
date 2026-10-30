package filter

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/th-release/vst3go/pkg/dsp/fft"
)

// impulseBands returns the impulse response of each band.
func impulseBands(c *Crossover, n int) [][]float32 {
	bands := make([][]float32, c.Bands())
	for b := range bands {
		bands[b] = make([]float32, n)
	}
	in := make([]float32, n)
	in[0] = 1
	c.Process(in, bands, 0)
	return bands
}

// at evaluates the response of an impulse response at exactly hz.
func at(h []float32, hz, rate float64) complex128 {
	var z complex128
	for i, v := range h {
		z += complex(float64(v), 0) * cmplx.Exp(complex(0, -2*math.Pi*hz/rate*float64(i)))
	}
	return z
}

// responses returns the frequency response of each band and of their sum
// from an impulse, on n/2+1 bins.
func responses(t *testing.T, c *Crossover, n int) ([][]complex128, []complex128) {
	t.Helper()
	bands := make([][]float32, c.Bands())
	for b := range bands {
		bands[b] = make([]float32, n)
	}
	in := make([]float32, n)
	in[0] = 1
	c.Process(in, bands, 0)
	f, err := fft.NewReal(n)
	if err != nil {
		t.Fatal(err)
	}
	sum := make([]float64, n)
	out := make([][]complex128, len(bands))
	x := make([]float64, n)
	for b, band := range bands {
		for i, v := range band {
			x[i] = float64(v)
			sum[i] += float64(v)
		}
		out[b] = make([]complex128, f.Bins())
		f.Forward(x, out[b])
	}
	total := make([]complex128, f.Bins())
	f.Forward(sum, total)
	return out, total
}

func db(z complex128) float64 { return 20 * math.Log10(cmplx.Abs(z)) }

func TestCrossoverBandsSumFlat(t *testing.T) {
	const rate, n = 48000.0, 1 << 15
	c, err := NewCrossover(rate, 1, 200, 2000, 8000)
	if err != nil {
		t.Fatal(err)
	}
	bands, total := responses(t, c, n)
	bin := func(hz float64) int { return int(math.Round(hz * n / rate)) }
	for k := 1; k < len(total)-1; k++ {
		if d := db(total[k]); math.Abs(d) > 0.01 {
			t.Fatalf("sum at %.0f Hz is %.4f dB", float64(k)*rate/n, d)
		}
	}
	// Linkwitz-Riley: -6 dB at each crossover for both neighbours (the
	// next crossover, two octaves away, adds a few hundredths of a dB).
	h := impulseBands(c, n)
	c.Reset()
	for i, f := range []float64{200, 2000, 8000} {
		for _, b := range []int{i, i + 1} {
			if d := db(at(h[b][:8192], f, rate)); math.Abs(d+6.02) > 0.05 {
				t.Errorf("band %d at %v Hz: %.2f dB, want -6.02", b, f, d)
			}
		}
	}
	// 24 dB per octave: three octaves past a crossover is about -72 dB
	// (−6 at the crossover plus about −66 more for the 4th-order slope).
	if d := db(bands[0][bin(1600)]); d > -65 {
		t.Errorf("low band three octaves above 200 Hz: %.1f dB", d)
	}
	if d := db(bands[3][bin(1000)]); d > -65 {
		t.Errorf("top band three octaves below 8 kHz: %.1f dB", d)
	}
	// Mid bands are flat between their crossovers.
	if d := db(bands[1][bin(632)]); math.Abs(d) > 0.5 {
		t.Errorf("band 1 in its middle: %.2f dB", d)
	}
}

func TestCrossoverSweepsAndClamps(t *testing.T) {
	c, err := NewCrossover(48000, 2, 500, 5000)
	if err != nil {
		t.Fatal(err)
	}
	c.SetFrequency(0, 9000) // past its neighbour
	if f := c.Frequency(0); f >= c.Frequency(1) {
		t.Fatalf("crossover 0 moved to %v, past crossover 1 at %v", f, c.Frequency(1))
	}
	c.SetFrequency(1, 1e6)
	if f := c.Frequency(1); f > 48000*0.49 {
		t.Fatalf("crossover 1 at %v Hz", f)
	}
	c.SetFrequency(0, 300)
	c.SetFrequency(1, 3000)
	c.Reset()
	_, total := responses(t, c, 1<<14)
	for k := 1; k < len(total)-1; k++ {
		if math.Abs(db(total[k])) > 0.01 {
			t.Fatalf("sum not flat after moving the crossovers")
		}
	}
}

func TestCrossoverRejectsBadFrequencies(t *testing.T) {
	for _, freqs := range [][]float64{nil, {0}, {2000, 1000}, {30000}} {
		if _, err := NewCrossover(48000, 1, freqs...); err == nil {
			t.Errorf("%v accepted", freqs)
		}
	}
}

func TestCrossoverDoesNotAllocate(t *testing.T) {
	c, _ := NewCrossover(48000, 2, 150, 1500, 6000)
	in := make([]float32, 256)
	bands := [][]float32{make([]float32, 256), make([]float32, 256), make([]float32, 256), make([]float32, 256)}
	allocs := testing.AllocsPerRun(50, func() {
		c.SetFrequency(1, 1700)
		c.Process(in, bands, 0)
		c.Process(in, bands, 1)
		c.SetFrequency(1, 1500)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
