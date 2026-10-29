package fft

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"
)

func dft(x []complex128) []complex128 {
	n := len(x)
	out := make([]complex128, n)
	for k := range out {
		for i, v := range x {
			s, c := math.Sincos(-2 * math.Pi * float64(k*i) / float64(n))
			out[k] += v * complex(c, s)
		}
	}
	return out
}

func TestComplexMatchesDFT(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := 1; n <= 512; n *= 2 {
		f, err := NewComplex(n)
		if err != nil {
			t.Fatal(err)
		}
		x := make([]complex128, n)
		for i := range x {
			x[i] = complex(rng.NormFloat64(), rng.NormFloat64())
		}
		want := dft(x)
		got := append([]complex128(nil), x...)
		f.Forward(got)
		for k := range want {
			if cmplx.Abs(got[k]-want[k]) > 1e-9*float64(n) {
				t.Fatalf("n=%d bin %d: %v, want %v", n, k, got[k], want[k])
			}
		}
		f.Inverse(got)
		for i := range x {
			if cmplx.Abs(got[i]-x[i]) > 1e-12*float64(n) {
				t.Fatalf("n=%d: inverse sample %d is %v, want %v", n, i, got[i], x[i])
			}
		}
	}
}

func TestRealMatchesDFT(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for n := 2; n <= 1024; n *= 2 {
		f, err := NewReal(n)
		if err != nil {
			t.Fatal(err)
		}
		x := make([]float64, n)
		cx := make([]complex128, n)
		for i := range x {
			x[i] = rng.NormFloat64()
			cx[i] = complex(x[i], 0)
		}
		want := dft(cx)
		got := make([]complex128, f.Bins())
		f.Forward(x, got)
		for k := range got {
			if cmplx.Abs(got[k]-want[k]) > 1e-9*float64(n) {
				t.Fatalf("n=%d bin %d: %v, want %v", n, k, got[k], want[k])
			}
		}
		back := make([]float64, n)
		f.Inverse(got, back)
		for i := range x {
			if math.Abs(back[i]-x[i]) > 1e-12*float64(n) {
				t.Fatalf("n=%d: inverse sample %d is %v, want %v", n, i, back[i], x[i])
			}
		}
	}
}

func TestRejectsSizesThatAreNotPowersOfTwo(t *testing.T) {
	for _, n := range []int{0, 3, 6, 1000} {
		if _, err := NewComplex(n); err == nil {
			t.Errorf("NewComplex(%d) succeeded", n)
		}
	}
	if _, err := NewReal(1); err == nil {
		t.Error("NewReal(1) succeeded")
	}
}

func TestTransformsDoNotAllocate(t *testing.T) {
	c, _ := NewComplex(1024)
	r, _ := NewReal(1024)
	x := make([]complex128, 1024)
	in := make([]float64, 1024)
	spec := make([]complex128, r.Bins())
	allocs := testing.AllocsPerRun(20, func() {
		c.Forward(x)
		c.Inverse(x)
		r.Forward(in, spec)
		r.Inverse(spec, in)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
