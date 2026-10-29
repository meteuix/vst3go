package resample

import (
	"math"
	"testing"
)

func sineAt(freq, rate float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / rate))
	}
	return x
}

func roundTrip(t *testing.T, o *Oversampler, in []float32, block int) []float32 {
	t.Helper()
	out := make([]float32, len(in))
	for start := 0; start < len(in); start += block {
		end := min(start+block, len(in))
		o.Down(o.Up(in[start:end]), out[start:end])
	}
	return out
}

func TestOversamplerRoundTripIsADelay(t *testing.T) {
	for _, factor := range []int{1, 2, 4, 8, 16} {
		o, err := NewOversampler(factor, 256)
		if err != nil {
			t.Fatal(err)
		}
		// Within the passband the round trip is the input delayed by
		// Latency samples.
		in := sineAt(1000, 48000, 9600)
		out := roundTrip(t, o, in, 100)
		d := o.Latency()
		for i := 4800; i < len(in); i++ {
			if math.Abs(float64(out[i]-in[i-d])) > 2e-4 {
				t.Fatalf("factor %d: sample %d is %v, want %v (latency %d)", factor, i, out[i], in[i-d], d)
			}
		}
	}
}

func TestOversamplerPassband(t *testing.T) {
	o, _ := NewOversampler(4, 512)
	for _, f := range []float64{100, 5000, 15000, 19000} {
		o.Reset()
		out := roundTrip(t, o, sineAt(f, 48000, 24000), 512)
		peak := 0.0
		for _, x := range out[12000:] {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
		if db := 20 * math.Log10(peak); math.Abs(db) > 0.1 {
			t.Errorf("%.0f Hz: %.3f dB through the round trip, want 0 ±0.1", f, db)
		}
	}
}

func TestOversamplerRejectsImagesAndAliases(t *testing.T) {
	for _, factor := range []int{2, 4, 8, 16} {
		o, _ := NewOversampler(factor, 512)
		// Up: a 10 kHz tone must come out as a clean 10 kHz tone at the
		// high rate, without images around multiples of 48 kHz.
		high := make([]float32, 0, 24000*factor)
		in := sineAt(10000, 48000, 24000)
		for start := 0; start < len(in); start += 512 {
			high = append(high, o.Up(in[start:min(start+512, len(in))])...)
		}
		rate := 48000 * float64(factor)
		if img := imageLevel(high[len(high)/2:], 10000, rate); img > -90 {
			t.Errorf("factor %d: images at %.1f dB, want below -90", factor, img)
		}

		// Down: a 30 kHz tone at the high rate is above the original
		// Nyquist frequency and must not fold back to 18 kHz.
		o.Reset()
		tone := sineAt(30000, rate, 24000*factor)
		out := make([]float32, 24000)
		for start := 0; start < len(out); start += 512 {
			end := min(start+512, len(out))
			o.Down(tone[start*factor:end*factor], out[start:end])
		}
		peak := 0.0
		for _, x := range out[12000:] {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
		if db := 20 * math.Log10(peak); db > -90 {
			t.Errorf("factor %d: 30 kHz aliases at %.1f dB, want below -90", factor, db)
		}
	}
}

// imageLevel returns the level in dB of x after removing its best-fitting
// sine at freq: everything else the upsampler added.
func imageLevel(x []float32, freq, rate float64) float64 {
	var ss, sc, cc, xs, xc float64
	for i, v := range x {
		s, c := math.Sincos(2 * math.Pi * freq * float64(i) / rate)
		ss += s * s
		cc += c * c
		sc += s * c
		xs += float64(v) * s
		xc += float64(v) * c
	}
	det := ss*cc - sc*sc
	a := (xs*cc - xc*sc) / det
	b := (xc*ss - xs*sc) / det
	peak := 0.0
	for i, v := range x {
		s, c := math.Sincos(2 * math.Pi * freq * float64(i) / rate)
		peak = math.Max(peak, math.Abs(float64(v)-a*s-b*c))
	}
	return 20 * math.Log10(peak)
}

func TestOversamplerRejectsBadFactors(t *testing.T) {
	for _, f := range []int{0, 3, 6, 32} {
		if _, err := NewOversampler(f, 64); err == nil {
			t.Errorf("factor %d accepted", f)
		}
	}
}

func TestOversamplerDoesNotAllocate(t *testing.T) {
	o, _ := NewOversampler(8, 512)
	in := sineAt(1000, 48000, 512)
	out := make([]float32, 512)
	if allocs := testing.AllocsPerRun(20, func() { o.Down(o.Up(in), out) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
