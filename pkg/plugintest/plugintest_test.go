package plugintest

import (
	"math"
	"testing"
)

func TestSignalsAndAnalysis(t *testing.T) {
	sine := Sine(1000, 48000, 4800, 0.5)
	if p := Peak(sine); math.Abs(p-0.5) > 1e-3 {
		t.Fatalf("Peak = %v", p)
	}
	if r := RMS(sine); math.Abs(r-0.5/math.Sqrt2) > 1e-3 {
		t.Fatalf("RMS = %v", r)
	}
	if f := ZeroCrossingFrequency(sine, 48000); math.Abs(f-1000) > 1 {
		t.Fatalf("frequency = %v", f)
	}
	if FirstNonZero(Impulse(10, 7), 0) != 7 || FirstNonZero(Silence(1, 5)[0], 0) != -1 {
		t.Fatal("FirstNonZero")
	}
	if db := PeakDB(Constant(4, 0.5)); math.Abs(db+6.0206) > 1e-3 {
		t.Fatalf("PeakDB = %v", db)
	}
	ch := Channels(2, []float32{1})
	ch[0][0] = 2
	if ch[1][0] != 1 {
		t.Fatal("Channels must copy the signal")
	}
}
