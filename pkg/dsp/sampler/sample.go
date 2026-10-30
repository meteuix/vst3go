// Package sampler plays recorded samples across the keyboard: Sample holds
// the audio with its root note and loop, Map assigns samples to key and
// velocity ranges with layers, round robin and random choice, LoadSFZ
// reads SFZ instruments, and Voice (a voice.Voice for the framework
// allocator) plays one note with band-limited interpolation, looping and
// an ADSR envelope. A Streamer plays samples from disk, keeping only their
// starts and loops in memory.
package sampler

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/v2/pkg/dsp/resample"
)

// maxLevels bounds the half-rate copies: up to five octaves above the
// root plays alias-free; higher reads step through the last copy.
const maxLevels = 6

// Sample is mono or stereo audio with a root note and an optional loop.
// NewSample prepares half-rate copies for pitching up; a Sample is
// immutable afterwards and can be shared by voices on any thread.
// Streamer.LoadSample returns Samples that play from disk.
type Sample struct {
	sampleRate float64
	rootNote   float64
	loopStart  int
	loopEnd    int // loop when loopEnd > loopStart
	frames     int
	channels   int
	// levels[l][ch] is the sample at sampleRate/2^l; nil when streamed.
	levels [][][]float32
	disk   *diskSample // streamed samples
}

// NewSample builds a Sample from one or two channels of equal length,
// recorded at sampleRate, sounding at rootNote (a MIDI note, fractional
// for fine tuning). It allocates and resamples; call it off the audio
// thread.
func NewSample(channels [][]float32, sampleRate, rootNote float64) (*Sample, error) {
	if len(channels) < 1 || len(channels) > 2 {
		return nil, fmt.Errorf("sampler: %d channels, want 1 or 2", len(channels))
	}
	n := len(channels[0])
	if n == 0 || len(channels) == 2 && len(channels[1]) != n {
		return nil, fmt.Errorf("sampler: channels are empty or differ in length")
	}
	if !(sampleRate > 0) {
		return nil, fmt.Errorf("sampler: sample rate %v", sampleRate)
	}
	s := &Sample{sampleRate: sampleRate, rootNote: rootNote, frames: n, channels: len(channels)}
	level := make([][]float32, len(channels))
	for ch, data := range channels {
		level[ch] = append([]float32(nil), data...)
	}
	s.levels = append(s.levels, level)
	for l := 1; l < maxLevels && len(level[0]) >= 64; l++ {
		next := make([][]float32, len(level))
		for ch, data := range level {
			half, err := resample.Convert(data, 2, 1)
			if err != nil {
				return nil, err
			}
			next[ch] = half
		}
		s.levels = append(s.levels, next)
		level = next
	}
	return s, nil
}

// SetLoop loops playback between frames start (inclusive) and end
// (exclusive) once it reaches end; end <= start turns looping off. It
// must be called before the Sample is shared with voices. A streamed
// sample reads the loop into memory, so it can fail.
func (s *Sample) SetLoop(start, end int) error {
	n := s.Len()
	start = max(0, min(start, n))
	end = max(0, min(end, n))
	if end-start < 2 {
		start, end = 0, 0
	}
	if s.disk != nil && end > start {
		if err := s.disk.keep(start, end); err != nil {
			return err
		}
	}
	s.loopStart, s.loopEnd = start, end
	return nil
}

// Len returns the length in frames.
func (s *Sample) Len() int { return s.frames }

// Channels returns 1 or 2.
func (s *Sample) Channels() int { return s.channels }

// Streamed reports whether the sample plays from disk.
func (s *Sample) Streamed() bool { return s.disk != nil }

// MemoryBytes returns the memory the sample's audio takes: every
// half-rate copy, or for a streamed sample the parts kept in memory.
func (s *Sample) MemoryBytes() int {
	if s.disk != nil {
		return s.disk.memoryBytes()
	}
	n := 0
	for _, level := range s.levels {
		for _, data := range level {
			n += 4 * len(data)
		}
	}
	return n
}

// levelCount returns the number of half-rate copies, the original
// included.
func (s *Sample) levelCount() int {
	if s.disk != nil {
		return len(s.disk.frames)
	}
	return len(s.levels)
}

// SampleRate returns the recording rate.
func (s *Sample) SampleRate() float64 { return s.sampleRate }

// RootNote returns the MIDI note the sample sounds at.
func (s *Sample) RootNote() float64 { return s.rootNote }

// Looped reports whether the sample loops.
func (s *Sample) Looped() bool { return s.loopEnd > s.loopStart }

// LoopMode is how a Zone plays its sample.
type LoopMode int

const (
	// LoopSample loops when the sample has a loop and plays once
	// otherwise.
	LoopSample LoopMode = iota
	// LoopNone plays the sample once, ignoring its loop.
	LoopNone
	// LoopContinuous loops until the voice ends.
	LoopContinuous
	// LoopSustain loops while the note is held, then plays out to the end
	// of the sample.
	LoopSustain
	// OneShot plays the whole sample once, ignoring note-off.
	OneShot
)

