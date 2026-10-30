// Package spectral provides streaming short-time Fourier transform (STFT)
// processing: overlapping windowed frames go through a real FFT, a
// per-frame callback edits the spectrum, and the inverse frames are
// overlap-added back into a signal. It is the base for spectral effects
// such as denoisers, spectral gates, linear-phase filtering and phase
// vocoders.
package spectral

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/v2/pkg/dsp/fft"
)

// FrameFunc edits one frame's spectrum in place. spectrum has size/2+1
// bins; bin k is frequency k·sampleRate/size.
type FrameFunc func(spectrum []complex128)

// STFT runs a FrameFunc over a mono stream. Analysis and synthesis use a
// periodic √Hann window, normalized so that a FrameFunc that leaves the
// spectrum alone reproduces the input exactly, delayed by Latency.
//
// Process does not allocate; the FrameFunc runs on the calling (audio)
// thread and must not allocate either.
type STFT struct {
	size, hop int
	window    []float64
	fft       *fft.Real
	frame     func([]complex128)

	input    []float64 // last size input samples, oldest first
	output   []float64 // overlap-add accumulator, size samples
	ready    []float64 // hop finished output samples
	fill     int       // input samples since the last frame
	time     []float64
	spectrum []complex128
}

// NewSTFT returns an STFT with the given frame size (a power of two of at
// least 4) and hop, which must divide size and be at most size/2.
func NewSTFT(size, hop int, frame FrameFunc) (*STFT, error) {
	if hop < 1 || hop > size/2 || size%hop != 0 {
		return nil, fmt.Errorf("spectral: hop %d must divide size %d and be at most half of it", hop, size)
	}
	if size < 4 {
		return nil, fmt.Errorf("spectral: size %d is below 4", size)
	}
	f, err := fft.NewReal(size)
	if err != nil {
		return nil, err
	}
	s := &STFT{
		size: size, hop: hop, fft: f, frame: frame,
		window:   make([]float64, size),
		input:    make([]float64, size),
		output:   make([]float64, size),
		ready:    make([]float64, hop),
		time:     make([]float64, size),
		spectrum: make([]complex128, f.Bins()),
	}
	// √Hann for analysis and synthesis: the product is Hann, whose shifted
	// copies at this hop sum to size/(2·hop).
	gain := math.Sqrt(2 * float64(hop) / float64(size))
	for i := range s.window {
		s.window[i] = math.Sqrt(0.5-0.5*math.Cos(2*math.Pi*float64(i)/float64(size))) * gain
	}
	return s, nil
}

// Size returns the frame size.
func (s *STFT) Size() int { return s.size }

// Hop returns the hop between frames.
func (s *STFT) Hop() int { return s.hop }

// Latency returns the delay in samples between input and output.
func (s *STFT) Latency() int { return s.size }

// Process runs in through the STFT into out (same length); in and out may
// be the same slice.
func (s *STFT) Process(in, out []float32) {
	for i, x := range in {
		y := s.ready[s.fill]
		s.input[s.size-s.hop+s.fill] = float64(x)
		s.fill++
		if s.fill == s.hop {
			s.runFrame()
		}
		out[i] = float32(y)
	}
}

func (s *STFT) runFrame() {
	for i, x := range s.input {
		s.time[i] = x * s.window[i]
	}
	s.fft.Forward(s.time, s.spectrum)
	if s.frame != nil {
		s.frame(s.spectrum)
	}
	s.fft.Inverse(s.spectrum, s.time)
	for i, x := range s.time {
		s.output[i] += x * s.window[i]
	}
	copy(s.ready, s.output[:s.hop])
	copy(s.output, s.output[s.hop:])
	clear(s.output[s.size-s.hop:])
	copy(s.input, s.input[s.hop:])
	s.fill = 0
}

// Reset clears the stream.
func (s *STFT) Reset() {
	clear(s.input)
	clear(s.output)
	clear(s.ready)
	s.fill = 0
}
