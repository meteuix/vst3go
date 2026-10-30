package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/pkg/plugintest"
)

func TestDelayEchoesWithFeedback(t *testing.T) {
	p := plugintest.New(t, delayPlugin{})
	p.Set(paramDelayTime, 100) // 4800 samples
	p.Set(paramFeedback, 50)
	p.Set(paramMix, 50)
	out := p.Render(plugintest.Channels(2, plugintest.Impulse(20000, 0)), 2)

	// Dry impulse at 0, then echoes every 4800 samples halving each time.
	want := map[int]float32{0: 0.5, 4800: 0.5, 9600: 0.25, 14400: 0.125, 19200: 0.0625}
	for ch := range out {
		for i, v := range out[ch] {
			w := want[i]
			if math.Abs(float64(v-w)) > 1e-6 {
				t.Fatalf("channel %d sample %d = %v, want %v", ch, i, v, w)
			}
		}
	}
}

func TestDelayTimeAutomationGlides(t *testing.T) {
	p := plugintest.New(t, delayPlugin{})
	p.Set(paramDelayTime, 10)
	p.Set(paramFeedback, 0)
	p.Set(paramMix, 100)
	p.Render(plugintest.Silence(2, 4800), 2)

	// A steady sine through a delay whose time jumps: smoothing turns the
	// jump into a short pitch glide, so the output never steps. 2.5 ms is
	// half a period: without smoothing the output would jump by up to 1.
	if err := p.Automate(paramDelayTime, 100, p.Normalized(paramDelayTime, 12.5)); err != nil {
		t.Fatal(err)
	}
	in := plugintest.Channels(2, plugintest.Sine(200, 48000, 9600, 0.5))
	out := p.Render(in, 2)
	maxStep := 0.0
	for i := 1000; i < len(out[0]); i++ {
		maxStep = max(maxStep, math.Abs(float64(out[0][i]-out[0][i-1])))
	}
	// A 200 Hz sine at 0.5 moves at most 2*pi*200/48000*0.5 = 0.013 per
	// sample; a glide raises that only slightly.
	if maxStep > 0.02 {
		t.Fatalf("output stepped by %v; the delay time change was not smoothed", maxStep)
	}
}

func TestDelayDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, delayPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	v := 0.0
	if allocs := p.AllocsPerProcess(20, in, out, func() {
		v = 1 - v
		_ = p.Automate(paramDelayTime, 10, v)
	}); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
