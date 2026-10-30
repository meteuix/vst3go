package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// levels renders a constant stereo input and returns each output
// channel's final sample.
func levels(t *testing.T, set func(p *plugintest.Instance), left, right float32) [numChannels]float64 {
	p := plugintest.New(t, surroundPlugin{})
	set(p)
	in := [][]float32{plugintest.Constant(4800, left), plugintest.Constant(4800, right)}
	out := p.Render(in, numChannels)
	var l [numChannels]float64
	for ch := range l {
		l[ch] = float64(out[ch][4799])
	}
	return l
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestSurroundPansBetweenSpeakerPairs(t *testing.T) {
	// With Width 0 both inputs sit at Angle; only the left one carries
	// signal here. At a speaker's angle it lands on that speaker alone.
	for _, s := range speakers {
		l := levels(t, func(p *plugintest.Instance) {
			p.Set(paramWidth, 0)
			p.Set(paramAngle, s.angle)
		}, 0.5, 0)
		for ch, v := range l {
			want := 0.0
			if ch == s.ch {
				want = 0.5
			}
			if !near(v, want) {
				t.Fatalf("angle %v: channel %d = %v, want %v (%v)", s.angle, ch, v, want, l)
			}
		}
	}

	// Halfway between C (0) and R (30): constant power, -3 dB each.
	l := levels(t, func(p *plugintest.Instance) {
		p.Set(paramWidth, 0)
		p.Set(paramAngle, 15)
	}, 1, 0)
	if !near(l[chC], math.Sqrt2/2) || !near(l[chR], math.Sqrt2/2) || l[chL] != 0 || l[chLs] != 0 {
		t.Fatalf("15 degrees: %v", l)
	}
}

func TestSurroundWidthSpreadsTheInputs(t *testing.T) {
	// Width 100%: left input at -30 (L), right input at +30 (R).
	l := levels(t, func(*plugintest.Instance) {}, 0.25, 0.5)
	if !near(l[chL], 0.25) || !near(l[chR], 0.5) || !near(l[chC], 0) {
		t.Fatalf("stereo at width 100%%: %v", l)
	}
}

func TestSurroundSpreadPreservesPower(t *testing.T) {
	for _, set := range []func(*plugintest.Instance){
		func(p *plugintest.Instance) { p.Set(paramDistance, 0) },
		func(p *plugintest.Instance) { p.Set(paramDivergence, 50); p.Set(paramAngle, 200) },
		func(p *plugintest.Instance) { p.Set(paramCenterLevel, 0); p.Set(paramWidth, 0) },
	} {
		l := levels(t, func(p *plugintest.Instance) { p.Set(paramWidth, 0); set(p) }, 1, 0)
		power := 0.0
		for ch, v := range l {
			if ch != chLFE {
				power += v * v
			}
		}
		if !near(power, 1) {
			t.Fatalf("total power %v, want 1: %v", power, l)
		}
	}
	// Distance 0: the source sits in the middle, equal on all five.
	l := levels(t, func(p *plugintest.Instance) { p.Set(paramDistance, 0) }, 1, 1)
	for _, s := range speakers {
		if !near(l[s.ch], 2/math.Sqrt(5)) { // two coherent inputs, each 1/sqrt(5)
			t.Fatalf("distance 0: %v", l)
		}
	}
	// Center Level 0: the center speaker is silent, a phantom center instead.
	l = levels(t, func(p *plugintest.Instance) { p.Set(paramWidth, 0); p.Set(paramCenterLevel, 0) }, 1, 0)
	if l[chC] != 0 || !near(l[chL], math.Sqrt2/2) || !near(l[chR], math.Sqrt2/2) {
		t.Fatalf("center level 0: %v", l)
	}
}

func TestSurroundLFESend(t *testing.T) {
	p := plugintest.New(t, surroundPlugin{})
	p.Set(paramLFE, -6)
	bass := plugintest.Sine(50, 48000, 48000, 0.5)
	treble := plugintest.Sine(2000, 48000, 48000, 0.5)
	out := p.Render([][]float32{bass, bass}, numChannels)
	if db := plugintest.PeakDB(out[chLFE][24000:]) - plugintest.DB(0.5); math.Abs(db+6) > 0.5 {
		t.Errorf("50 Hz LFE send at %.2f dB, want -6", db)
	}
	out = p.Render([][]float32{treble, treble}, numChannels)
	if db := plugintest.PeakDB(out[chLFE][24000:]); db > -40 {
		t.Errorf("2 kHz reaches the LFE at %.1f dB", db)
	}

	off := plugintest.New(t, surroundPlugin{}).Render([][]float32{bass, bass}, numChannels)
	if plugintest.Peak(off[chLFE]) != 0 {
		t.Error("LFE carries signal with the send off")
	}
}

func TestSurroundAngleChangesRampWithinTheBlock(t *testing.T) {
	p := plugintest.New(t, surroundPlugin{})
	p.Set(paramWidth, 0)
	p.Render([][]float32{plugintest.Constant(512, 1), plugintest.Constant(512, 1)}, numChannels)
	p.Set(paramAngle, 30) // C -> R
	out := p.Render([][]float32{plugintest.Constant(512, 1), plugintest.Constant(512, 1)}, numChannels)
	for i := 1; i < 512; i++ {
		if out[chC][i] > out[chC][i-1] || out[chR][i] < out[chR][i-1] {
			t.Fatalf("sample %d: the move from C to R is not a ramp", i)
		}
	}
	if out[chC][0] < 1.9 || out[chR][511] < 1.99 {
		t.Fatalf("ramp from C %v to R %v", out[chC][0], out[chR][511])
	}
}

func TestSurroundDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, surroundPlugin{})
	p.Set(paramLFE, -10)
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(numChannels, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