// Zone maps a Sample to a range of keys and velocities and says how to
// play it. The zero value of every optional field is its usual setting.
type Zone struct {
	Sample       *Sample
	LowKey       uint8
	HighKey      uint8
	LowVelocity  uint8
	HighVelocity uint8

	// Tune is in cents. KeyTrack is the pitch change per key in cents
	// (0 reads as 100, following the keyboard); FixedPitch plays every
	// key at the sample's root.
	Tune       float64
	KeyTrack   float64
	FixedPitch bool
	// Gain is in dB; Pan balances from -1 (left) to 1 (right).
	Gain float64
	Pan  float64

	// Offset is the first frame played. LoopStart and LoopEnd replace the
	// sample's loop when LoopEnd > LoopStart (frames, end exclusive).
	Offset             int
	Loop               LoopMode
	LoopStart, LoopEnd int

	// Round robin: with SeqLength > 1 the zone plays on one note of its
	// key in every SeqLength, the SeqPosition-th (from 1).
	SeqLength, SeqPosition int
	// Random choice: with HighRand > 0 the zone plays when the note's
	// random number in [0, 1) is in [LowRand, HighRand).
	LowRand, HighRand float64
}

// keyTrack returns the pitch change per key in cents.
func (z *Zone) keyTrack() float64 {
	switch {
	case z.FixedPitch:
		return 0
	case z.KeyTrack == 0:
		return 100
	}
	return z.KeyTrack
}

// loop returns the frames the zone loops between, or 0, 0.
func (z *Zone) loop() (start, end int) {
	s := z.Sample
	start, end = s.loopStart, s.loopEnd
	if z.LoopEnd > z.LoopStart {
		start, end = max(0, z.LoopStart), min(s.Len(), z.LoopEnd)
	}
	switch z.Loop {
	case LoopNone, OneShot:
		return 0, 0
	case LoopContinuous, LoopSustain:
		if end-start < 2 {
			return 0, s.Len()
		}
	}
	if end-start < 2 {
		return 0, 0
	}
	return start, end
}

// Map is an instrument: every zone matching a note plays, so zones can
// layer. It is immutable once built apart from its round-robin counters;
// replace the whole Map to change it.
type Map struct {
	zones []Zone
	seq   [128]atomic.Uint32 // notes played per key
	rng   atomic.Uint64      // xorshift state for random choice
}

// NewMap returns a Map of zones. Zones with a nil Sample, empty ranges or
// a round-robin position outside their sequence are rejected. The loops of
// zones playing streamed samples are read into memory, so voices never
// wait for the disk at a loop's wrap.
func NewMap(zones ...Zone) (*Map, error) {
	for i, z := range zones {
		if z.Sample == nil || z.LowKey > z.HighKey || z.LowVelocity > z.HighVelocity {
			return nil, fmt.Errorf("sampler: zone %d has no sample or an empty range", i)
		}
		if z.SeqLength > 1 && (z.SeqPosition < 1 || z.SeqPosition > z.SeqLength) {
			return nil, fmt.Errorf("sampler: zone %d has round-robin position %d of %d", i, z.SeqPosition, z.SeqLength)
		}
		if start, end := z.loop(); z.Sample.disk != nil && end > start {
			if err := z.Sample.disk.keep(start, end); err != nil {
				return nil, err
			}
		}
	}
	m := &Map{zones: append([]Zone(nil), zones...)}
	m.rng.Store(0x9e3779b97f4a7c15)
	return m, nil
}

// MemoryBytes returns the memory the audio of the Map's samples takes,
// counting shared samples (and streamed samples sharing a cache file)
// once.
func (m *Map) MemoryBytes() int {
	seen := map[any]bool{}
	n := 0
	for i := range m.zones {
		s := m.zones[i].Sample
		var key any = s
		if s.disk != nil {
			key = s.disk
		}
		if !seen[key] {
			seen[key] = true
			n += s.MemoryBytes()
		}
	}
	return n
}

// Zones returns the number of zones.
func (m *Map) Zones() int { return len(m.zones) }

// Match appends the zones a note-on plays to dst, up to its capacity, and
// returns it. It advances the key's round robin and draws the note's
// random number, so call it once per note-on (from one thread at a time).
// It does not allocate.
func (m *Map) Match(note, velocity uint8, dst []*Zone) []*Zone {
	if m == nil {
		return dst
	}
	count := int(m.seq[note&127].Add(1) - 1)
	x := m.rng.Load()
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	m.rng.Store(x)
	random := float64(x>>11) / (1 << 53)
	for i := range m.zones {
		if len(dst) == cap(dst) {
			break
		}
		z := &m.zones[i]
		if note < z.LowKey || note > z.HighKey || velocity < z.LowVelocity || velocity > z.HighVelocity {
			continue
		}
		if z.SeqLength > 1 && count%z.SeqLength != z.SeqPosition-1 {
			continue
		}
		if z.HighRand > 0 && (random < z.LowRand || random >= z.HighRand) {
			continue
		}
		dst = append(dst, z)
	}
	return dst
}

// dbToGain converts dB to a linear factor.
func dbToGain(db float64) float64 { return math.Pow(10, db/20) }
