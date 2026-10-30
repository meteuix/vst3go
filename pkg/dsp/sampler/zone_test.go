package sampler

import (
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/audiofile"
)

func TestLayersPlayTogetherWithTheirPan(t *testing.T) {
	a, _ := NewSample([][]float32{tone(300, rate, 48000)}, rate, 60)
	b, _ := NewSample([][]float32{tone(1200, rate, 48000)}, rate, 60)
	v := NewVoice(rate, instrument(t,
		Zone{Sample: a, HighKey: 127, HighVelocity: 127, Pan: -1},
		Zone{Sample: b, HighKey: 127, HighVelocity: 127, Pan: 0.5},
	))
	l, r := play(v, 60, 127, 20000)
	al, ar := spectrum(t, l[2048:]), spectrum(t, r[2048:])
	for _, c := range []struct {
		name       string
		amp        []float64
		freq, want float64
	}{
		{"300 Hz left", al, 300, 0.5},
		{"300 Hz right", ar, 300, 0},
		{"1200 Hz left", al, 1200, 0.25}, // balance 0.5 right halves the left
		{"1200 Hz right", ar, 1200, 0.5},
	} {
		if got := at(c.amp, c.freq, 16384); math.Abs(got-c.want) > 0.01 {
			t.Errorf("%s: %.4f, want %.2f", c.name, got, c.want)
		}
	}
	// Mono output ignores pan.
	mono := make([]float32, 20000)
	v.TriggerNote(60, 127)
	v.Process(mono)
	if got := at(spectrum(t, mono[2048:]), 1200, 16384); math.Abs(got-0.5) > 0.01 {
		t.Errorf("mono 1200 Hz: %.4f", got)
	}
}

func TestRoundRobinAndRandomChoice(t *testing.T) {
	s, _ := NewSample([][]float32{{1, 1}}, rate, 60)
	m, err := NewMap(
		Zone{Sample: s, HighKey: 127, HighVelocity: 127, SeqLength: 3, SeqPosition: 1, Tune: 1},
		Zone{Sample: s, HighKey: 127, HighVelocity: 127, SeqLength: 3, SeqPosition: 2, Tune: 2},
		Zone{Sample: s, HighKey: 127, HighVelocity: 127, SeqLength: 3, SeqPosition: 3, Tune: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]*Zone, 0, MaxLayers)
	// Each key keeps its own count.
	for i, want := range []float64{1, 2, 3, 1, 2} {
		z := m.Match(60, 100, dst)
		if len(z) != 1 || z[0].Tune != want {
			t.Fatalf("note %d on key 60 played %v, want zone %v", i, z, want)
		}
		if i == 1 {
			if z := m.Match(61, 100, dst); len(z) != 1 || z[0].Tune != 1 {
				t.Fatal("key 61 did not start its own round robin")
			}
		}
	}
	if _, err := NewMap(Zone{Sample: s, HighKey: 127, HighVelocity: 127, SeqLength: 2, SeqPosition: 3}); err == nil {
		t.Error("position 3 of 2 accepted")
	}

	m, _ = NewMap(
		Zone{Sample: s, HighKey: 127, HighVelocity: 127, LowRand: 0, HighRand: 0.25, Tune: 1},
		Zone{Sample: s, HighKey: 127, HighVelocity: 127, LowRand: 0.25, HighRand: 1, Tune: 2},
	)
	counts := map[float64]int{}
	for range 4000 {
		z := m.Match(60, 100, dst)
		if len(z) != 1 {
			t.Fatalf("%d zones for one random number", len(z))
		}
		counts[z[0].Tune]++
	}
	if counts[1] < 900 || counts[1] > 1100 {
		t.Fatalf("the quarter zone played %d of 4000 notes", counts[1])
	}
}

