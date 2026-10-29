package pitch

import (
	"fmt"
	"math"
	"math/cmplx"

	"github.com/th-release/vst3go/pkg/dsp/fft"
)

// Stretch changes the length of a signal by factor (2 is twice as long)
// without changing its pitch, with a phase vocoder of the given frame size
// (a power of two; 2048 suits 44.1 or 48 kHz). It returns
// round(len(in)·factor) samples. It allocates and is meant for offline
// use; combined with resample.Convert it also shifts pitch without
// changing length.
func Stretch(in []float32, factor float64, size int) ([]float32, error) {
	if !(factor >= 0.1 && factor <= 10) {
		return nil, fmt.Errorf("pitch: stretch factor %v is outside 0.1..10", factor)
	}
	f, err := fft.NewReal(size)
	if err != nil {
		return nil, err
	}
	// At least four-fold overlap, and analysis hops of at most half a
	// frame so the phase advance of a peak is unambiguous.
	synthHop := size / 4
	for synthHop > 1 && float64(synthHop)/factor > float64(size/2) {
		synthHop /= 2
	}
	analysisHop := float64(synthHop) / factor
	outLen := int(math.Round(float64(len(in)) * factor))
	bins := size/2 + 1

	window := make([]float64, size)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(size))
	}
	// Hann analysis and synthesis windows: their products at this hop
	// sum to 3/8 of the overlap.
	windowSum := 0.375 * float64(size) / float64(synthHop)

	// Pad so the first and last samples sit in fully overlapped frames:
	// input sample i is padded sample pad+i, which lands at output
	// (pad+i)·factor = offset+i·factor.
	pad := int(math.Ceil(float64(size) / math.Min(factor, 1)))
	offset := int(math.Round(float64(pad) * factor))
	padded := make([]float64, pad+len(in)+size)
	for i, x := range in {
		padded[pad+i] = float64(x)
	}
	out := make([]float64, offset+outLen+2*size)
	frame := make([]float64, size)
	spectrum := make([]complex128, bins)
	lastPhase := make([]float64, bins)
	sumPhase := make([]float64, bins)
	mag := make([]float64, bins)
	phase := make([]float64, bins)
	peaks := make([]int, 0, bins)

	for m := 0; ; m++ {
		outPos := m * synthHop
		if outPos > offset+outLen {
			break
		}
		inPos := float64(m) * analysisHop
		start := int(math.Round(inPos))
		for i := range frame {
			if j := start + i; j < len(padded) {
				frame[i] = padded[j] * window[i]
			} else {
				frame[i] = 0
			}
		}
		f.Forward(frame, spectrum)
		for k, x := range spectrum {
			mag[k], phase[k] = cmplx.Abs(x), cmplx.Phase(x)
		}
		// Identity phase locking (Laroche and Dolson): advance the phase
		// of each magnitude peak by its true frequency, and keep every
		// other bin at its analysed phase offset from the peak whose
		// region it lies in. Advancing every bin on its own would lose
		// the phase relations between bins that shape each partial.
		peaks = peaks[:0]
		for k := range spectrum {
			if (k == 0 || mag[k] > mag[k-1]) && (k == bins-1 || mag[k] >= mag[k+1]) {
				peaks = append(peaks, k)
				omega := 2 * math.Pi * float64(k) / float64(size)
				if m == 0 {
					sumPhase[k] = phase[k]
					continue
				}
				delta := phase[k] - lastPhase[k] - omega*analysisHop
				delta -= 2 * math.Pi * math.Round(delta/(2*math.Pi))
				sumPhase[k] += (omega + delta/analysisHop) * float64(synthHop)
			}
		}
		lockToPeaks(peaks, mag, phase, sumPhase)
		for k := range spectrum {
			lastPhase[k] = phase[k]
			spectrum[k] = cmplx.Rect(mag[k], sumPhase[k])
		}
		f.Inverse(spectrum, frame)
		for i, x := range frame {
			out[outPos+i] += x * window[i] / windowSum
		}
	}

	result := make([]float32, outLen)
	for i := range result {
		result[i] = float32(out[offset+i])
	}
	return result, nil
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
