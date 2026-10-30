// Package pitch shifts, stretches and detects pitch: Shifter is a
// low-latency time-domain pitch shifter, SpectralShifter a phase-vocoder
// pitch shifter for cleaner results on tonal material, Stretch an offline
// phase-vocoder time stretch, and Detector a YIN pitch detector for tuners
// and pitch correction.
package pitch

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fastmath"
	"github.com/meteuix/vst3go/v2/pkg/dsp/interpolation"
)

// Shifter shifts pitch with two delay-line taps whose delays sweep at the
// rate that resamples the signal by the pitch ratio; each tap fades out
// as it wraps around while the other, half a window away, takes over. A
// window of 30 to 60 ms suits most material: shorter windows flutter on
// low notes, longer ones smear transients. The signal is delayed by about
// half a window. Process does not allocate.
type Shifter struct {
	sampleRate float64
	buf        []float32
	mask       int
	write      int
	ratio      float64
	window     float64 // in samples
	phase      float64 // position of tap A in [0, 1)
}

// NewShifter returns a Shifter for windows up to maxWindow seconds, set to
// a 50 ms window (or maxWindow if shorter) and no shift.
func NewShifter(sampleRate, maxWindow float64) *Shifter {
	n := 1
	for n < int(maxWindow*sampleRate)+8 {
		n <<= 1
	}
	s := &Shifter{sampleRate: sampleRate, buf: make([]float32, n), mask: n - 1, ratio: 1}
	s.SetWindow(min(0.05, maxWindow))
	return s
}

// SetRatio sets the pitch ratio (2 is an octave up), clamped to 0.25..4.
func (s *Shifter) SetRatio(ratio float64) { s.ratio = max(0.25, min(4, ratio)) }

// SetSemitones sets the shift in semitones.
func (s *Shifter) SetSemitones(semitones float64) { s.SetRatio(math.Exp2(semitones / 12)) }

// Ratio returns the pitch ratio.
func (s *Shifter) Ratio() float64 { return s.ratio }

// SetWindow sets the window length in seconds, up to the constructor's
// maximum.
func (s *Shifter) SetWindow(seconds float64) {
	s.window = max(4, min(float64(len(s.buf)-8), seconds*s.sampleRate))
}

// Process shifts in into out (same length); in and out may be the same
// slice.
func (s *Shifter) Process(in, out []float32) {
	step := (1 - s.ratio) / s.window
	for i, x := range in {
		s.buf[s.write] = x
		a := s.phase
		b := a + 0.5
		if b >= 1 {
			b--
		}
		// sin² fades: the two gains always sum to one.
		ga := fastmath.Sin2Pi(0.5 * a)
		gb := fastmath.Sin2Pi(0.5 * b)
		out[i] = float32(ga*ga)*s.tap(a) + float32(gb*gb)*s.tap(b)
		s.phase += step
		s.phase -= math.Floor(s.phase)
		s.write = (s.write + 1) & s.mask
	}
}

// tap reads the delay line at phase p of the window, one sample past the
// newest input so the interpolator has a sample on each side.
func (s *Shifter) tap(p float64) float32 {
	d := 2 + p*s.window
	pos := float64(s.write) - d
	i := int(math.Floor(pos))
	f := float32(pos - float64(i))
	m := s.mask
	return interpolation.Hermite(s.buf[(i-1)&m], s.buf[i&m], s.buf[(i+1)&m], s.buf[(i+2)&m], f)
}

// Reset clears the delay line.
func (s *Shifter) Reset() {
	clear(s.buf)
	s.write, s.phase = 0, 0
}
