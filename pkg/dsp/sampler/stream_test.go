package sampler

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meteuix/vst3go/v2/pkg/audiofile"
)

// longSample writes a stereo float WAV of n frames (a sweep left, noise
// right) and returns its path.
func longSample(t *testing.T, n int) string {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	l, r := make([]float32, n), make([]float32, n)
	phase := 0.0
	for i := range l {
		phase += 2 * math.Pi * (100 + 4000*float64(i)/float64(n)) / rate
		l[i] = float32(0.5 * math.Sin(phase))
		r[i] = float32(rng.Float64() - 0.5)
	}
	path := filepath.Join(t.TempDir(), "long.wav")
	if err := audiofile.Save(path, &audiofile.Buffer{SampleRate: rate, Channels: [][]float32{l, r}}, audiofile.Float32); err != nil {
		t.Fatal(err)
	}
	return path
}

func newStreamer(t *testing.T, opts StreamerOptions) *Streamer {
	t.Helper()
	st, err := NewStreamer(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st
}

// render plays a note on v in blocks of 256 frames, pausing between
// blocks as a real-time host does, so the streamer keeps ahead.
func render(v *Voice, n int, release int, pace time.Duration) ([]float32, []float32) {
	l, r := make([]float32, n), make([]float32, n)
	for start := 0; start < n; start += 256 {
		if start >= release && release >= 0 {
			v.ReleaseNote()
			release = -1
		}
		end := min(start+256, n)
		v.Render(l[start:end], r[start:end])
		time.Sleep(pace)
	}
	return l, r
}

func closeTo(t *testing.T, name string, got, want []float32) {
	t.Helper()
	for i := range want {
		if d := math.Abs(float64(got[i] - want[i])); d > 1e-5 {
			t.Fatalf("%s: frame %d = %g, from memory %g", name, i, got[i], want[i])
		}
	}
}

// TestStreamedMatchesMemory plays the same notes from memory and from
// disk: a pitched-up note through the end of the sample, and a sustain
// loop released into the sample's tail.
func TestStreamedMatchesMemory(t *testing.T) {
	const frames = 6 * streamWindow
	path := longSample(t, frames)
	st := newStreamer(t, StreamerOptions{Preload: 4096, Streams: 4})
	memory, err := LoadSample(path)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.LoadSample(path)
	if err != nil {
		t.Fatal(err)
	}
	if !disk.Streamed() || memory.Streamed() || disk.Len() != frames || disk.Channels() != 2 || disk.RootNote() != DefaultRoot {
		t.Fatalf("streamed sample: %v, %d frames, %d channels, root %v", disk.Streamed(), disk.Len(), disk.Channels(), disk.RootNote())
	}
	if m, d := memory.MemoryBytes(), disk.MemoryBytes(); d*10 > m {
		t.Fatalf("streamed sample keeps %d bytes, the memory one %d", d, m)
	}

	for _, tc := range []struct {
		name    string
		zone    func(s *Sample) Zone
		note    uint8
		release int
	}{
		{"pitched up", func(s *Sample) Zone {
			return Zone{Sample: s, HighKey: 127, HighVelocity: 127, Loop: LoopNone}
		}, 67, -1},
		{"offset and sustain loop", func(s *Sample) Zone {
			return Zone{Sample: s, HighKey: 127, HighVelocity: 127, Offset: 1000, Loop: LoopSustain, LoopStart: 2*streamWindow + 77, LoopEnd: 3*streamWindow + 5000}
		}, 58, 4 * streamWindow},
	} {
		play := func(s *Sample) ([]float32, []float32) {
			v := NewVoice(rate, instrument(t, tc.zone(s)))
			v.SetADSR(0.0001, 0.001, 1, 0.05)
			v.TriggerNote(tc.note, 127)
			return render(v, frames+2*streamWindow, tc.release, 200*time.Microsecond)
		}
		before := st.Underruns()
		ml, mr := play(memory)
		dl, dr := play(disk)
		if u := st.Underruns() - before; u != 0 {
			t.Fatalf("%s: %d underruns", tc.name, u)
		}
		closeTo(t, tc.name+" left", dl, ml)
		closeTo(t, tc.name+" right", dr, mr)
	}
	if err := st.Err(); err != nil {
		t.Fatal(err)
	}
	for i := range st.streams {
		if st.streams[i].inUse.Load() {
			t.Fatalf("stream %d still taken after the voices ended", i)
		}
	}
}

// TestStreamUnderrun runs out of streams: the note without one plays its
// preloaded start, then silence, and counts underruns.
func TestStreamUnderrun(t *testing.T) {
	path := longSample(t, 4*streamWindow)
	st := newStreamer(t, StreamerOptions{Preload: 4096, Streams: 1})
	s, err := st.LoadSample(path)
	if err != nil {
		t.Fatal(err)
	}
	inst := instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127})
	first, second := NewVoice(rate, inst), NewVoice(rate, inst)
	first.TriggerNote(60, 127)
	second.TriggerNote(60, 127)
	if first.layers[0].stream == nil || second.layers[0].stream != nil {
		t.Fatal("the first note should take the only stream")
	}
	_, r := render(second, 3*streamWindow, -1, 0)
	if st.Underruns() == 0 {
		t.Fatal("no underrun counted")
	}
	if r[1000] == 0 {
		t.Fatal("the preloaded start did not play")
	}
	for i := 4096 + 300; i < len(r); i++ {
		if r[i] != 0 {
			t.Fatalf("frame %d = %g beyond the preload without a stream", i, r[i])
		}
	}
	first.Stop()
	if st.streams[0].inUse.Load() {
		t.Fatal("Stop kept the stream")
	}
}

