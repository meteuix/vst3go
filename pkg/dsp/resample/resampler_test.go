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
			worst = math.Max(worst, math.Abs(float64(y[k])-ideal))
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
			peak = math.Max(peak, math.Abs(float64(v)))
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
	k := SincKernel()
	if k.Weight(0) != 1 {
		t.Errorf("Weight(0) = %v", k.Weight(0))
	}
	for _, n := range []float64{1, 2, -3, 7} {
		if w := k.Weight(n); math.Abs(w) > 1e-12 {
			t.Errorf("Weight(%v) = %v, want 0 at a zero crossing", n, w)
		}
	}
	if k.Weight(24) != 0 || k.Weight(-25) != 0 {
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
		t.Errorf("interpolated %v, want %v", y, want)
	}
}
