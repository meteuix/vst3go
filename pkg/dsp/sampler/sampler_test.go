package sampler

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fft"
)

const rate = 48000.0

func tone(freq, sampleRate float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
	}
	return x
}

func instrument(t *testing.T, zones ...Zone) *atomic.Pointer[Map] {
	t.Helper()
	m, err := NewMap(zones...)
	if err != nil {
		t.Fatal(err)
	}
	var p atomic.Pointer[Map]
	p.Store(m)
	return &p
}

func play(v *Voice, note, velocity uint8, n int) ([]float32, []float32) {
	l := make([]float32, n)
	r := make([]float32, n)
	v.SetADSR(0.0001, 0.001, 1, 0.01)
	v.TriggerNote(note, velocity)
	for start := 0; start < n; start += 512 {
		end := min(start+512, n)
		v.Render(l[start:end], r[start:end])
	}
	return l, r
}

// spectrum returns flat-top windowed amplitudes of x[:n], n a power of two.
func spectrum(t *testing.T, x []float32) []float64 {
	t.Helper()
	n := 1
	for n*2 <= len(x) {
		n *= 2
	}
	f, err := fft.NewReal(n)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float64, n)
	sum := 0.0
	for i := range in {
		p := 2 * math.Pi * float64(i) / float64(n)
		w := 0.21557895 - 0.41663158*math.Cos(p) + 0.277263158*math.Cos(2*p) -
			0.083578947*math.Cos(3*p) + 0.006947368*math.Cos(4*p)
		in[i] = float64(x[i]) * w
		sum += w
	}
	spec := make([]complex128, f.Bins())
	f.Forward(in, spec)
	amp := make([]float64, len(spec))
	for k, z := range spec {
		amp[k] = 2 * math.Hypot(real(z), imag(z)) / sum
	}
	return amp
}

func at(amp []float64, freq float64, n int) float64 {
	c := int(math.Round(freq * float64(n) / rate))
	best := 0.0
	for k := max(0, c-3); k <= min(len(amp)-1, c+3); k++ {
		best = max(best, amp[k])
	}
	return best
}

func TestVoicePlaysAtThePitchOfTheNote(t *testing.T) {
	// Two seconds of whole cycles, looped so high notes keep sounding.
	s, err := NewSample([][]float32{tone(1000, 44100, 88200)}, 44100, 69)
	if err != nil {
		t.Fatal(err)
	}
	s.SetLoop(0, 88200)
	inst := instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127})
	for _, q := range []Quality{QualityHigh, QualityStandard} {
		for note, want := range map[uint8]float64{69: 1000, 81: 2000, 57: 500, 76: 1000 * math.Exp2(7.0/12), 93: 4000} {
			v := NewVoice(rate, inst)
			v.SetQuality(q)
			l, r := play(v, note, 127, 40000)
			amp := spectrum(t, l[4096:])
			if a := at(amp, want, 32768); math.Abs(a-0.5) > 0.01 {
				t.Errorf("quality %d note %d: %.4f at %.1f Hz, want 0.5", q, note, a, want)
			}
			for i := range l {
				if l[i] != r[i] {
					t.Fatalf("quality %d note %d: a mono sample differs between channels at %d", q, note, i)
				}
			}
		}
	}
}

func TestVoicePitchingUpDoesNotAlias(t *testing.T) {
	// Seven semitones up, the read cuts off at 16 kHz of the sample. A
	// 20 kHz tone becomes 30 kHz, above Nyquist: it must be filtered, not
	// fold back to 18 kHz. The standard kernel's wider transition band
	// rejects from about 1.3 times the cutoff: 22 kHz (33 kHz, which
	// would fold to 15 kHz).
	for q, freq := range map[Quality]float64{QualityHigh: 20000, QualityStandard: 22000} {
		s, _ := NewSample([][]float32{tone(freq, rate, 96000)}, rate, 60)
		v := NewVoice(rate, instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127}))
		v.SetQuality(q)
		l, _ := play(v, 67, 127, 40000)
		amp := spectrum(t, l[4096:])
		worst := 0.0
		for _, a := range amp {
			worst = max(worst, a)
		}
		if db := 20 * math.Log10(worst/0.5); db > -60 {
			t.Errorf("quality %d: alias of %.0f Hz at %.1f dB, want below -60", q, freq, db)
		}
	}
	// Two and four octaves up a 1 kHz tone comes through cleanly.
	s, _ := NewSample([][]float32{tone(1000, rate, 96000)}, rate, 60)
	s.SetLoop(0, 96000)
	for _, q := range []Quality{QualityHigh, QualityStandard} {
		for note, want := range map[uint8]float64{84: 4000, 108: 16000} {
			v := NewVoice(rate, instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127}))
			v.SetQuality(q)
			l, _ := play(v, note, 127, 20000)
			amp := spectrum(t, l[2048:])
			if a := at(amp, want, 16384); math.Abs(a-0.5) > 0.02 {
				t.Errorf("quality %d note %d: %.4f at %.0f Hz, want 0.5", q, note, a, want)
			}
		}
	}
}