// TestStreamCache reuses the cache file: a second Streamer on the same
// directory opens it without decoding, and a changed source rebuilds it.
func TestStreamCache(t *testing.T) {
	path := longSample(t, 3*streamWindow)
	dir := t.TempDir()
	load := func() (*Sample, os.FileInfo) {
		st, err := NewStreamer(dir, StreamerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		s, err := st.LoadSample(path)
		if err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.smp"))
		if len(files) == 0 {
			t.Fatal("no cache file")
		}
		info, err := os.Stat(files[len(files)-1])
		if err != nil {
			t.Fatal(err)
		}
		return s, info
	}
	_, first := load()
	_, second := load()
	if !first.ModTime().Equal(second.ModTime()) {
		t.Fatal("the cache file was rewritten")
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	load()
	if files, _ := filepath.Glob(filepath.Join(dir, "*.smp")); len(files) != 2 {
		t.Fatalf("%d cache files after the source changed, want 2", len(files))
	}

	// A corrupt cache file is rebuilt.
	files, _ := filepath.Glob(filepath.Join(dir, "*.smp"))
	for _, f := range files {
		if err := os.WriteFile(f, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := load(); s.Len() != 3*streamWindow {
		t.Fatalf("rebuilt sample has %d frames", s.Len())
	}
}

// TestStreamShared loads a file twice: the Samples share the cache file
// and kept parts but not their loops, and a kept loop is read once.
func TestStreamShared(t *testing.T) {
	path := longSample(t, 4*streamWindow)
	st := newStreamer(t, StreamerOptions{Preload: 4096})
	a, err := st.LoadSample(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.LoadSample(path)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a.disk != b.disk {
		t.Fatal("loads of one file should share the cache file only")
	}
	head := a.MemoryBytes()
	if err := b.SetLoop(30000, 40000); err != nil {
		t.Fatal(err)
	}
	if a.Looped() || !b.Looped() {
		t.Fatal("SetLoop changed the other Sample")
	}
	kept := a.MemoryBytes()
	if kept <= head {
		t.Fatal("the loop was not kept in memory")
	}
	if _, err := NewMap(Zone{Sample: a, HighKey: 127, HighVelocity: 127, LoopStart: 30000, LoopEnd: 40000}); err != nil {
		t.Fatal(err)
	}
	if a.MemoryBytes() != kept {
		t.Fatal("the same loop was kept twice")
	}
}
