package delay

import (
	"math"
	"testing"
)

func TestLineDelaysByWholeSamples(t *testing.T) {
	d := New(0.01, 1000) // 10 samples
	for _, delay := range []int{1, 3, 10} {
		d.Reset()
		out := make([]float32, 20)
		for i := range out {
			in := float32(0)
			if i == 2 {
				in = 1
			}
			out[i] = d.Process(in, float64(delay))
		}
		for i, v := range out {
			want := float32(0)
			if i == 2+delay {
				want = 1
			}
			if v != want {
				t.Fatalf("delay %d: out[%d] = %v, want %v", delay, i, v, want)
			}
		}
	}
}

func TestLineInterpolatesAndClamps(t *testing.T) {
	d := New(0.01, 1000)
	for i := 0; i < 10; i++ {
		d.Write(float32(i)) // a ramp: the sample k writes ago is 9-(k-1)
	}
	if got := d.Read(1.5); math.Abs(float64(got)-8.5) > 1e-6 {
		t.Fatalf("Read(1.5) = %v, want 8.5", got)
	}
	// Below one sample the newest sample is returned, never the oldest.
	if got := d.Read(0); got != 9 {
		t.Fatalf("Read(0) = %v, want the newest sample 9", got)
	}
	if got := d.Read(0.5); got != 9 {
		t.Fatalf("Read(0.5) = %v, want 9", got)
	}
	if got, want := d.Read(1e9), d.Read(float64(d.MaxDelay())); got != want {
		t.Fatalf("Read beyond the buffer = %v, want the oldest sample %v", got, want)
	}
	if got := d.ReadMs(3); got != 7 {
		t.Fatalf("ReadMs(3) = %v, want 7", got)
	}
}

func TestLineReadJustPastTheWriteHeadWraps(t *testing.T) {
	// writePos - delay a hair below zero rounds to exactly the buffer size
	// when the buffer wraps; the read must land on the first slot.
	d := New(1, 48000)
	for range 3 {
		d.Write(0.25)
	}
	d.Write(1) // writePos is now 4
	for _, delay := range []float64{4 + 1e-13, 4 - 1e-13} {
		if got := d.Read(delay); math.Abs(float64(got)-0.25) > 1e-6 {
			t.Fatalf("Read(%v) = %v, want 0.25", delay, got)
		}
	}
}

func TestMultiTapDelaysEachTap(t *testing.T) {
	m := NewMultiTap(0.1, 1000, 2)
	taps := []TapOutput{{DelaySamples: 5, Gain: 1, Pan: -1}, {DelaySamples: 9, Gain: 0.5, Pan: 1}}
	var l, r float32
	for i := 0; i < 20; i++ {
		in := float32(0)
		if i == 0 {
			in = 1
		}
		m.ProcessMultiTap(in, taps, &l, &r)
		switch i {
		case 5:
			if math.Abs(float64(l)-1) > 1e-6 || math.Abs(float64(r)) > 1e-6 {
				t.Fatalf("tap 1 at %d: %v %v", i, l, r)
			}
		case 9:
			if math.Abs(float64(r)-0.5) > 1e-6 || math.Abs(float64(l)) > 1e-6 {
				t.Fatalf("tap 2 at %d: %v %v", i, l, r)
			}
		default:
			if l != 0 || math.Abs(float64(r)) > 1e-6 {
				t.Fatalf("unexpected output at %d: %v %v", i, l, r)
			}
		}
	}
}
