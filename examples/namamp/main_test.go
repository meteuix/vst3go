package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/plugintest"
)

// writeModel writes a Linear model that scales its input by g, trained
// at rate, stating loudness when it is not NaN.
func writeModel(t *testing.T, g float32, rate, loudness float64) string {
	t.Helper()
	metadata := map[string]any{"name": "Half", "gear_make": "Test", "gear_model": "Box"}
	if !math.IsNaN(loudness) {
		metadata["loudness"] = loudness
	}
	data, err := json.Marshal(map[string]any{
		"version":      "0.5.4",
		"architecture": "Linear",
		"config":       map[string]any{"receptive_field": 1, "bias": false},
		"weights":      []float32{g},
		"sample_rate":  rate,
		"metadata":     metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.nam")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// load loads path through the editor and waits for it.
func load(t *testing.T, p *plugintest.Instance, path string) loadStatus {
	t.Helper()
	if err := p.EditorMessage("load", map[string]string{"path": path}); err != nil {
		t.Fatal(err)
	}
	return waitLoaded(t, p)
}

// waitLoaded polls the editor status until the load in progress ends.
func waitLoaded(t *testing.T, p *plugintest.Instance) loadStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := p.EditorMessage("status", nil); err != nil {
			t.Fatal(err)
		}
		for _, m := range p.TakeEditorMessages() {
			var s loadStatus
			if err := json.Unmarshal(m.Data, &s); err != nil {
				t.Fatal(err)
			}
			if !s.Loading {
				return s
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("load did not finish")
	return loadStatus{}
}

// checkScaled fails unless out[i] is g·in[i-delay] on every channel.
func checkScaled(t *testing.T, in []float32, out [][]float32, g float64, delay int, tol float64) {
	t.Helper()
	for ch, y := range out {
		for i := delay + 200; i < len(y); i++ {
			if d := math.Abs(float64(y[i]) - g*float64(in[i-delay])); d > tol {
				t.Fatalf("channel %d frame %d: %v, want %v", ch, i, y[i], g*float64(in[i-delay]))
			}
		}
	}
}

func TestNAMAmpPassesThroughWithoutAModel(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	p.Set(paramInput, 6)
	p.Set(paramOutput, -6)
	in := plugintest.Sine(440, 48000, 9600, 0.5)
	out := p.Render([][]float32{in, plugintest.Constant(9600, 0.25)}, 2)
	checkScaled(t, in, out[:1], 1, 0, 1e-6)
	if math.Abs(float64(out[1][5000])-0.25) > 1e-6 {
		t.Fatalf("right channel %v, want 0.25", out[1][5000])
	}
}

func TestNAMAmpRunsTheModel(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	s := load(t, p, writeModel(t, 0.5, 48000, -12))
	if s.Error != "" || s.Name != "Half" || s.Gear != "Test Box" || s.Architecture != "Linear" || s.SampleRate != 48000 || s.Latency != 0 {
		t.Fatalf("status %+v", s)
	}
	in := plugintest.Sine(440, 48000, 9600, 0.4)
	// Mono into both inputs; Normalize brings -12 dB to -18 dB.
	out := p.Render(plugintest.Channels(2, in), 2)
	checkScaled(t, in, out, 0.5*math.Pow(10, -6.0/20), 0, 1e-6)

	p.Set(paramNormalize, 0)
	out = p.Render(plugintest.Channels(2, in), 2)
	checkScaled(t, in, out, 0.5, 0, 1e-6)

	// The model sees the mix of the inputs.
	out = p.Render([][]float32{in, plugintest.Silence(1, 9600)[0]}, 2)
	checkScaled(t, in, out, 0.25, 0, 1e-6)
}

func TestNAMAmpWithoutLoudnessIsNotNormalized(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	load(t, p, writeModel(t, 0.5, 48000, math.NaN()))
	in := plugintest.Sine(440, 48000, 4800, 0.4)
	checkScaled(t, in, p.Render(plugintest.Channels(2, in), 2), 0.5, 0, 1e-6)
}

func TestNAMAmpResamplesAndAnnouncesLatency(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	p.TakeRestarts()
	s := load(t, p, writeModel(t, 0.5, 44100, math.NaN()))
	if s.Latency <= 0 || s.Latency != p.Latency() {
		t.Fatalf("status latency %d, plugin latency %d", s.Latency, p.Latency())
	}
	if p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatal("latency not announced")
	}
	in := plugintest.Sine(1000, 48000, 24000, 0.4)
	out := p.Render(plugintest.Channels(2, in), 2)
	checkScaled(t, in, out, 0.5, s.Latency, 1e-3)

	// Unloading returns to no latency.
	load(t, p, "")
	if p.Latency() != 0 || p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatalf("latency %d after unloading, want 0 and announced", p.Latency())
	}
}

func TestNAMAmpFollowsTheSampleRate(t *testing.T) {
	path := writeModel(t, 0.5, 48000, math.NaN())
	p := plugintest.New(t, namAmpPlugin{}, plugin.OfflineSetup{SampleRate: 96000})
	if s := load(t, p, path); s.Latency <= 0 {
		t.Fatalf("a 48 kHz model at 96 kHz has latency %d", s.Latency)
	}
	in := plugintest.Sine(1000, 96000, 24000, 0.4)
	checkScaled(t, in, p.Render(plugintest.Channels(2, in), 2), 0.5, p.Latency(), 1e-3)
}

func TestNAMAmpStateReloadsTheModel(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	path := writeModel(t, 0.5, 48000, math.NaN())
	load(t, p, path)
	state, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	q := plugintest.New(t, namAmpPlugin{})
	if err := q.SetState(state); err != nil {
		t.Fatal(err)
	}
	if s := waitLoaded(t, q); s.Path != path {
		t.Fatalf("restored status %+v", s)
	}
	in := plugintest.Sine(440, 48000, 4800, 0.4)
	checkScaled(t, in, q.Render(plugintest.Channels(2, in), 2), 0.5, 0, 1e-6)
}

func TestNAMAmpKeepsTheModelWhenALoadFails(t *testing.T) {
	p := plugintest.New(t, namAmpPlugin{})
	path := writeModel(t, 0.5, 48000, math.NaN())
	load(t, p, path)
	bad := filepath.Join(t.TempDir(), "bad.nam")
	if err := os.WriteFile(bad, []byte(`{"architecture":"ConvNet","config":{},"weights":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := load(t, p, bad); s.Error == "" {
		t.Fatalf("no error for an unsupported model: %+v", s)
	}
	in := plugintest.Sine(440, 48000, 4800, 0.4)
	checkScaled(t, in, p.Render(plugintest.Channels(2, in), 2), 0.5, 0, 1e-6)
	state, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	q := plugintest.New(t, namAmpPlugin{})
	if err := q.SetState(state); err != nil {
		t.Fatal(err)
	}
	if s := waitLoaded(t, q); s.Path != path {
		t.Fatalf("the failed path replaced the saved one: %+v", s)
	}
}

func TestNAMAmpDoesNotAllocate(t *testing.T) {
	for _, rate := range []float64{48000, 44100} {
		p := plugintest.New(t, namAmpPlugin{}, plugin.OfflineSetup{SampleRate: rate})
		if s := load(t, p, "../../pkg/dsp/nam/testdata/wavenet.nam"); s.Error != "" {
			t.Fatal(s.Error)
		}
		in := plugintest.Channels(2, plugintest.Sine(110, rate, 512, 0.5))
		if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
			t.Fatalf("%v Hz: %v allocations per block", rate, allocs)
		}
	}
}
