package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/th-release/vst3go/v2/pkg/audiofile"
	"github.com/th-release/vst3go/v2/pkg/dsp/sampler"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// TestMain keeps the streamer's cache files out of the user's cache
// directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sampler-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	os.Setenv("XDG_CACHE_HOME", dir)
	os.Setenv("LocalAppData", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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

// pitchOf plays a held note and measures its frequency. The render runs
// faster than real time, so it stays within the preloaded start of the
// sample (sampler.DefaultPreload frames, 0.74 s at 44.1 kHz): a G4 from a C4
// root recorded at 44.1 kHz reads 27,500 source frames in 20,000 output
// frames at 48 kHz.
func pitchOf(t *testing.T, p *plugintest.Instance, note uint8) float64 {
	t.Helper()
	st, err := streamer()
	if err != nil {
		t.Fatal(err)
	}
	before := st.Underruns()
	p.Note(0, note, 1, 0, 19999)
	out := p.RenderSilence(20000, 0, 2)
	if u := st.Underruns() - before; u != 0 {
		t.Fatalf("note %d: %d underruns", note, u)
	}
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

func TestSamplerStandardQuality(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	p.Set(paramQuality, float64(sampler.QualityStandard))
	p.Note(0, 79, 1, 0, 23999)
	out := p.RenderSilence(24000, 0, 2)
	if f, want := plugintest.ZeroCrossingFrequency(out[0][4800:], 48000), 440*math.Exp2(10.0/12); math.Abs(f-want) > 1 {
		t.Fatalf("G5 at %.2f Hz, want %.2f", f, want)
	}
	if rms := plugintest.RMS(out[0][4800:]); rms < 0.1 {
		t.Fatalf("G5 at %.3f RMS", rms)
	}
}

func TestSamplerLoadsAFile(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	path := writeTone(t)
	if err := p.EditorMessage("load", map[string]string{"path": path}); err != nil {
		t.Fatal(err)
	}
	s := waitLoaded(t, p)
	if s.Error != "" || s.Path != path || s.Channels != 1 || s.RootNote != sampler.DefaultRoot || math.Abs(s.Seconds-1) > 1e-9 {
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

func TestSamplerLoadsAnSFZInstrument(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	dir := filepath.Dir(writeTone(t))
	sfz := filepath.Join(dir, "split.sfz")
	// The 1 kHz tone at C4 below middle C (keycenter c4) and an octave
	// lower above it (keycenter c5).
	text := "<region> sample=tone.wav hikey=59 pitch_keycenter=c4\n" +
		"<region> sample=tone.wav lokey=60 pitch_keycenter=c5 ampeg_attack=0.1\n"
	if err := os.WriteFile(sfz, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.EditorMessage("load", map[string]string{"path": sfz}); err != nil {
		t.Fatal(err)
	}
	s := waitLoaded(t, p)
	if s.Error != "" || s.Regions != 2 || s.Samples != 1 || len(s.Ignored) != 1 || s.Ignored[0] != "ampeg_attack" {
		t.Fatalf("status %+v", s)
	}
	if f := pitchOf(t, p, 59); math.Abs(f-1000*math.Exp2(-1.0/12)) > 1 {
		t.Fatalf("B3 at %.2f Hz", f)
	}
	p.RenderSilence(48000, 0, 2)
	if f := pitchOf(t, p, 60); math.Abs(f-500) > 1 {
		t.Fatalf("C4 at %.2f Hz, want 500", f)
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

// TestSamplerStreamsFromDisk plays a file longer than the preloaded start:
// the rest streams from disk while the note plays.
func TestSamplerStreamsFromDisk(t *testing.T) {
	p := plugintest.New(t, samplerPlugin{})
	path := filepath.Join(t.TempDir(), "long.wav")
	b := &audiofile.Buffer{SampleRate: 48000, Channels: [][]float32{plugintest.Sine(1000, 48000, 4*48000, 0.5)}}
	if err := audiofile.Save(path, b, audiofile.Float32); err != nil {
		t.Fatal(err)
	}
	if err := p.EditorMessage("load", map[string]string{"path": path}); err != nil {
		t.Fatal(err)
	}
	s := waitLoaded(t, p)
	// The file is 0.73 MB of float audio; the start and its half-rate
	// copies stay in memory.
	if s.Error != "" || s.Seconds != 4 || s.MemoryMB <= 0 || s.MemoryMB > 0.4 {
		t.Fatalf("status %+v", s)
	}
	st, err := streamer()
	if err != nil {
		t.Fatal(err)
	}
	before := st.Underruns()
	p.Note(0, 60, 1, 0, 0)
	var tail []float32
	for block := 0; block < 3*48000/512; block++ {
		out := p.RenderSilence(512, 0, 2)
		if block >= 2*48000/512 {
			tail = append(tail, out[0]...)
		}
		time.Sleep(200 * time.Microsecond)
	}
	if u := st.Underruns() - before; u != 0 {
		t.Fatalf("%d underruns", u)
	}
	// The third second is well past the preloaded 0.68 s.
	if f := plugintest.ZeroCrossingFrequency(tail, 48000); math.Abs(f-1000) > 1 {
		t.Fatalf("streamed part at %.2f Hz", f)
	}
	if rms := plugintest.RMS(tail); rms < 0.1 {
		t.Fatalf("streamed part RMS %.3f", rms)
	}
}
