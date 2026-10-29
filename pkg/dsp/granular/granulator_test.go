package granular

import (
	"math"
	"testing"
)

const rate = 48000.0

func sine(freq float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/rate))
	}
	return x
}

// crossingRate returns the frequency from rising zero crossings.
func crossingRate(x []float32) float64 {
	first, last, count := -1, -1, 0
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			if first < 0 {
				first = i
			} else {
				count++
			}
			last = i
		}
	}
	return float64(count) * rate / float64(last-first)
}

func TestGranulatorRegularGrainsAreADelay(t *testing.T) {
	// 80 ms grains every 40 ms: the Hann windows overlap by half and sum
	// to one, so unpitched grains at a fixed position rebuild the input
	// delayed by Position, exactly from the first sample (before the
	// buffer has filled to Position, grains read silence).
	g := New(rate, 1, 8)
	g.SetSize(0.08)
	g.SetDensity(25)
	g.SetPosition(0.1)
	in := sine(440, 48000)
	l := make([]float32, len(in))
	r := make([]float32, len(in))
	for start := 0; start < len(in); start += 256 {
		end := min(start+256, len(in))
		g.Process(in[start:end], l[start:end], r[start:end])
	}
	d := 4800
	for i := range in {
		var want float32
		if i >= d {
			want = in[i-d]
		}
		if math.Abs(float64(l[i]-want)) > 1e-4 || l[i] != r[i] {
			t.Fatalf("sample %d: %v/%v, want %v", i, l[i], r[i], want)
		}
	}
}

func TestGranulatorPitchesGrains(t *testing.T) {
	g := New(rate, 2, 32)
	g.Load(sine(300, 96000))
	g.SetSize(0.1)
	g.SetDensity(40)
	g.SetPosition(1.5)
	g.SetPitch(2)
	l := make([]float32, 48000)
	r := make([]float32, 48000)
	g.Process(make([]float32, 48000), l, r)
	// Each grain is a 600 Hz tone; the zero-crossing rate over many
	// grains stays near it.
	if f := crossingRate(l[4800:]); math.Abs(f-600) > 15 {
		t.Fatalf("grains at %.1f Hz, want 600", f)
	}
}

func TestGranulatorFreezeHoldsTheBuffer(t *testing.T) {
	g := New(rate, 1, 16)
	l := make([]float32, 24000)
	r := make([]float32, 24000)
	g.Process(sine(500, 24000), l, r)
	g.Freeze(true)
	g.Process(make([]float32, 24000), l, r)
	if peak(l[12000:]) < 0.2 {
		t.Fatal("frozen buffer went silent")
	}
	// Recording again: a second of silence replaces the whole buffer.
	g.Freeze(false)
	ll := make([]float32, 72000)
	rr := make([]float32, 72000)
	g.Process(make([]float32, 72000), ll, rr)
	if p := peak(ll[60000:]); p > 1e-6 {
		t.Fatalf("recording resumed but old audio still plays: %v", p)
	}
}

func TestGranulatorSpreadPansGrains(t *testing.T) {
	g := New(rate, 1, 32)
	g.Load(sine(440, 48000))
	g.SetSpread(1)
	g.SetPosition(0.5)
	l := make([]float32, 48000)
	r := make([]float32, 48000)
	g.Process(make([]float32, 48000), l, r)
	diff := 0.0
	for i := range l {
		diff += math.Abs(float64(l[i] - r[i]))
	}
	if diff/48000 < 0.01 {
		t.Fatal("spread grains are not panned")
	}
}

func peak(x []float32) float64 {
	p := 0.0
	for _, v := range x {
		p = math.Max(p, math.Abs(float64(v)))
	}
	return p
}

func TestGranulatorDoesNotAllocate(t *testing.T) {
	g := New(rate, 1, 64)
	g.SetDensity(200)
	g.SetSpray(0.1)
	g.SetPitchSpray(3)
	in := sine(440, 512)
	l := make([]float32, 512)
	r := make([]float32, 512)
	if allocs := testing.AllocsPerRun(50, func() { g.Process(in, l, r) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
