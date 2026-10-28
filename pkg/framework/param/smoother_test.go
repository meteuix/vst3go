package param

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRampLinearReachesTargetInRampTime(t *testing.T) {
	r := NewRamp(SmoothLinear, 1000, 0.004, 0) // 4 samples
	r.SetTarget(1)
	want := []float64{0.25, 0.5, 0.75, 1, 1}
	for i, w := range want {
		if got := r.Next(); !near(got, w) {
			t.Fatalf("sample %d = %v, want %v", i, got, w)
		}
	}
	if r.IsSmoothing() {
		t.Fatal("ramp still running at target")
	}
}

func TestRampRetargetStartsFromCurrent(t *testing.T) {
	r := NewRamp(SmoothLinear, 1000, 0.004, 0)
	r.SetTarget(1)
	r.Next()
	r.Next() // 0.5
	r.SetTarget(0)
	if got := r.Next(); !near(got, 0.375) {
		t.Fatalf("after retarget = %v, want 0.375", got)
	}
	if got := r.Skip(10); got != 0 || r.IsSmoothing() {
		t.Fatalf("Skip past end = %v smoothing=%v", got, r.IsSmoothing())
	}
}

func TestRampMultiplicativeIsGeometric(t *testing.T) {
	r := NewRamp(SmoothMultiplicative, 1000, 0.002, 100)
	r.SetTarget(400)
	if got := r.Next(); !near(got, 200) {
		t.Fatalf("first step = %v, want 200", got)
	}
	if got := r.Next(); got != 400 {
		t.Fatalf("second step = %v, want exactly 400", got)
	}
	r.SetTarget(0) // not representable geometrically: jump
	if r.Next() != 0 || r.IsSmoothing() {
		t.Fatal("multiplicative ramp to zero must jump")
	}
}

func TestRampSkipMatchesNext(t *testing.T) {
	a := NewRamp(SmoothMultiplicative, 48000, 0.05, 20)
	b := a
	a.SetTarget(20000)
	b.SetTarget(20000)
	for i := 0; i < 1000; i++ {
		a.Next()
	}
	if got, want := b.Skip(1000), a.Current(); math.Abs(got-want)/want > 1e-9 {
		t.Fatalf("Skip = %v, Next x1000 = %v", got, want)
	}
}

func TestRampWithoutTimeJumps(t *testing.T) {
	var r Ramp
	r.SetTarget(3)
	if r.Next() != 3 || r.IsSmoothing() {
		t.Fatal("zero ramp must jump")
	}
	r = NewRamp(SmoothLinear, 1000, 0.01, 0)
	r.SetTarget(1)
	r.Reset(1000, 0.02)
	if r.IsSmoothing() || r.Current() != 1 {
		t.Fatal("Reset must stop a running ramp at its target")
	}
}

func TestRampFill(t *testing.T) {
	r := NewRamp(SmoothLinear, 1000, 0.002, 0)
	r.SetTarget(1)
	buf := make([]float32, 4)
	r.Fill(buf)
	if buf[0] != 0.5 || buf[1] != 1 || buf[3] != 1 {
		t.Fatalf("Fill = %v", buf)
	}
	buf64 := make([]float64, 2)
	r.SetTarget(0)
	r.Fill64(buf64)
	if buf64[0] != 0.5 || buf64[1] != 0 {
		t.Fatalf("Fill64 = %v", buf64)
	}
}
