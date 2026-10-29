package convolution

import (
	"math"
	"math/rand/v2"
	"testing"
)

func direct(x, h []float32) []float64 {
	y := make([]float64, len(x))
	for n := range y {
		for k := 0; k < len(h) && k <= n; k++ {
			y[n] += float64(h[k]) * float64(x[n-k])
		}
	}
	return y
}

func noise(rng *rand.Rand, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(rng.NormFloat64() * 0.3)
	}
	return x
}

func TestConvolverMatchesDirectConvolutionWithoutLatency(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for _, tc := range []struct{ irLen, block int }{
		{1, 8}, {5, 8}, {8, 8}, {9, 8}, {100, 16}, {1000, 64}, {3000, 128}, {4096, 256},
	} {
		h := noise(rng, tc.irLen)
		x := noise(rng, 6000)
		c, err := New(h, tc.block)
		if err != nil {
			t.Fatal(err)
		}
		y := make([]float32, len(x))
		for start := 0; start < len(x); start += 93 {
			end := min(start+93, len(x))
			c.Process(x[start:end], y[start:end])
		}
		want := direct(x, h)
		for n := range y {
			if math.Abs(float64(y[n])-want[n]) > 1e-4 {
				t.Fatalf("ir %d block %d: sample %d is %v, want %v", tc.irLen, tc.block, n, y[n], want[n])
			}
		}
	}
}

func TestConvolverImpulseReturnsTheResponse(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	h := noise(rng, 700)
	c, _ := New(h, 64)
	x := make([]float32, 1000)
	x[0] = 1
	c.Process(x, x)
	for i, v := range h {
		if math.Abs(float64(x[i]-v)) > 1e-5 {
			t.Fatalf("sample %d: %v, want %v", i, x[i], v)
		}
	}
	for i := len(h); i < len(x); i++ {
		if math.Abs(float64(x[i])) > 1e-5 {
			t.Fatalf("sample %d after the response: %v", i, x[i])
		}
	}
}

func TestConvolverReset(t *testing.T) {
	c, _ := New([]float32{1, 0.5, 0.25, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1}, 8)
	buf := []float32{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	c.Process(buf, buf)
	c.Reset()
	silence := make([]float32, 32)
	c.Process(silence, silence)
	for i, x := range silence {
		if x != 0 {
			t.Fatalf("sample %d after Reset: %v", i, x)
		}
	}
}

func TestConvolverRejectsBadBlockSizes(t *testing.T) {
	for _, b := range []int{0, 4, 12, 100} {
		if _, err := New([]float32{1}, b); err == nil {
			t.Errorf("block size %d accepted", b)
		}
	}
}

func TestConvolverDoesNotAllocate(t *testing.T) {
	c, _ := New(make([]float32, 48000), 128)
	buf := make([]float32, 512)
	if allocs := testing.AllocsPerRun(20, func() { c.Process(buf, buf) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func BenchmarkConvolverTwoSecondResponse(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	c, _ := New(noise(rng, 96000), 256)
	buf := make([]float32, 512)
	b.SetBytes(512 * 4)
	for b.Loop() {
		c.Process(buf, buf)
	}
}
