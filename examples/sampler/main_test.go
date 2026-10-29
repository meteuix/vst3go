package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/th-release/vst3go/pkg/audiofile"
	"github.com/th-release/vst3go/pkg/plugintest"
)

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

// pitchOf plays a held note and measures its frequency.
func pitchOf(t *testing.T, p *plugintest.Instance, note uint8) float64 {
	t.Helper()
	p.Note(0, note, 1, 0, 23999)
	out := p.RenderSilence(24000, 0, 2)
	return plugintest.ZeroCrossingFrequency(out[0][4800:], 48000)
}

// writeTone writes a mono 1 kHz tone recorded at 44.1 kHz.
func writeTone(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tone.wav")
	b := &audiofile.Buffer{SampleRate: 44100, Channels: [][]float32{plugintest.Sine(1000, 44100, 44100, 0.5)}}
	if err := audiofile.Save(path, b, audiofile.Float32); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSamplerBuiltInTone(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	p.Note(0, 72, 1, 300, 0)
	out := p.RenderSilence(24000, 0, 2)
	if at := plugintest.FirstNonZero(out[0], 0); at != 300 {
		t.Fatalf("note starts at %d, want 300", at)
	}
	if f := plugintest.ZeroCrossingFrequency(out[0][4800:], 48000); math.Abs(f-523.25) > 1 {
		t.Fatalf("C5 at %.2f Hz", f)
	}
	// The loop holds the note past the end of the 0.77 s sample.
	out = p.RenderSilence(96000, 0, 2)
	if rms := plugintest.RMS(out[0][48000:]); rms < 0.1 {
		t.Fatalf("held note faded to %.3f RMS", rms)
	}
}

func TestSamplerLoadsAFile(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	path := writeTone(t)
	if err := p.EditorMessage("load", map[string]string{"path": path}); err != nil {
		t.Fatal(err)
	}
	s := waitLoaded(t, p)
	if s.Error != "" || s.Path != path || s.Channels != 1 || s.RootNote != defaultRoot || math.Abs(s.Seconds-1) > 1e-9 {
		t.Fatalf("status %+v", s)
	}
	// Root C4 at 1 kHz: G4 is a fifth up, resampled from 44.1 kHz.
	if f, want := pitchOf(t, p, 67), 1000*math.Exp2(7.0/12); math.Abs(f-want) > 1 {
		t.Fatalf("G4 at %.2f Hz, want %.2f", f, want)
	}
	p.Set(paramTune, -12)
	p.RenderSilence(48000, 0, 2) // let the note release
	if f := pitchOf(t, p, 60); math.Abs(f-500) > 1 {
		t.Fatalf("C4 tuned an octave down at %.2f Hz, want 500", f)
	}
}

func TestSamplerStateReloadsTheFile(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	path := writeTone(t)
	if err := p.EditorMessage("load", map[string]string{"path": path}); err != nil {
		t.Fatal(err)
	}
	waitLoaded(t, p)
	state, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	q := plugintest.New(t, samplerPlugin{})
	if err := q.SetState(state); err != nil {
		t.Fatal(err)
	}
	if s := waitLoaded(t, q); s.Path != path {
		t.Fatalf("restored status %+v", s)
	}
	if f := pitchOf(t, q, 60); math.Abs(f-1000) > 1 {
		t.Fatalf("restored instance plays %.2f Hz, want 1000", f)
	}
}

func TestSamplerKeepsTheInstrumentWhenALoadFails(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	if err := p.EditorMessage("load", map[string]string{"path": filepath.Join(t.TempDir(), "missing.wav")}); err != nil {
		t.Fatal(err)
	}
	if s := waitLoaded(t, p); s.Error == "" {
		t.Fatalf("no error for a missing file: %+v", s)
	}
	if f := pitchOf(t, p, 72); math.Abs(f-523.25) > 1 {
		t.Fatalf("built-in tone replaced: %.2f Hz", f)
	}
	state, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	q := plugintest.New(t, samplerPlugin{})
	if err := q.SetState(state); err != nil {
		t.Fatal(err)
	}
	if s := waitLoaded(t, q); s.Path != "" || s.Error != "" {
		t.Fatalf("failed path was saved: %+v", s)
	}
}

func TestSamplerDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	out := plugintest.Silence(2, 512)
	allocs := p.AllocsPerProcess(20, nil, out, func() {
		p.Note(0, 60, 0.8, 10, 100)
		p.Note(0, 79, 0.8, 200, 0)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
