package pitch

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fft"
)

const rate = 48000.0

func tone(freq float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/rate))
	}
	return x
}

// dominant returns the strongest frequency in x, refined between bins.
func dominant(t *testing.T, x []float32) float64 {
	t.Helper()
	n := 1
	for n*2 <= len(x) {
		n *= 2
	}
	f, err := fft.NewReal(n)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float64, n)
	for i := range in {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
		in[i] = float64(x[i]) * w
	}
	spec := make([]complex128, f.Bins())
	f.Forward(in, spec)
	best := 1
	mag := func(k int) float64 { return math.Log(math.Hypot(real(spec[k]), imag(spec[k])) + 1e-30) }
	for k := 1; k < len(spec)-1; k++ {
		if mag(k) > mag(best) {
			best = k
		}
	}
	a, b, c := mag(best-1), mag(best), mag(best+1)
	return (float64(best) + 0.5*(a-c)/(a-2*b+c)) * rate / float64(n)
}

func TestShifterShiftsATone(t *testing.T) {
	for _, semis := range []float64{-12, -5, 7, 12} {
		s := NewShifter(rate, 0.1)
		s.SetSemitones(semis)
		x := tone(440, 96000)
		s.Process(x, x)
		want := 440 * math.Exp2(semis/12)
		if got := dominant(t, x[16384:]); math.Abs(got-want) > want*0.003 {
			t.Errorf("%+.0f semitones: %.2f Hz, want %.2f", semis, got, want)
		}
	}
}

func TestShifterUnityRatioIsADelay(t *testing.T) {
	s := NewShifter(rate, 0.1)
	x := tone(440, 9600)
	y := make([]float32, len(x))
	s.Process(x, y)
	// Both taps hold fixed delays; their fades sum to one, so a steady
	// tone keeps its frequency and roughly its level.
	if got := dominant(t, y[4800:]); math.Abs(got-440) > 2 {
		t.Errorf("unity ratio: %.2f Hz, want 440", got)
	}
}

func TestSpectralShifterShiftsATone(t *testing.T) {
	for _, semis := range []float64{-7, 3, 12} {
		s, err := NewSpectralShifter(2048)
		if err != nil {
			t.Fatal(err)
		}
		s.SetSemitones(semis)
		x := tone(330, 96000)
		s.Process(x, x)
		want := 330 * math.Exp2(semis/12)
		if got := dominant(t, x[16384:]); math.Abs(got-want) > want*0.003 {
			t.Errorf("%+.0f semitones: %.2f Hz, want %.2f", semis, got, want)
		}
		peak := 0.0
		for _, v := range x[16384:] {
			peak = max(peak, math.Abs(float64(v)))
		}
		if math.Abs(peak-0.5) > 0.05 {
			t.Errorf("%+.0f semitones: level %.3f, want about 0.5", semis, peak)
		}
	}
}

func TestSpectralShifterUnityRatioReproducesTheInput(t *testing.T) {
	s, _ := NewSpectralShifter(1024)
	x := tone(440, 24000)
	y := make([]float32, len(x))
	s.Process(x, y)
	d := s.Latency()
	for i := 8192; i < len(x); i++ {
		if math.Abs(float64(y[i]-x[i-d])) > 1e-3 {
			t.Fatalf("sample %d: %v, want %v", i, y[i], x[i-d])
		}
	}
}

func TestStretchKeepsPitchAndChangesLength(t *testing.T) {
	for _, factor := range []float64{0.25, 0.5, 0.8, 1.5, 2, 4} {
		x := tone(440, 48000)
		y, err := Stretch(x, factor, 2048)
		if err != nil {
			t.Fatal(err)
		}
		if want := int(math.Round(48000 * factor)); len(y) != want {
			t.Fatalf("factor %v: %d samples, want %d", factor, len(y), want)
		}
		mid := y[len(y)/4 : len(y)*3/4]
		if got := dominant(t, mid); math.Abs(got-440) > 1.5 {
			t.Errorf("factor %v: %.2f Hz, want 440", factor, got)
		}
		peak := 0.0
		for _, v := range mid {
			peak = max(peak, math.Abs(float64(v)))
		}
		if math.Abs(peak-0.5) > 0.05 {
			t.Errorf("factor %v: level %.3f, want 0.5", factor, peak)
		}
	}
	if _, err := Stretch(nil, 0, 2048); err == nil {
		t.Error("factor 0 accepted")
	}
}

func TestStretchKeepsEventsInPlace(t *testing.T) {
	// A burst starting at 0.5 s starts at 1 s after stretching by 2.
	x := make([]float32, 48000)
	copy(x[24000:], tone(1000, 4800))
	y, _ := Stretch(x, 2, 1024)
	onset := -1
	for i, v := range y {
		if math.Abs(float64(v)) > 0.25 {
			onset = i
			break
		}
	}
	if math.Abs(float64(onset-48000)) > 600 {
		t.Errorf("burst starts at %d, want about 48000", onset)
	}
}

