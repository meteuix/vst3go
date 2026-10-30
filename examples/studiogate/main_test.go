package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

func lastOutput(t *testing.T, p *plugintest.Instance, id uint32) float64 {
	t.Helper()
	changes := p.OutputParameters()
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].ParamID == id {
			return changes[i].Value
		}
	}
	t.Fatalf("parameter %d was never written", id)
	return 0
}

func renderKeyed(t *testing.T, p *plugintest.Instance, main, key [][]float32) [][]float32 {
	t.Helper()
	out := plugintest.Silence(2, len(main[0]))
	if err := p.ProcessBuses([][][]float32{main, key}, [][][]float32{out}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStudioGateMutesBelowThresholdAndOpensAbove(t *testing.T) {
	p := plugintest.New(t, studioGatePlugin{})
	p.Set(paramThreshold, -30)
	p.Set(paramRange, -60)

	quiet := p.Render(plugintest.Channels(2, plugintest.Sine(1000, 48000, 4800, 0.01)), 2) // -40 dB
	if db := plugintest.PeakDB(quiet[0][2400:]); db > -99 {
		t.Fatalf("closed gate output %.1f dB, want -40-60 = -100 dB", db)
	}
	if lastOutput(t, p, paramGateOpen) != 0 || lastOutput(t, p, paramReduction) != p.Normalized(paramReduction, -60) {
		t.Fatal("meters do not show a closed gate")
	}

	loud := p.Render(plugintest.Channels(2, plugintest.Sine(1000, 48000, 4800, 0.5)), 2)
	if db := plugintest.PeakDB(loud[1][2400:]); math.Abs(db-plugintest.DB(0.5)) > 0.01 {
		t.Fatalf("open gate output %.2f dB, want unchanged", db)
	}
	if lastOutput(t, p, paramGateOpen) != 1 || lastOutput(t, p, paramReduction) != p.Normalized(paramReduction, 0) {
		t.Fatal("meters do not show an open gate")
	}
	if got, want := lastOutput(t, p, paramOutputPeak), p.Normalized(paramOutputPeak, plugintest.DB(0.5)); math.Abs(got-want) > 0.001 {
		t.Fatalf("output peak %v, want %v", got, want)
	}
}

func TestStudioGateKeyHPFIgnoresLowFrequencies(t *testing.T) {
	p := plugintest.New(t, studioGatePlugin{})
	p.Set(paramThreshold, -20)
	rumble := plugintest.Channels(2, plugintest.Sine(30, 48000, 9600, 0.5)) // -6 dB at 30 Hz
	if out := p.Render(rumble, 2); plugintest.Peak(out[0][4800:]) < 0.49 {
		t.Fatal("rumble did not open the unfiltered gate")
	}

	p = plugintest.New(t, studioGatePlugin{})
	p.Set(paramThreshold, -20)
	p.Set(paramKeyHPFOn, 1)
	p.Set(paramKeyHPF, 500)
	if out := p.Render(rumble, 2); plugintest.Peak(out[0][4800:]) > 0.001 {
		t.Fatal("the key HPF let 30 Hz rumble open the gate")
	}
}

func TestStudioGateExternalKey(t *testing.T) {
	p := plugintest.New(t, studioGatePlugin{})
	p.Set(paramThreshold, -30)
	p.Set(paramRelease, 5) // fully closed well within one 100 ms render
	n := 4800
	main := plugintest.Channels(2, plugintest.Constant(n, 0.5))
	key := [][]float32{plugintest.Silence(1, n)[0], plugintest.Constant(n, 1)}

	if out := renderKeyed(t, p, main, plugintest.Silence(2, n)); out[0][n-1] != 0.5 {
		t.Fatalf("self-keyed gate output %v, want 0.5", out[0][n-1])
	}
	p.Set(paramExternalKey, 1)
	if out := renderKeyed(t, p, main, plugintest.Silence(2, n)); out[0][n-1] > 0.001 {
		t.Fatalf("silent external key left the gate open: %v", out[0][n-1])
	}
	if out := renderKeyed(t, p, main, key); out[0][n-1] != 0.5 || out[1][n-1] != 0.5 {
		t.Fatalf("loud right key channel did not open both channels: %v %v", out[0][n-1], out[1][n-1])
	}
}

func TestStudioGateDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, studioGatePlugin{})
	in := plugintest.Channels(2, plugintest.Sine(100, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	if allocs := p.AllocsPerProcess(20, in, out, nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
