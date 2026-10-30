package pitch

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/pkg/dsp/fft"
)

// Detector estimates the fundamental frequency of a monophonic signal with
// the YIN algorithm (de Cheveigné and Kawahara, 2002): the cumulative mean
// normalized difference function over a window twice the longest period,
// computed with an FFT, its first dip below a threshold, refined by
// parabolic interpolation. It analyses every hop samples. Process does
// not allocate.
type Detector struct {
	sampleRate     float64
	minLag, maxLag int
	window         int // integration window, maxLag samples
	hop, fill      int
	threshold      float64

	ring    []float64 // last window+maxLag samples
	write   int
	history []float64 // ring unrolled, oldest first
	fft     *fft.Real
	a, b    []float64
	specA   []complex128
	specB   []complex128
	diff    []float64 // d'(τ)

	frequency, clarity float64
}

// NewDetector returns a detector for fundamentals between minFreq and
// maxFreq Hz, analysing every hop samples.
func NewDetector(sampleRate, minFreq, maxFreq float64, hop int) (*Detector, error) {
	if !(minFreq > 0 && maxFreq > minFreq && maxFreq < sampleRate/4) || hop < 1 {
		return nil, fmt.Errorf("pitch: detector range %v..%v Hz or hop %d is invalid at %v Hz", minFreq, maxFreq, hop, sampleRate)
	}
	d := &Detector{
		sampleRate: sampleRate,
		minLag:     max(2, int(sampleRate/maxFreq)),
		maxLag:     int(math.Ceil(sampleRate/minFreq)) + 1,
		hop:        hop,
		threshold:  0.15,
	}
	d.window = d.maxLag
	n := 1
	for n < d.window+d.maxLag+1 {
		n <<= 1
	}
	f, err := fft.NewReal(n)
	if err != nil {
		return nil, err
	}
	d.fft = f
	d.ring = make([]float64, d.window+d.maxLag)
	d.history = make([]float64, d.window+d.maxLag)
	d.a = make([]float64, n)
	d.b = make([]float64, n)
	d.specA = make([]complex128, f.Bins())
	d.specB = make([]complex128, f.Bins())
	d.diff = make([]float64, d.maxLag+1)
	return d, nil
}

// Latency returns how many samples the analysis window spans; a pitch is
// reported for the window ending at the latest hop.
func (d *Detector) Latency() int { return len(d.history) }

// SetThreshold sets the YIN threshold (0.05 strict to 0.3 permissive).
func (d *Detector) SetThreshold(t float64) { d.threshold = max(0.01, min(0.5, t)) }

// Process adds samples and updates the estimate every hop samples.
func (d *Detector) Process(in []float32) {
	for _, x := range in {
		d.ring[d.write] = float64(x)
		if d.write++; d.write == len(d.ring) {
			d.write = 0
		}
		d.fill++
		if d.fill >= d.hop {
			d.fill = 0
			d.analyse()
		}
	}
}

// Pitch returns the latest estimate in Hz and its clarity (1 - the
// normalized difference at the chosen period, 0..1). The frequency is 0
// when no period was found.
func (d *Detector) Pitch() (frequency, clarity float64) { return d.frequency, d.clarity }

func (d *Detector) analyse() {
	w, h := d.window, d.history
	n := copy(h, d.ring[d.write:])
	copy(h[n:], d.ring[:d.write])
	// r(τ) = Σ_{j<w} h[j]·h[j+τ] by FFT cross-correlation.
	clear(d.a)
	copy(d.a, h[:w])
	clear(d.b)
	copy(d.b, h)
	d.fft.Forward(d.a, d.specA)
	d.fft.Forward(d.b, d.specB)
	for k := range d.specA {
		a := d.specA[k]
		d.specA[k] = complex(real(a), -imag(a)) * d.specB[k]
	}
	d.fft.Inverse(d.specA, d.a)

	// d(τ) = Σ h[j]² + Σ h[j+τ]² - 2r(τ), with running energies.
	e0 := 0.0
	for _, x := range h[:w] {
		e0 += x * x
	}
	eTau := e0
	running := 0.0
	d.diff[0] = 1
	for tau := 1; tau <= d.maxLag; tau++ {
		eTau += h[tau+w-1]*h[tau+w-1] - h[tau-1]*h[tau-1]
		v := max(0, e0+eTau-2*d.a[tau])
		running += v
		if running > 0 {
			d.diff[tau] = v * float64(tau) / running
		} else {
			d.diff[tau] = 1
		}
	}

	best := -1
	for tau := d.minLag; tau < d.maxLag; tau++ {
		if d.diff[tau] < d.threshold {
			for tau+1 < d.maxLag && d.diff[tau+1] < d.diff[tau] {
				tau++
			}
			best = tau
			break
		}
	}
	if best < 0 || e0 < 1e-10 {
		d.frequency, d.clarity = 0, 0
		return
	}
	period := float64(best)
	if best > 1 {
		y0, y1, y2 := d.diff[best-1], d.diff[best], d.diff[best+1]
		if den := y0 - 2*y1 + y2; den > 0 {
			period += 0.5 * (y0 - y2) / den
		}
	}
	d.frequency = d.sampleRate / period
	d.clarity = max(0, 1-d.diff[best])
}

// Reset clears the history and the estimate.
func (d *Detector) Reset() {
	clear(d.ring)
	d.write, d.fill = 0, 0
	d.frequency, d.clarity = 0, 0
}
