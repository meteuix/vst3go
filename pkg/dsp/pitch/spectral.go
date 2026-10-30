package pitch

import (
	"math"
	"math/cmplx"

	"github.com/meteuix/vst3go/v2/pkg/dsp/spectral"
)

// SpectralShifter shifts pitch with a phase vocoder (Laroche and Dolson's
// peak shifting): each STFT magnitude peak's true frequency is estimated
// from its phase advance, the bins around the peak are moved together to
// ratio times that frequency, and their phases are advanced at the new
// frequency while keeping their offsets from the peak. It keeps tonal
// material cleaner than Shifter at the cost of Latency samples (the frame
// size) and some smearing of transients. Process does not allocate.
type SpectralShifter struct {
	stft      *spectral.STFT
	ratio     float64
	overlap   float64
	lastPhase []float64
	outPhase  []float64
	mag       []float64
	phase     []float64
	synMag    []float64
	synPhase  []float64
	written   []bool
	peaks     []int
}

// NewSpectralShifter returns a phase-vocoder shifter with the given frame
// size (a power of two, 2048 at 44.1 or 48 kHz) and four-fold overlap.
func NewSpectralShifter(size int) (*SpectralShifter, error) {
	bins := size/2 + 1
	s := &SpectralShifter{
		ratio:     1,
		lastPhase: make([]float64, bins),
		outPhase:  make([]float64, bins),
		mag:       make([]float64, bins),
		phase:     make([]float64, bins),
		synMag:    make([]float64, bins),
		synPhase:  make([]float64, bins),
		written:   make([]bool, bins),
		peaks:     make([]int, 0, bins),
	}
	stft, err := spectral.NewSTFT(size, size/4, s.frame)
	if err != nil {
		return nil, err
	}
	s.stft = stft
	s.overlap = 4
	return s, nil
}

// SetRatio sets the pitch ratio (2 is an octave up), clamped to 0.25..4.
func (s *SpectralShifter) SetRatio(ratio float64) { s.ratio = max(0.25, min(4, ratio)) }

// SetSemitones sets the shift in semitones.
func (s *SpectralShifter) SetSemitones(semitones float64) { s.SetRatio(math.Exp2(semitones / 12)) }

// Latency returns the delay in samples.
func (s *SpectralShifter) Latency() int { return s.stft.Latency() }

// Process shifts in into out (same length); in and out may be the same
// slice.
func (s *SpectralShifter) Process(in, out []float32) { s.stft.Process(in, out) }

func (s *SpectralShifter) frame(spectrum []complex128) {
	bins := len(spectrum)
	for k, x := range spectrum {
		s.mag[k], s.phase[k] = cmplx.Abs(x), cmplx.Phase(x)
	}
	if s.ratio == 1 {
		// No shift: pass the frame through, tracking phases so a later
		// shift continues from them.
		copy(s.lastPhase, s.phase)
		copy(s.outPhase, s.phase)
		return
	}
	// Phase advance of bin k per hop is k·2π/overlap.
	advance := 2 * math.Pi / s.overlap
	s.peaks = s.peaks[:0]
	for k := range bins {
		if (k == 0 || s.mag[k] > s.mag[k-1]) && (k == bins-1 || s.mag[k] >= s.mag[k+1]) {
			s.peaks = append(s.peaks, k)
		}
	}
	clear(s.synMag)
	clear(s.written)
	start := 0
	for i, p := range s.peaks {
		end := regionEnd(s.peaks, i, s.mag)
		delta := s.phase[p] - s.lastPhase[p] - float64(p)*advance
		delta -= 2 * math.Pi * math.Round(delta/(2*math.Pi))
		target := (float64(p) + delta/advance) * s.ratio // in bins
		shift := int(math.Round(target)) - p
		if j := p + shift; j >= 0 && j < bins {
			peakPhase := s.outPhase[j] + target*advance
			for k := start; k < end; k++ {
				if t := k + shift; t >= 0 && t < bins {
					s.synMag[t] += s.mag[k]
					s.synPhase[t] = peakPhase + s.phase[k] - s.phase[p]
					s.written[t] = true
				}
			}
		}
		start = end
	}
	for k := range spectrum {
		s.lastPhase[k] = s.phase[k]
		if !s.written[k] {
			s.synPhase[k] = s.outPhase[k] + float64(k)*advance
		}
		s.outPhase[k] = math.Remainder(s.synPhase[k], 2*math.Pi)
		spectrum[k] = cmplx.Rect(s.synMag[k], s.outPhase[k])
	}
}

// Reset clears the stream and the phase state.
func (s *SpectralShifter) Reset() {
	s.stft.Reset()
	clear(s.lastPhase)
	clear(s.outPhase)
}
