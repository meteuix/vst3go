package meter

import (
	"math"
	"testing"
)

func TestSineCalibrationAcrossSampleRates(t *testing.T) {
	for _, sr := range []float64{44100, 48000, 88200, 96000, 192000} {
		c := New(sr)
		amplitude := math.Sqrt2 * math.Pow(10, -18.0/20)
		for i := 0; i < int(2*sr); i++ {
			c.Sample(amplitude * math.Sin(2*math.Pi*1000*float64(i)/sr))
		}
		r := c.Reading()
		if math.Abs(r.VU) > .05 {
			t.Errorf("sr %.0f: VU %.6f, want 0 +/- .05", sr, r.VU)
		}
		if math.Abs(r.HoldDB-(-18+20*math.Log10(math.Sqrt2))) > .02 {
			t.Errorf("sr %.0f: peak %.6f", sr, r.HoldDB)
		}
	}
}

func TestVURectifiesRatherThanComputesRMS(t *testing.T) {
	c := New(48000)
	// Same RMS as calibration sine, but a square wave has greater mean abs.
	x := math.Pow(10, -18.0/20)
	for i := 0; i < 96000; i++ {
		if i%48 < 24 {
			c.Sample(x)
		} else {
			c.Sample(-x)
		}
	}
	want := 20 * math.Log10(math.Pi/(2*math.Sqrt2))
	if got := c.Reading().VU; math.Abs(got-want) > 1e-5 {
		t.Fatalf("VU %.6f, want mean-rectified %.6f", got, want)
	}
}

func TestVUTrialStepDynamics(t *testing.T) {
	for _, sr := range []float64{44100, 48000, 96000} {
		c := New(sr)
		x := math.Pow(10, -18.0/20) / (math.Pi / (2 * math.Sqrt2))
		for i := 0; i < int(sr*.3); i++ {
			c.Sample(x)
		}
		if got := c.Reading().VU; got < 0 || got > .15 {
			t.Errorf("sr %.0f at .3 s: %.6f VU", sr, got)
		}
		for i := 0; i < int(sr); i++ {
			c.Sample(0)
		}
		if got := c.Reading().VU; got > -60 {
			t.Errorf("sr %.0f after silence: %.3f VU", sr, got)
		}
	}
}

func TestPeakTransientHoldClipAndReset(t *testing.T) {
	c := New(48000)
	c.Sample(1.25)
	if r := c.Reading(); math.Abs(r.HoldDB-20*math.Log10(1.25)) > 1e-9 || !r.Clip || !r.RecordedClip {
		t.Fatalf("transient lost: %+v", r)
	}
	for i := 0; i < 96000; i++ {
		c.Sample(0)
	}
	r := c.Reading()
	if r.Clip || !r.RecordedClip {
		t.Fatalf("clip history %+v", r)
	}
	if math.Abs(r.HoldDB-20*math.Log10(1.25)) > 1e-9 {
		t.Fatal("hold decayed")
	}
	if math.Abs(r.PeakDB-(20*math.Log10(1.25)-40)) > 1e-6 {
		t.Fatalf("peak decay %+v", r)
	}
	before := c.vu
	c.ResetPeak()
	if c.vu != before {
		t.Fatal("peak reset changed VU")
	}
	if r = c.Reading(); r.HoldDB != FloorDB || r.RecordedClip || r.Clip {
		t.Fatalf("reset %+v", r)
	}
}

func TestNonfiniteSamplesDoNotPoisonMeasurement(t *testing.T) {
	c := New(48000)
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), .5} {
		c.Sample(x)
	}
	if r := c.Reading(); math.IsNaN(r.VU) || math.Abs(r.HoldDB-20*math.Log10(.5)) > 1e-9 {
		t.Fatal(r)
	}
}
