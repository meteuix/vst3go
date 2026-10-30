package fastmath

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestSin2PiMatchesMath(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	check := func(x float64) {
		if d := math.Abs(Sin2Pi(x) - math.Sin(2*math.Pi*x)); d > 1e-13 {
			t.Fatalf("Sin2Pi(%v) off by %g", x, d)
		}
		if d := math.Abs(Cos2Pi(x) - math.Cos(2*math.Pi*x)); d > 1e-13 {
			t.Fatalf("Cos2Pi(%v) off by %g", x, d)
		}
	}
	for _, x := range []float64{0, 0.25, -0.25, 0.5, -0.5, 0.75, 1, 0.125, -0.375, 1e-12} {
		check(x)
	}
	for range 1_000_000 {
		check(rng.Float64()*20 - 10)
	}
	// Exact at the zero crossings, so silence stays silent.
	for _, x := range []float64{0, 0.5, -0.5, 1, 3} {
		if Sin2Pi(x) != 0 {
			t.Fatalf("Sin2Pi(%v) = %v", x, Sin2Pi(x))
		}
	}
}

var sink float64

func BenchmarkSin2Pi(b *testing.B) {
	x := 0.0
	for b.Loop() {
		sink += Sin2Pi(x)
		x += 0.0013
	}
}

func BenchmarkMathSin(b *testing.B) {
	x := 0.0
	for b.Loop() {
		sink += math.Sin(2 * math.Pi * x)
		x += 0.0013
	}
}
