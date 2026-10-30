package resample

import (
	"math"
	"testing"
)

func TestResamplerConvertsATone(t *testing.T) {
	for _, rates := range [][2]float64{{44100, 48000}, {48000, 44100}, {48000, 96000}, {96000, 48000}, {44100, 22050}, {8000, 48000}} {
		in, out := rates[0], rates[1]
		freq := 1000.0
		x := sineAt(freq, in, int(in)) // one second
		y, err := Convert(x, in, out)
		if err != nil {
			t.Fatal(err)
		}
		if want := int(out); len(y) != want {
			t.Fatalf("%v: %d samples, want %d", rates, len(y), want)
		}
		// Time-aligned: output k is the tone at time k/out.
		worst := 0.0
		for k := len(y) / 4; k < len(y)*3/4; k++ {
			ideal := math.Sin(2 * math.Pi * freq * float64(k) / out)
			worst = max(worst, math.Abs(float64(y[k])-ideal))
		}
		if db := 20 * math.Log10(worst); db > -90 {
			t.Errorf("%v: error %.1f dB, want below -90", rates, db)
		}
	}
}

func TestResamplerPassbandAndStopband(t *testing.T) {
	level := func(freq, in, out float64) float64 {
		y, _ := Convert(sineAt(freq, in, int(in)), in, out)
		peak := 0.0
		for _, v := range y[len(y)/4 : len(y)*3/4] {
			peak = max(peak, math.Abs(float64(v)))
		}
		return 20 * math.Log10(peak)
	}
	if db := level(19500, 48000, 44100); math.Abs(db) > 0.05 {
		t.Errorf("19.5 kHz to 44.1 kHz: %.3f dB, want 0", db)
	}
	// 23 kHz is above 22.05 kHz: it must be removed, not folded to 21.1 kHz.
	if db := level(23000, 48000, 44100); db > -90 {
		t.Errorf("23 kHz to 44.1 kHz: %.1f dB, want below -90", db)
	}
}

func TestResamplerStreamsInAnyBlockSize(t *testing.T) {
	x := sineAt(440, 44100, 20000)
	whole, _ := Convert(x, 44100, 48000)
	r, _ := NewResampler(44100, 48000)
	var got []float32
	in := x
	out := make([]float32, 37)
	for len(got) < len(whole)-200 {
		c, n := r.Process(in[:min(len(in), 53)], out)
		in = in[c:]
		got = append(got, out[:n]...)
	}
	for i := range got {
		if got[i] != whole[i] {
			t.Fatalf("sample %d: streamed %v, whole %v", i, got[i], whole[i])
		}
	}
}

func TestResamplerRejectsBadRates(t *testing.T) {
	for _, r := range [][2]float64{{0, 48000}, {48000, -1}, {math.NaN(), 48000}} {
		if _, err := NewResampler(r[0], r[1]); err == nil {
			t.Errorf("%v accepted", r)
		}
	}
}

func TestResamplerDoesNotAllocate(t *testing.T) {
	r, _ := NewResampler(44100, 48000)
	in := sineAt(1000, 44100, 512)
	out := make([]float32, 512)
	if allocs := testing.AllocsPerRun(20, func() { r.Process(in, out) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestSincKernel(t *testing.T) {
	for _, k := range []*Kernel{SincKernel(), ShortSincKernel()} {
		checkKernel(t, k)
	}
}

func checkKernel(t *testing.T, k *Kernel) {
	t.Helper()
	if k.Weight(0) != 1 {
		t.Errorf("Weight(0) = %v", k.Weight(0))
	}
	for _, n := range []float64{1, 2, -3, 7} {
		if w := k.Weight(n); math.Abs(w) > 1e-12 {
			t.Errorf("Weight(%v) = %v, want 0 at a zero crossing", n, w)
		}
	}
	if z := float64(k.Zeros()); k.Weight(z) != 0 || k.Weight(-z-1) != 0 {
		t.Error("kernel is not zero outside its span")
	}
	// Interpolating a sampled sine halfway between samples.
	x := sineAt(1000, 48000, 64)
	pos := 31.5
	y := 0.0
	for i := 32 - k.Zeros(); i < 32+k.Zeros(); i++ {
		y += float64(x[i]) * k.Weight(pos-float64(i))
	}
	want := math.Sin(2 * math.Pi * 1000 * pos / 48000)
	if math.Abs(y-want) > 1e-3 {
		t.Errorf("%d zeros: interpolated %v, want %v", k.Zeros(), y, want)
	}
}

func TestKernelInterpolateMatchesWeight(t *testing.T) {
	for _, k := range []*Kernel{SincKernel(), ShortSincKernel()} {
		checkInterpolate(t, k)
	}
}

func checkInterpolate(t *testing.T, k *Kernel) {
	t.Helper()
	x0 := make([]float32, 400)
	x1 := make([]float32, 400)
	for i := range x0 {
		x0[i] = float32(math.Sin(float64(i) * 0.37))
		x1[i] = float32(math.Cos(float64(i) * 0.11))
	}
	for _, c := range []float64{1, 0.9, 0.51, 0.25} {
		for _, p := range []float64{200, 200.25, 200.5, 201.999} {
			var w0, w1 float64
			span := float64(k.Zeros()) / c
			for i := int(math.Ceil(p - span)); i <= int(math.Floor(p+span)); i++ {
				w := c * k.Weight(c*(p-float64(i)))
				w0 += w * float64(x0[i])
				w1 += w * float64(x1[i])
			}
			a, b := k.Interpolate(x0, x1, p, c)
			m, _ := k.Interpolate(x0, nil, p, c)
			if math.Abs(a-w0) > 1e-12 || math.Abs(b-w1) > 1e-12 || m != a {
				t.Fatalf("c=%v p=%v: (%v, %v, %v), want (%v, %v)", c, p, a, b, m, w0, w1)
			}
		}
	}
}

// kernelResponse returns the kernel's gain in dB at frequency f, in units
// of the cutoff (half a cycle per sample at c = 1).
func kernelResponse(k *Kernel, f float64) float64 {
	const os = 64
	var re, im float64
	for i := -k.Zeros() * os; i <= k.Zeros()*os; i++ {
		x := float64(i) / os
		w := k.Weight(x) / os
		re += w * math.Cos(math.Pi*f*x)
		im += w * math.Sin(math.Pi*f*x)
	}
	return 20 * math.Log10(math.Hypot(re, im))
}

func TestKernelResponses(t *testing.T) {
	for _, tc := range []struct {
		k          *Kernel
		flat, stop float64 // within 0.1 dB up to flat; 80 dB down from stop
	}{
		{SincKernel(), 0.9, 1.11},
		{ShortSincKernel(), 0.75, 1.33},
	} {
		for f := 0.0; f <= tc.flat; f += 0.01 {
			if db := kernelResponse(tc.k, f); math.Abs(db) > 0.1 {
				t.Errorf("%d zeros: %.2f of the cutoff at %.3f dB", tc.k.Zeros(), f, db)
			}
		}
		for f := tc.stop; f < 4; f += 0.01 {
			if db := kernelResponse(tc.k, f); db > -80 {
				t.Errorf("%d zeros: %.2f of the cutoff at %.1f dB", tc.k.Zeros(), f, db)
			}
		}
	}
}
