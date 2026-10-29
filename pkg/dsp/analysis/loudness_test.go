package analysis

import (
	"math"
	"testing"
)

// segment is a stereo 1 kHz sine of the given peak level, as in the EBU
// Tech 3341/3342 conformance signals.
type segment struct {
	dBFS    float64
	seconds float64
}

// measure feeds the segments to a fresh 48 kHz meter in 512-frame blocks.
func measure(channels int, segments ...segment) *LUFSMeter {
	const sampleRate = 48000.0
	lm := NewLUFSMeter(sampleRate, channels)
	block := make([]float64, 512*channels)
	n := 0
	for _, s := range segments {
		amp := math.Pow(10, s.dBFS/20)
		frames := int(s.seconds * sampleRate)
		for f := 0; f < frames; {
			k := 0
			for ; k < 512 && f < frames; k, f, n = k+1, f+1, n+1 {
				x := amp * math.Sin(2*math.Pi*1000*float64(n)/sampleRate)
				for ch := range channels {
					block[k*channels+ch] = x
				}
			}
			lm.Process(block[:k*channels])
		}
	}
	return lm
}

func TestLUFSMeterEBUConformance(t *testing.T) {
	cases := []struct {
		name     string
		segments []segment
		want     float64
	}{
		{"3341 case 1", []segment{{-23, 20}}, -23},
		{"3341 case 2", []segment{{-33, 20}}, -33},
		{"3341 case 3 (relative gate)", []segment{{-36, 10}, {-23, 60}, {-36, 10}}, -23},
		{"3341 case 4 (absolute gate)", []segment{{-72, 20}, {-36, 10}, {-23, 60}, {-36, 10}, {-72, 20}}, -23},
	}
	for _, tc := range cases {
		lm := measure(2, tc.segments...)
		if got := lm.GetIntegratedLUFS(); math.Abs(got-tc.want) > 0.1 {
			t.Errorf("%s: integrated %.3f LUFS, want %.1f ±0.1", tc.name, got, tc.want)
		}
	}

	// A steady signal reads the same on every time scale.
	lm := measure(2, segment{-23, 5})
	for name, got := range map[string]float64{"momentary": lm.GetMomentaryLUFS(), "short-term": lm.GetShortTermLUFS()} {
		if math.Abs(got+23) > 0.1 {
			t.Errorf("%s %.3f LUFS, want -23", name, got)
		}
	}
}

func TestLUFSMeterLoudnessRange(t *testing.T) {
	cases := []struct {
		segments []segment
		want     float64
	}{
		{[]segment{{-20, 20}, {-30, 20}}, 10}, // EBU Tech 3342 case 1
		{[]segment{{-20, 20}, {-15, 20}}, 5},  // case 2
		{[]segment{{-40, 20}, {-20, 20}}, 20}, // case 3
	}
	for _, tc := range cases {
		if got := measure(2, tc.segments...).GetLoudnessRange(); math.Abs(got-tc.want) > 1 {
			t.Errorf("%v: LRA %.2f LU, want %.0f ±1", tc.segments, got, tc.want)
		}
	}
	if got := measure(2, segment{-23, 2}).GetLoudnessRange(); got != 0 {
		t.Errorf("LRA %.2f before a full 3 s window, want 0", got)
	}
}

func TestLUFSMeterMomentaryWindow(t *testing.T) {
	// 400 ms of tone after silence: the momentary window is full of it,
	// the 3 s window holds it for 2/15 of its length.
	lm := measure(2, segment{-200, 3}, segment{-23, 0.4})
	if got := lm.GetMomentaryLUFS(); math.Abs(got+23) > 0.2 {
		t.Errorf("momentary %.3f LUFS, want -23", got)
	}
	want := -23 + 10*math.Log10(4.0/30)
	if got := lm.GetShortTermLUFS(); math.Abs(got-want) > 0.2 {
		t.Errorf("short-term %.3f LUFS, want %.2f", got, want)
	}
}

func TestLUFSMeterSurroundWeighting(t *testing.T) {
	// 5.1: a tone on the LFE alone is not measured; on a surround channel
	// it reads 1.5 dB (10·log10 1.41) louder than on a front channel.
	reading := func(channel int) float64 {
		lm := NewLUFSMeter(48000, 6)
		frames := make([]float64, 48000*6)
		for f := 0; f < 48000; f++ {
			frames[f*6+channel] = 0.1 * math.Sin(2*math.Pi*1000*float64(f)/48000)
		}
		lm.Process(frames)
		return lm.GetMomentaryLUFS()
	}
	if got := reading(3); !math.IsInf(got, -1) {
		t.Errorf("LFE alone reads %.2f LUFS, want -Inf", got)
	}
	if diff := reading(4) - reading(0); math.Abs(diff-10*math.Log10(1.41)) > 0.01 {
		t.Errorf("surround reads %.3f dB above front, want 1.49", diff)
	}
}

// Every Process method runs on the audio thread and must not allocate.
func TestProcessDoesNotAllocate(t *testing.T) {
	l := make([]float64, 512)
	r := make([]float64, 512)
	interleaved := make([]float64, 1024)
	for i := range l {
		l[i], r[i] = math.Sin(float64(i)), math.Cos(float64(i))
		interleaved[2*i], interleaved[2*i+1] = l[i], r[i]
	}
	sa := NewSpectrumAnalyzer(1024, 48000, HannWindow)
	ps := NewPhaseScope(1024)
	vs := NewVectorScope(1024)
	cm := NewCorrelationMeter(1024, 48000)
	sf := NewStereoFieldAnalyzer(1024, 48000)
	bm := NewBalanceMeter(1024)
	wm := NewStereoWidthMeter(1024)
	pm := NewPeakMeter(48000)
	rm := NewRMSMeter(1024)
	lm := NewLUFSMeter(48000, 2)
	for name, process := range map[string]func(){
		"SpectrumAnalyzer":    func() { sa.Process(l) },
		"PhaseScope":          func() { ps.Process(l, r) },
		"VectorScope":         func() { vs.Process(l, r) },
		"CorrelationMeter":    func() { cm.Process(l, r) },
		"StereoFieldAnalyzer": func() { sf.Process(l, r) },
		"BalanceMeter":        func() { bm.Process(l, r) },
		"StereoWidthMeter":    func() { wm.Process(l, r) },
		"PeakMeter":           func() { pm.Process(l) },
		"RMSMeter":            func() { rm.Process(l) },
		"LUFSMeter":           func() { lm.Process(interleaved) },
	} {
		if allocs := testing.AllocsPerRun(200, process); allocs != 0 {
			t.Errorf("%s.Process: %v allocations", name, allocs)
		}
	}
}
