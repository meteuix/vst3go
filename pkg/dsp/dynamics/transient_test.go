package dynamics

import (
	"math"
	"testing"
)

// shape runs a mono signal through a shaper and returns the gain in dB
// at every sample.
func shapeGains(s *TransientShaper, signal []float32) []float64 {
	gains := make([]float64, len(signal))
	out := make([]float32, 1)
	for i, x := range signal {
		s.ProcessBuffer([]float32{x}, out)
		gains[i] = s.GainDB()
	}
	return gains
}

// burst is silence, then level from sample on until off, then level/10.
func burst(n, on, off int, level float32) []float32 {
	x := make([]float32, n)
	for i := on; i < n; i++ {
		x[i] = level
		if i >= off {
			x[i] = level / 10
		}
	}
	return x
}

func TestTransientShaperNeutralIsTransparent(t *testing.T) {
	s := NewTransientShaper(48000)
	for i, g := range shapeGains(s, burst(48000, 1000, 20000, 0.5)) {
		if g != 0 {
			t.Fatalf("sample %d: gain %v dB with neutral settings", i, g)
		}
	}
}

func TestTransientShaperAttack(t *testing.T) {
	signal := burst(48000, 1000, 48000, 0.5)
	for _, amount := range []float64{1, -1} {
		s := NewTransientShaper(48000)
		s.SetAttack(amount)
		gains := shapeGains(s, signal)
		onset := gains[1000+48] // 1 ms into the note
		if amount > 0 && onset < 6 || amount < 0 && onset > -6 {
			t.Errorf("attack %v: %.2f dB at the onset", amount, onset)
		}
		// 250 ms later the note is steady: back to unity.
		if g := gains[1000+12000]; math.Abs(g) > 0.1 {
			t.Errorf("attack %v: %.2f dB on the steady note", amount, g)
		}
	}
}

func TestTransientShaperSustain(t *testing.T) {
	// The note drops 20 dB at sample 20000.
	signal := burst(144000, 1000, 20000, 0.5)
	for _, amount := range []float64{1, -1} {
		s := NewTransientShaper(48000)
		s.SetSustain(amount)
		gains := shapeGains(s, signal)
		if g := gains[19999]; math.Abs(g) > 0.1 {
			t.Errorf("sustain %v: %.2f dB on the steady note", amount, g)
		}
		tail := gains[20000+2400] // 50 ms into the decay
		if amount > 0 && tail < 6 || amount < 0 && tail > -6 {
			t.Errorf("sustain %v: %.2f dB in the tail", amount, tail)
		}
		if g := gains[143999]; math.Abs(g) > 0.01 { // ten long release times later
			t.Errorf("sustain %v: %.2f dB once the tail level is steady", amount, g)
		}
	}
}

func TestTransientShaperStereoIsLinked(t *testing.T) {
	s := NewTransientShaper(48000)
	s.SetAttack(1)
	l := burst(4800, 100, 4800, 0.5)
	r := make([]float32, 4800)
	for i := range r {
		r[i] = 0.01
	}
	outL := make([]float32, 4800)
	outR := make([]float32, 4800)
	s.ProcessStereo(l, r, outL, outR)
	if outL[148]/l[148] != outR[148]/r[148] || outR[148] <= r[148] {
		t.Fatalf("gains L %v R %v: want the same boost on both", outL[148]/l[148], outR[148]/r[148])
	}
}

func TestTransientShaperDoesNotAllocate(t *testing.T) {
	s := NewTransientShaper(48000)
	s.SetAttack(0.5)
	s.SetSustain(-0.5)
	buf := burst(512, 10, 300, 0.5)
	if allocs := testing.AllocsPerRun(100, func() { s.ProcessStereo(buf, buf, buf, buf) }); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}
