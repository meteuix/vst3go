package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/dsp/distortion"
	"github.com/th-release/vst3go/pkg/plugintest"
)

func newEngine(t *testing.T, engine int) *plugintest.Instance {
	p := plugintest.New(t, multiDistortionPlugin{})
	p.Set(paramType, float64(engine))
	return p
}

// thirdHarmonic returns the level of the 3 kHz component relative to the
// 1 kHz fundamental, in dB.
func thirdHarmonic(x []float32) float64 {
	bin := func(freq float64) float64 {
		var re, im float64
		for i, v := range x {
			phase := 2 * math.Pi * freq * float64(i) / 48000
			re += float64(v) * math.Cos(phase)
			im -= float64(v) * math.Sin(phase)
		}
		return math.Hypot(re, im)
	}
	return plugintest.DB(bin(3000) / bin(1000))
}

func TestMultiDistortionEnginesDistort(t *testing.T) {
	in := plugintest.Channels(2, plugintest.Sine(1000, 48000, 9600, 0.5))
	for _, engine := range []int{engineWaveshaper, engineTube, engineTape} {
		p := newEngine(t, engine)
		p.Set(paramDrive, 100)
		out := p.Render(in, 2)
		if h := thirdHarmonic(out[0][4800:]); h < -30 {
			t.Errorf("engine %d: 3rd harmonic at %.1f dB, want audible distortion", engine, h)
		}
		if peak := plugintest.Peak(out[1]); peak > 1.5 || peak < 0.1 {
			t.Errorf("engine %d: output peak %v", engine, peak)
		}
	}
}

func TestMultiDistortionWaveshaperHardClip(t *testing.T) {
	p := newEngine(t, engineWaveshaper)
	p.Set(paramWaveCurve, float64(distortion.CurveHardClip))
	p.Set(paramDrive, 100) // 20x
	p.Set(paramOutput, -6)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(100, 48000, 4800, 0.5)), 2)
	if peak := plugintest.PeakDB(out[0][2400:]); math.Abs(peak+6) > 0.01 {
		t.Fatalf("clipped and attenuated peak %.2f dB, want -6", peak)
	}
}

func TestMultiDistortionBitcrusher(t *testing.T) {
	p := newEngine(t, engineBitcrusher)
	p.Set(paramDrive, 0)
	p.Set(paramBitDepth, 3)
	p.Set(paramSampleReduction, 4)
	p.Set(paramAntiAlias, 0)
	out := p.Render(plugintest.Channels(2, plugintest.Sine(100, 48000, 4800, 0.9)), 2)

	step := 2.0 / 8
	for i, x := range out[0] {
		if q := float64(x) / step; math.Abs(q-math.Round(q)) > 1e-6 {
			t.Fatalf("sample %d = %v is not a multiple of the 3-bit step %v", i, x, step)
		}
		if i%4 != 0 && x != out[0][i-1] {
			t.Fatalf("sample %d changed inside a 4-sample hold", i)
		}
	}
}

func TestMultiDistortionMixZeroIsDry(t *testing.T) {
	for engine := range 4 {
		p := newEngine(t, engine)
		p.Set(paramDrive, 100)
		p.Set(paramMix, 0)
		in := plugintest.Channels(2, plugintest.Sine(440, 48000, 4800, 0.5))
		out := p.Render(in, 2)
		// After the 20 ms Mix smoothing ramp from its 100% default.
		for i := 960; i < len(out[0]); i++ {
			if out[0][i] != in[0][i] {
				t.Fatalf("engine %d: 0%% mix changed sample %d", engine, i)
			}
		}
	}
}

func TestMultiDistortionDoesNotAllocate(t *testing.T) {
	for engine := range 4 {
		p := newEngine(t, engine)
		p.Set(paramTapeFlutter, 50)
		p.Set(paramDither, float64(distortion.DitherTriangular))
		in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
		if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
			t.Fatalf("engine %d: %v allocations per block", engine, allocs)
		}
	}
}