func TestLoopModes(t *testing.T) {
	data := tone(480, rate, 9600) // 96 whole cycles
	s, _ := NewSample([][]float32{data}, rate, 60)
	s.SetLoop(0, 9600)
	render := func(z Zone, releaseAt, n int) ([]float32, *Voice) {
		z.Sample, z.HighKey, z.HighVelocity = s, 127, 127
		v := NewVoice(rate, instrument(t, z))
		v.SetADSR(0.0001, 0.001, 1, 0.005)
		v.TriggerNote(60, 127)
		out := make([]float32, n)
		v.Process(out[:releaseAt])
		if v.IsActive() {
			v.ReleaseNote()
			v.Process(out[releaseAt:])
		}
		return out, v
	}
	// LoopNone ignores the sample's loop.
	if out, v := render(Zone{Loop: LoopNone}, 20000, 30000); v.IsActive() || out[9700] != 0 {
		t.Fatal("LoopNone looped")
	}
	// OneShot plays through a note-off to the end.
	out, _ := render(Zone{Loop: OneShot}, 100, 12000)
	if peak(out[9000:9500]) < 0.4 {
		t.Fatal("OneShot stopped at note-off")
	}
	if out[9700] != 0 {
		t.Fatal("OneShot went past the sample")
	}
	// LoopSustain loops while held, then plays out: with a long release
	// it still ends at the sample's end.
	z := Zone{Loop: LoopSustain, LoopStart: 4800, LoopEnd: 9600}
	z.Sample, z.HighKey, z.HighVelocity = s, 127, 127
	v := NewVoice(rate, instrument(t, z))
	v.SetADSR(0.0001, 0.001, 1, 10)
	v.TriggerNote(60, 127)
	held := make([]float32, 30000)
	v.Process(held)
	if !v.IsActive() || peak(held[25000:]) < 0.4 {
		t.Fatal("LoopSustain stopped while held")
	}
	v.ReleaseNote()
	v.Process(make([]float32, 9600))
	if v.IsActive() {
		t.Fatal("LoopSustain kept looping after note-off")
	}
	// An Offset starts later in the sample.
	out, _ = render(Zone{Loop: LoopNone, Offset: 9000}, 1000, 1000)
	if out[600] != 0 || out[500] == 0 {
		t.Fatal("Offset 9000 did not leave 600 frames")
	}
}

func peak(x []float32) float64 {
	p := 0.0
	for _, v := range x {
		p = max(p, math.Abs(float64(v)))
	}
	return p
}

func TestKeyTrackAndFixedPitch(t *testing.T) {
	s, _ := NewSample([][]float32{tone(1000, rate, 96000)}, rate, 60)
	s.SetLoop(0, 96000)
	for _, c := range []struct {
		z    Zone
		want float64
	}{
		{Zone{FixedPitch: true}, 1000},
		{Zone{KeyTrack: 50}, 1000 * math.Exp2(6.0/12)}, // 12 keys up, half tracking
		{Zone{}, 2000},
	} {
		c.z.Sample, c.z.HighKey, c.z.HighVelocity = s, 127, 127
		l, _ := play(NewVoice(rate, instrument(t, c.z)), 72, 127, 20000)
		if a := at(spectrum(t, l[2048:]), c.want, 16384); math.Abs(a-0.5) > 0.02 {
			t.Errorf("%+v: %.4f at %.1f Hz", c.z, a, c.want)
		}
	}
}