func TestVoiceLoopsWhileHeldAndEndsWithoutLoop(t *testing.T) {
	// 100 cycles of 480 Hz at 48 kHz: the loop is a whole number of cycles.
	data := tone(480, rate, 10000)
	s, _ := NewSample([][]float32{data}, rate, 60)
	s.SetLoop(0, 10000)
	v := NewVoice(rate, instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127}))
	l, _ := play(v, 60, 127, 48000)
	if !v.IsActive() {
		t.Fatal("looped voice stopped")
	}
	amp := spectrum(t, l[8192:])
	if a := at(amp, 480, 32768); math.Abs(a-0.5) > 0.01 {
		t.Fatalf("loop plays %.4f at 480 Hz, want 0.5", a)
	}

	s, _ = NewSample([][]float32{data}, rate, 60)
	v = NewVoice(rate, instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127}))
	l, _ = play(v, 60, 127, 48000)
	if v.IsActive() {
		t.Fatal("one-shot voice still active after its sample ended")
	}
	for i := 10000; i < len(l); i++ {
		if l[i] != 0 {
			t.Fatalf("sample %d after the end: %v", i, l[i])
		}
	}
}

func TestMapSelectsZonesByKeyAndVelocity(t *testing.T) {
	soft, _ := NewSample([][]float32{tone(300, rate, 48000)}, rate, 60)
	loud, _ := NewSample([][]float32{tone(300, rate, 48000), tone(300, rate, 48000)}, rate, 60)
	high, _ := NewSample([][]float32{tone(300, rate, 48000)}, rate, 72)
	inst := instrument(t,
		Zone{Sample: soft, LowKey: 0, HighKey: 66, LowVelocity: 0, HighVelocity: 63},
		Zone{Sample: loud, LowKey: 0, HighKey: 66, LowVelocity: 64, HighVelocity: 127, Gain: -6},
		Zone{Sample: high, LowKey: 67, HighKey: 127, HighVelocity: 127},
	)
	m := inst.Load()
	only := func(note, velocity uint8, want *Sample) bool {
		z := m.Match(note, velocity, make([]*Zone, 0, MaxLayers))
		return len(z) == 1 && z[0].Sample == want
	}
	if !only(60, 30, soft) {
		t.Error("soft layer not chosen")
	}
	if !only(60, 100, loud) {
		t.Error("loud layer not chosen")
	}
	if !only(72, 100, high) {
		t.Error("upper zone not chosen")
	}
	// Velocity 127 at -6 dB.
	v := NewVoice(rate, inst)
	l, r := play(v, 60, 127, 20000)
	amp := spectrum(t, r[2048:])
	if a := at(amp, 300, 16384); math.Abs(a-0.5*math.Pow(10, -6.0/20)) > 0.01 {
		t.Errorf("loud zone level %.3f", a)
	}
	for i := range l {
		if l[i] != r[i] {
			t.Fatalf("identical stereo channels differ at %d", i)
		}
	}
	if _, err := NewMap(Zone{}); err == nil {
		t.Error("zone without a sample accepted")
	}
}

func TestVoiceOutsideEveryZoneIsSilent(t *testing.T) {
	s, _ := NewSample([][]float32{tone(300, rate, 4800)}, rate, 60)
	v := NewVoice(rate, instrument(t, Zone{Sample: s, LowKey: 60, HighKey: 60, HighVelocity: 127}))
	v.TriggerNote(61, 100)
	if v.IsActive() {
		t.Fatal("voice active for a note outside every zone")
	}
}

func TestSampleRejectsBadInput(t *testing.T) {
	if _, err := NewSample(nil, rate, 60); err == nil {
		t.Error("no channels accepted")
	}
	if _, err := NewSample([][]float32{{1, 2}, {1}}, rate, 60); err == nil {
		t.Error("unequal channels accepted")
	}
	if _, err := NewSample([][]float32{{1, 2}}, 0, 60); err == nil {
		t.Error("zero sample rate accepted")
	}
}

func TestVoiceDoesNotAllocate(t *testing.T) {
	s, _ := NewSample([][]float32{tone(300, rate, 480000)}, rate, 60)
	v := NewVoice(rate, instrument(t, Zone{Sample: s, HighKey: 127, HighVelocity: 127}))
	l := make([]float32, 512)
	r := make([]float32, 512)
	v.TriggerNote(80, 100)
	allocs := testing.AllocsPerRun(20, func() { v.Render(l, r) })
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func BenchmarkVoiceFifthUp(b *testing.B) {
	s, _ := NewSample([][]float32{tone(300, rate, 480000), tone(300, rate, 480000)}, rate, 60)
	s.SetLoop(0, 480000)
	m, _ := NewMap(Zone{Sample: s, HighKey: 127, HighVelocity: 127})
	var p atomic.Pointer[Map]
	p.Store(m)
	for name, q := range map[string]Quality{"High": QualityHigh, "Standard": QualityStandard} {
		b.Run(name, func(b *testing.B) {
			v := NewVoice(rate, &p)
			v.SetQuality(q)
			l := make([]float32, 512)
			r := make([]float32, 512)
			v.TriggerNote(67, 100)
			for b.Loop() {
				v.Render(l, r)
			}
		})
	}
}
