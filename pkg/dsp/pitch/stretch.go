package pitch

import (
	"fmt"
	"math"
	"math/cmplx"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fft"
)

// Stretch changes the length of a signal by factor (2 is twice as long)
// without changing its pitch, with a phase vocoder of the given frame size
// (a power of two; 2048 suits 44.1 or 48 kHz). It returns
// round(len(in)·factor) samples aligned with the input. It allocates and is
// meant for offline use (Stretcher streams); combined with resample.Convert
// it also shifts pitch without changing length.
func Stretch(in []float32, factor float64, size int) ([]float32, error) {
	if !(factor >= minStretch && factor <= maxStretch) {
		return nil, fmt.Errorf("pitch: stretch factor %v is outside %v..%v", factor, minStretch, maxStretch)
	}
	s, err := NewStretcher(size, factor)
	if err != nil {
		return nil, err
	}
	s.SetFactor(factor)
	// Lead with silence so the first samples sit in fully overlapped
	// frames: input sample i is fed as sample pad+i, which comes out at
	// (pad+i)·factor = offset+i·factor.
	pad := int(math.Ceil(float64(size) / min(factor, 1)))
	offset := int(math.Round(float64(pad) * factor))
	outLen := int(math.Round(float64(len(in)) * factor))
	result := make([]float32, offset+outLen)
	zeros := make([]float32, size)
	produced := 0
	for produced < len(result) {
		switch {
		case pad > 0:
			pad -= s.Write(zeros[:min(pad, size)])
		case len(in) > 0:
			in = in[s.Write(in):]
		default:
			s.Write(zeros)
		}
		produced += s.Read(result[produced:])
	}
	return result[offset:], nil
}

const (
	minStretch = 0.1
	maxStretch = 10.0
)

// Stretcher is a streaming phase vocoder time stretcher: Write feeds it
// input, Read takes output, factor output samples per input sample (2 is
// twice as long), at the same pitch. The factor can change at any time,
// for varispeed playback at constant pitch. Output lags input by about one
// frame; it starts with a frame-long fade-in from silence.
//
// Write, Read and SetFactor do not allocate.
type Stretcher struct {
	f                 *fft.Real
	size, synthHop    int
	factor, minFactor float64
	window            []float64
	windowSum         float64

	// Input FIFO: absolute sample positions, in[pos % len(in)].
	in          []float32
	inStart     int     // oldest sample still needed
	inEnd       int     // one past the newest sample written
	readPos     float64 // where the next frame starts
	lastStart   int     // where the previous frame started
	framesSoFar int

	// Overlap-add accumulator and FIFO of finished output.
	ola              []float64
	out              []float32
	outStart, outLen int

	frame, mag, phase, lastPhase, sumPhase []float64
	spectrum                               []complex128
	peaks                                  []int
}

// NewStretcher returns a stretcher with frames of size samples (a power of
// two) for factors from minFactor (at least 0.1) to 10. Smaller minimum
// factors need shorter hops, so they cost more per output sample.
func NewStretcher(size int, minFactor float64) (*Stretcher, error) {
	if !(minFactor >= minStretch && minFactor <= maxStretch) {
		return nil, fmt.Errorf("pitch: stretch factor %v is outside %v..%v", minFactor, minStretch, maxStretch)
	}
	f, err := fft.NewReal(size)
	if err != nil {
		return nil, err
	}
	// At least four-fold overlap, and analysis hops of at most half a
	// frame so the phase advance of a peak is unambiguous.
	synthHop := size / 4
	for synthHop > 1 && float64(synthHop)/minFactor > float64(size/2) {
		synthHop /= 2
	}
	bins := size/2 + 1
	s := &Stretcher{
		f:         f,
		size:      size,
		synthHop:  synthHop,
		factor:    1,
		minFactor: minFactor,
		window:    make([]float64, size),
		// Hann analysis and synthesis windows: their products at this
		// hop sum to 3/8 of the overlap.
		windowSum: 0.375 * float64(size) / float64(synthHop),
		in:        make([]float32, 2*size),
		ola:       make([]float64, size),
		out:       make([]float32, size),
		frame:     make([]float64, size),
		mag:       make([]float64, bins),
		phase:     make([]float64, bins),
		lastPhase: make([]float64, bins),
		sumPhase:  make([]float64, bins),
		spectrum:  make([]complex128, bins),
		peaks:     make([]int, 0, bins),
	}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(size))
	}
	return s, nil
}

// SetFactor sets the stretch factor, clamped to the range given to
// NewStretcher.
func (s *Stretcher) SetFactor(factor float64) {
	s.factor = max(s.minFactor, min(maxStretch, factor))
}

// Factor returns the stretch factor.
func (s *Stretcher) Factor() float64 { return s.factor }