func TestLayeredVoiceDoesNotAllocate(t *testing.T) {
	s, _ := NewSample([][]float32{tone(300, rate, 480000)}, rate, 60)
	zones := make([]Zone, MaxLayers)
	for i := range zones {
		zones[i] = Zone{Sample: s, HighKey: 127, HighVelocity: 127, Pan: float64(i)/4 - 1}
	}
	v := NewVoice(rate, instrument(t, zones...))
	l := make([]float32, 512)
	r := make([]float32, 512)
	allocs := testing.AllocsPerRun(20, func() {
		v.TriggerNote(70, 100)
		v.Render(l, r)
		v.ReleaseNote()
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

// writeWAV saves a mono tone of freq Hz.
func writeWAV(t *testing.T, path string, freq float64, n int) {
	t.Helper()
	if err := audiofile.Save(path, &audiofile.Buffer{SampleRate: rate, Channels: [][]float32{tone(freq, rate, n)}}, audiofile.Float32); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSFZ(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "samples"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWAV(t, filepath.Join(dir, "samples", "soft a.wav"), 440, 48000)
	writeWAV(t, filepath.Join(dir, "samples", "loud.wav"), 440, 48000)
	writeWAV(t, filepath.Join(dir, "samples", "rr2.wav"), 880, 48000)
	if err := os.WriteFile(filepath.Join(dir, "common.sfzh"), []byte("<global> loop_mode=one_shot ampeg_release=0.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sfz := `// test instrument
#define $LOUD 64
#include "common.sfzh"
<control> default_path=samples\ note_offset=0
<group> lokey=c4 hikey=b4 pitch_keycenter=a4 /* the tones are A4 */
<region> sample=soft a.wav hivel=63 pan=-100
<region> sample=loud.wav lovel=$LOUD volume=-6 seq_length=2 seq_position=1
<region> sample=rr2.wav lovel=$LOUD seq_length=2 seq_position=2 tune=-1200
<region> sample=loud.wav trigger=release
<region> sample=*sine
<group> key=d6
<region> sample=loud.wav pitch_keytrack=0 loop_mode=no_loop
<curve> v000=0
`
	path := filepath.Join(dir, "piano.sfz")
	if err := os.WriteFile(path, []byte(sfz), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := LoadSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Regions != 4 || inst.Samples != 3 || inst.Skipped != 2 {
		t.Fatalf("%d regions, %d samples, %d skipped; want 4, 3, 2", inst.Regions, inst.Samples, inst.Skipped)
	}
	if want := []string{"<curve>", "ampeg_release"}; len(inst.Ignored) != 2 || inst.Ignored[0] != want[0] || inst.Ignored[1] != want[1] {
		t.Fatalf("ignored %v, want %v", inst.Ignored, want)
	}
	var p = instrumentOf(inst.Map)
	freq := func(note, velocity uint8) (float64, float64, float64) {
		l, r := play(NewVoice(rate, p), note, velocity, 20000)
		al, ar := spectrum(t, l[2048:]), spectrum(t, r[2048:])
		f := 440 * math.Exp2(float64(int(note)-69)/12)
		return f, at(al, f, 16384), at(ar, f/2, 16384) + at(ar, f, 16384)
	}
	// Soft: panned hard left, at the played pitch (keycenter A4).
	vel := uint8(40)
	g := float64(vel) / 127 * float64(vel) / 127 * 0.5
	if f, l, r := freq(64, vel); math.Abs(l-g) > 0.01 || r > 0.001 {
		t.Errorf("soft region at %.1f Hz: left %.4f right %.4f, want %.4f and 0", f, l, r, g)
	}
	// Loud alternates between loud.wav at -6 dB and rr2.wav an octave
	// lower (880 Hz tuned down to 440 Hz).
	_, l1, _ := freq(69, 127)
	_, l2, _ := freq(69, 127)
	if math.Abs(l1-0.5*math.Pow(10, -6.0/20)) > 0.01 || math.Abs(l2-0.5) > 0.01 {
		t.Errorf("round robin levels %.4f and %.4f, want 0.25 and 0.5", l1, l2)
	}
	// key=d6 at a fixed pitch.
	l, _ := play(NewVoice(rate, p), 86, 127, 20000)
	if a := at(spectrum(t, l[2048:]), 440, 16384); math.Abs(a-0.5) > 0.01 {
		t.Errorf("fixed-pitch region: %.4f at 440 Hz", a)
	}
	if z := inst.Map.Match(85, 127, nil); len(z) != 0 {
		t.Error("key=d6 also covers 85")
	}
}

func instrumentOf(m *Map) *atomic.Pointer[Map] {
	var p atomic.Pointer[Map]
	p.Store(m)
	return &p
}

func TestLoadSFZErrors(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"empty.sfz":   "<region> sample=*sine\n",
		"missing.sfz": "<region> sample=nothere.wav\n",
		"orphan.sfz":  "lokey=1\n<region> sample=x.wav\n",
		"badkey.sfz":  "<region> sample=x.wav lokey=h9\n",
		"loop.sfz":    "#include \"loop.sfz\"\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSFZ(path); err == nil {
			t.Errorf("%s loaded", name)
		}
	}
}

func TestSFZNoteNames(t *testing.T) {
	for v, want := range map[string]int{"c4": 60, "C#4": 61, "db4": 61, "a4": 69, "c-1": 0, "g9": 127, "37": 37} {
		if n, err := sfzNote(v); err != nil || n != want {
			t.Errorf("%s: %d, %v; want %d", v, n, err, want)
		}
	}
}