func TestDetectorFindsTheFundamental(t *testing.T) {
	for _, f0 := range []float64{55, 110, 196, 440, 880, 1318.5} {
		d, err := NewDetector(rate, 50, 2000, 512)
		if err != nil {
			t.Fatal(err)
		}
		// A sawtooth-like tone with strong harmonics, where naive peak
		// picking finds an overtone.
		x := make([]float32, 24000)
		for i := range x {
			v := 0.0
			for h := 1; h <= 8; h++ {
				v += math.Sin(2*math.Pi*f0*float64(h)*float64(i)/rate) / float64(h)
			}
			x[i] = float32(0.3 * v)
		}
		d.Process(x)
		got, clarity := d.Pitch()
		if cents := 1200 * math.Log2(got/f0); math.Abs(cents) > 2 {
			t.Errorf("%.1f Hz: detected %.2f Hz (%.2f cents)", f0, got, cents)
		}
		if clarity < 0.9 {
			t.Errorf("%.1f Hz: clarity %.2f, want above 0.9", f0, clarity)
		}
	}
}

func TestDetectorReportsNoPitchForNoiseAndSilence(t *testing.T) {
	d, _ := NewDetector(rate, 50, 2000, 512)
	d.Process(make([]float32, 9600))
	if f, _ := d.Pitch(); f != 0 {
		t.Errorf("silence: %.1f Hz", f)
	}
	state := uint32(1)
	x := make([]float32, 9600)
	for i := range x {
		state = state*1664525 + 1013904223
		x[i] = float32(state>>8)/float32(1<<24) - 0.5
	}
	d.Process(x)
	if f, _ := d.Pitch(); f != 0 {
		t.Errorf("white noise: %.1f Hz", f)
	}
}

func TestPitchProcessorsDoNotAllocate(t *testing.T) {
	s := NewShifter(rate, 0.1)
	s.SetSemitones(5)
	v, _ := NewSpectralShifter(2048)
	v.SetSemitones(5)
	d, _ := NewDetector(rate, 50, 2000, 256)
	buf := tone(440, 512)
	allocs := testing.AllocsPerRun(20, func() {
		s.Process(buf, buf)
		v.Process(buf, buf)
		d.Process(buf)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestStretcherStreamsWhatStretchRenders(t *testing.T) {
	x := tone(330, 12000)
	want, err := Stretch(x, 1.5, 1024)
	if err != nil {
		t.Fatal(err)
	}
	// The same feed as Stretch, written and read in uneven blocks.
	s, _ := NewStretcher(1024, 1.5)
	s.SetFactor(1.5)
	pad := 1024
	offset := int(math.Round(float64(pad) * 1.5))
	feed := append(make([]float32, pad), x...)
	feed = append(feed, make([]float32, 4096)...)
	var got []float32
	buf := make([]float32, 53)
	for len(got) < offset+len(want) {
		feed = feed[s.Write(feed[:min(37, len(feed))]):]
		got = append(got, buf[:s.Read(buf)]...)
	}
	for i, w := range want {
		if g := got[offset+i]; g != w {
			t.Fatalf("sample %d: streamed %v, offline %v", i, g, w)
		}
	}
}

func TestStretcherFollowsAChangingFactor(t *testing.T) {
	s, err := NewStretcher(2048, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	x := tone(440, 96000)
	out := make([]float32, 0, 200000)
	buf := make([]float32, 512)
	for i := 0; i < len(x); {
		// The first half at 0.5, the second at 2.
		if i < len(x)/2 {
			s.SetFactor(0.5)
		} else {
			s.SetFactor(2)
		}
		i += s.Write(x[i:min(i+256, len(x))])
		out = append(out, buf[:s.Read(buf)]...)
	}
	// 48000·0.5 + 48000·2 = 120000, less about a frame still in flight.
	if len(out) < 120000-3*2048 || len(out) > 120000 {
		t.Fatalf("%d samples out, want about 120000", len(out))
	}
	for _, part := range [][]float32{out[4096:20000], out[40000:110000]} {
		if f := crossingFrequency(part); math.Abs(f-440) > 2 {
			t.Fatalf("pitch moved to %.1f Hz", f)
		}
	}
}

// crossingFrequency estimates frequency from zero crossings.
func crossingFrequency(x []float32) float64 {
	crossings, first, last := 0, -1, -1
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			if first < 0 {
				first = i
			}
			last = i
			crossings++
		}
	}
	return float64(crossings-1) * rate / float64(last-first)
}

func TestStretcherDoesNotAllocate(t *testing.T) {
	s, _ := NewStretcher(1024, 0.5)
	in := tone(220, 256)
	out := make([]float32, 512)
	allocs := testing.AllocsPerRun(50, func() {
		s.SetFactor(0.8)
		s.Write(in)
		s.Read(out)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
