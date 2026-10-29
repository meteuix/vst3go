package spectral

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestSTFTIdentityReproducesTheInputDelayed(t *testing.T) {
	for _, cfg := range [][2]int{{64, 32}, {256, 64}, {1024, 256}, {512, 128}} {
		s, err := NewSTFT(cfg[0], cfg[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewPCG(5, 6))
		in := make([]float32, 8000)
		for i := range in {
			in[i] = float32(rng.NormFloat64() * 0.3)
		}
		out := make([]float32, len(in))
		// Odd block sizes: framing must not depend on them.
		for start := 0; start < len(in); start += 77 {
			end := min(start+77, len(in))
			s.Process(in[start:end], out[start:end])
		}
		d := s.Latency()
		for i := d + s.Size(); i < len(in); i++ {
			if math.Abs(float64(out[i]-in[i-d])) > 1e-5 {
				t.Fatalf("size %d hop %d: sample %d is %v, want %v", cfg[0], cfg[1], i, out[i], in[i-d])
			}
		}
	}
}

func TestSTFTFrameFuncFiltersTheSpectrum(t *testing.T) {
	const size = 1024
	// Brick-wall low-pass at bin 64 (3 kHz at 48 kHz).
	s, err := NewSTFT(size, size/4, func(spectrum []complex128) {
		for k := 64; k < len(spectrum); k++ {
			spectrum[k] = 0
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	level := func(freq float64) float64 {
		s.Reset()
		buf := make([]float32, 48000)
		for i := range buf {
			buf[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / 48000))
		}
		s.Process(buf, buf)
		peak := 0.0
		for _, x := range buf[24000:] {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
		return peak
	}
	if l := level(1000); math.Abs(l-1) > 0.02 {
		t.Errorf("1 kHz passes at %.3f, want 1", l)
	}
	if l := level(8000); l > 0.01 {
		t.Errorf("8 kHz passes at %.3f, want blocked", l)
	}
}

func TestSTFTRejectsBadFraming(t *testing.T) {
	for _, cfg := range [][2]int{{1000, 250}, {256, 0}, {256, 200}, {256, 256}, {2, 1}} {
		if _, err := NewSTFT(cfg[0], cfg[1], nil); err == nil {
			t.Errorf("NewSTFT(%d, %d) succeeded", cfg[0], cfg[1])
		}
	}
}

func TestSTFTDoesNotAllocate(t *testing.T) {
	s, _ := NewSTFT(512, 128, func(spectrum []complex128) { spectrum[0] = 0 })
	buf := make([]float32, 512)
	if allocs := testing.AllocsPerRun(20, func() { s.Process(buf, buf) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