// Write queues input and returns how many samples it took: fewer than
// len(in) when the queue is full, until Read consumes some.
func (s *Stretcher) Write(in []float32) int {
	n := min(len(in), len(s.in)-(s.inEnd-s.inStart))
	for i, x := range in[:n] {
		s.in[(s.inEnd+i)%len(s.in)] = x
	}
	s.inEnd += n
	return n
}

// Read fills out with stretched audio and returns how many samples it
// wrote: fewer than len(out) when it needs more input.
func (s *Stretcher) Read(out []float32) int {
	written := 0
	for written < len(out) {
		if s.outLen == 0 && !s.synthesize() {
			break
		}
		n := min(len(out)-written, s.outLen, len(s.out)-s.outStart)
		copy(out[written:], s.out[s.outStart:s.outStart+n])
		s.outStart = (s.outStart + n) % len(s.out)
		s.outLen -= n
		written += n
	}
	return written
}

// Reset clears the input, output and phase state.
func (s *Stretcher) Reset() {
	s.inStart, s.inEnd, s.readPos, s.lastStart, s.framesSoFar = 0, 0, 0, 0, 0
	s.outStart, s.outLen = 0, 0
	clear(s.ola)
}

// synthesize runs one frame and queues synthHop finished samples, or
// returns false when a whole frame of input is not there yet.
func (s *Stretcher) synthesize() bool {
	start := int(math.Round(s.readPos))
	if start+s.size > s.inEnd {
		return false
	}
	size, bins := s.size, len(s.mag)
	for i := range s.frame {
		s.frame[i] = float64(s.in[(start+i)%len(s.in)]) * s.window[i]
	}
	s.f.Forward(s.frame, s.spectrum)
	for k, x := range s.spectrum {
		s.mag[k], s.phase[k] = cmplx.Abs(x), cmplx.Phase(x)
	}
	// Identity phase locking (Laroche and Dolson): advance the phase of
	// each magnitude peak by its true frequency, and keep every other bin
	// at its analysed phase offset from the peak whose region it lies in.
	// Advancing every bin on its own would lose the phase relations
	// between bins that shape each partial.
	hop := float64(start - s.lastStart)
	s.peaks = s.peaks[:0]
	for k := range bins {
		mag := s.mag
		if (k == 0 || mag[k] > mag[k-1]) && (k == bins-1 || mag[k] >= mag[k+1]) {
			s.peaks = append(s.peaks, k)
			if s.framesSoFar == 0 || hop == 0 {
				// A repeated frame (an analysis hop under half a sample)
				// has no measurable phase advance; use the bin frequency.
				omega := 2 * math.Pi * float64(k) / float64(size)
				if s.framesSoFar == 0 {
					s.sumPhase[k] = s.phase[k]
				} else {
					s.sumPhase[k] += omega * float64(s.synthHop)
				}
				continue
			}
			omega := 2 * math.Pi * float64(k) / float64(size)
			delta := s.phase[k] - s.lastPhase[k] - omega*hop
			delta -= 2 * math.Pi * math.Round(delta/(2*math.Pi))
			s.sumPhase[k] += (omega + delta/hop) * float64(s.synthHop)
		}
	}
	lockToPeaks(s.peaks, s.mag, s.phase, s.sumPhase)
	for k := range s.spectrum {
		s.lastPhase[k] = s.phase[k]
		s.spectrum[k] = cmplx.Rect(s.mag[k], s.sumPhase[k])
	}
	s.f.Inverse(s.spectrum, s.frame)
	for i, x := range s.frame {
		s.ola[i] += x * s.window[i] / s.windowSum
	}

	// The first synthHop samples are complete; queue them and shift.
	at := (s.outStart + s.outLen) % len(s.out)
	for i, x := range s.ola[:s.synthHop] {
		s.out[(at+i)%len(s.out)] = float32(x)
	}
	s.outLen += s.synthHop
	copy(s.ola, s.ola[s.synthHop:])
	clear(s.ola[size-s.synthHop:])

	s.lastStart = start
	s.framesSoFar++
	s.readPos += float64(s.synthHop) / s.factor
	s.inStart = int(math.Round(s.readPos))
	return true
}

// lockToPeaks sets the output phase of every bin that is not a peak to
// the output phase of the peak owning it plus their analysed phase
// difference.
func lockToPeaks(peaks []int, mag, phase, out []float64) {
	start := 0
	for i, p := range peaks {
		end := regionEnd(peaks, i, mag)
		for k := start; k < end; k++ {
			if k != p {
				out[k] = out[p] + phase[k] - phase[p]
			}
		}
		start = end
	}
}

// regionEnd returns the end (exclusive) of the bins owned by peaks[i]: up
// to and including the lowest magnitude before the next peak, or every
// remaining bin for the last peak.
func regionEnd(peaks []int, i int, mag []float64) int {
	if i+1 == len(peaks) {
		return len(mag)
	}
	p, end := peaks[i], peaks[i]+1
	for k := p + 1; k < peaks[i+1]; k++ {
		if end == p+1 || mag[k] < mag[end-1] {
			end = k + 1
		}
	}
	return end
}
