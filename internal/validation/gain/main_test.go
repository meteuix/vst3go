package main

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/plugintest"
)

func TestGainAutomationIsSampleAccurate(t *testing.T) {
	p := plugintest.New(t, gainPlugin{})
	p.Set(paramGain, 0)
	// +6 dB from sample 1000, inside the second 512-sample block.
	if err := p.Automate(paramGain, 1000, (6.0+80)/92); err != nil {
		t.Fatal(err)
	}
	out := p.Render(plugintest.Channels(2, plugintest.Constant(2048, 0.5)), 2)
	g := float32(math.Pow(10, 6.0/20))
	if out[0][999] != 0.5 || math.Abs(float64(out[1][1000]-0.5*g)) > 1e-6 {
		t.Fatalf("out[999]=%v out[1000]=%v, want 0.5 then %v", out[0][999], out[1][1000], 0.5*g)
	}
	meters := p.OutputParameters()
	if len(meters) == 0 || meters[len(meters)-1].ParamID != paramLevel {
		t.Fatalf("meter output = %+v", meters)
	}
}

func TestGainBypassAndDoublePrecision(t *testing.T) {
	p := plugintest.New(t, gainPlugin{}, plugin.OfflineSetup{DoublePrecision: true})
	p.Set(paramGain, -6)
	in := [][]float64{{1, 1, 1, 1}}
	out := [][]float64{make([]float64, 4)}
	if err := p.Process64(in, out); err != nil {
		t.Fatal(err)
	}
	if want := math.Pow(10, -6.0/20); math.Abs(out[0][3]-want) > 1e-12 {
		t.Fatalf("64-bit out = %v, want %v", out[0][3], want)
	}
	p.Set(paramBypass, 1)
	if err := p.Process64(in, out); err != nil || out[0][0] != 1 {
		t.Fatalf("bypass: out = %v, err = %v", out[0][0], err)
	}
}

func TestGainRendersAudioFiles(t *testing.T) {
	p := plugintest.New(t, gainPlugin{}, plugin.OfflineSetup{SampleRate: 44100})
	p.Set(paramGain, -6)
	dir := t.TempDir()
	in := plugintest.Channels(2, plugintest.Sine(440, 44100, 9000, 0.5))
	p.SaveAudio(filepath.Join(dir, "in.flac"), in)
	loaded, rate := plugintest.LoadAudio(t, filepath.Join(dir, "in.flac"))
	if rate != 44100 || len(loaded) != 2 || len(loaded[0]) != 9000 {
		t.Fatalf("loaded %d channels of %d frames at %g Hz", len(loaded), len(loaded[0]), rate)
	}
	out := p.Render(loaded, 2)
	p.SaveAudio(filepath.Join(dir, "out.wav"), out)
	rendered, _ := plugintest.LoadAudio(t, filepath.Join(dir, "out.wav"))
	g := math.Pow(10, -6.0/20)
	for n := 4096; n < 9000; n++ { // after the gain ramp
		if d := math.Abs(float64(rendered[1][n]) - float64(in[1][n])*g); d > 1e-4 {
			t.Fatalf("frame %d = %g, want %g", n, rendered[1][n], float64(in[1][n])*g)
		}
	}
}
