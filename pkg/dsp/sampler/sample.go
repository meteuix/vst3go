// Package sampler plays recorded samples across the keyboard: Sample holds
// the audio with its root note and loop, Map assigns samples to key and
// velocity ranges, and Voice (a voice.Voice for the framework allocator)
// plays one note with band-limited interpolation, looping and an ADSR
// envelope.
package sampler

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/pkg/dsp/resample"
)

// maxLevels bounds the half-rate copies: up to five octaves above the
// root plays alias-free; higher reads step through the last copy.
const maxLevels = 6

// Sample is mono or stereo audio with a root note and an optional loop.
// NewSample prepares half-rate copies for pitching up; a Sample is
// immutable afterwards and can be shared by voices on any thread.
type Sample struct {
	sampleRate float64
	rootNote   float64
	loopStart  int
	loopEnd    int // loop when loopEnd > loopStart
	// levels[l][ch] is the sample at sampleRate/2^l.
	levels [][][]float32
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
	s := &Sample{sampleRate: sampleRate, rootNote: rootNote}
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
// must be called before the Sample is shared with voices.
func (s *Sample) SetLoop(start, end int) {
	n := s.Len()
	start = max(0, min(start, n))
	end = max(0, min(end, n))
	if end-start < 2 {
		start, end = 0, 0
	}
	s.loopStart, s.loopEnd = start, end
}

// Len returns the length in frames.
func (s *Sample) Len() int { return len(s.levels[0][0]) }

// Channels returns 1 or 2.
func (s *Sample) Channels() int { return len(s.levels[0]) }

// SampleRate returns the recording rate.
func (s *Sample) SampleRate() float64 { return s.sampleRate }

// RootNote returns the MIDI note the sample sounds at.
func (s *Sample) RootNote() float64 { return s.rootNote }

// Looped reports whether the sample loops.
func (s *Sample) Looped() bool { return s.loopEnd > s.loopStart }

// Zone maps a Sample to a range of keys and velocities, with tuning in
// cents and gain in dB.
type Zone struct {
	Sample       *Sample
	LowKey       uint8
	HighKey      uint8
	LowVelocity  uint8
	HighVelocity uint8
	Tune         float64
	Gain         float64
}

// Map is an instrument: zones searched in order, the first match plays.
// It is immutable once built; replace the whole Map to change it.
type Map struct {
	zones []Zone
}

// NewMap returns a Map of zones. Zones with a nil Sample or empty ranges
// are rejected.
func NewMap(zones ...Zone) (*Map, error) {
	for i, z := range zones {
		if z.Sample == nil || z.LowKey > z.HighKey || z.LowVelocity > z.HighVelocity {
			return nil, fmt.Errorf("sampler: zone %d has no sample or an empty range", i)
		}
	}
	return &Map{zones: append([]Zone(nil), zones...)}, nil
}

// Find returns the zone for a note and velocity, or nil.
func (m *Map) Find(note, velocity uint8) *Zone {
	if m == nil {
		return nil
	}
	for i := range m.zones {
		z := &m.zones[i]
		if note >= z.LowKey && note <= z.HighKey && velocity >= z.LowVelocity && velocity <= z.HighVelocity {
			return z
		}
	}
	return nil
}

// dbToGain converts dB to a linear factor.
func dbToGain(db float64) float64 { return math.Pow(10, db/20) }
