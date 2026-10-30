package analysis

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fft"
)

// FFT is a windowed spectrum transform of a fixed power-of-two size on
// the transforms of pkg/dsp/fft.
type FFT struct {
	size       int
	window     WindowFunc
	windowData []float64
	real       *fft.Real
	complex    *fft.Complex // made by the first ForwardComplex or Inverse
	frame      []float64
	spectrum   []complex128
	work       []complex128 // for complex
	magnitude  []float64
	phase      []float64
}

// WindowFunc represents a window function type
type WindowFunc int

const (
	RectangularWindow WindowFunc = iota
	HannWindow
	HammingWindow
	BlackmanWindow
	BlackmanHarrisWindow
	KaiserWindow
	FlatTopWindow
)

// NewFFT creates a new FFT processor with the specified size, a power of
// two of at least 2, and window function. It panics on other sizes.
func NewFFT(size int, window WindowFunc) *FFT {
	r, err := fft.NewReal(size)
	if err != nil {
		panic(err)
	}
	f := &FFT{
		size:       size,
		window:     window,
		windowData: make([]float64, size),
		real:       r,
		frame:      make([]float64, size),
		spectrum:   make([]complex128, r.Bins()),
		magnitude:  make([]float64, size/2+1),
		phase:      make([]float64, size/2+1),
	}
	f.calculateWindow()
	return f
}

// calculateWindow pre-calculates the window coefficients
func (f *FFT) calculateWindow() {
	n := float64(f.size)

	switch f.window {
	case RectangularWindow:
		for i := 0; i < f.size; i++ {
			f.windowData[i] = 1.0
		}

	case HannWindow:
		for i := 0; i < f.size; i++ {
			f.windowData[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/(n-1.0)))
		}

	case HammingWindow:
		for i := 0; i < f.size; i++ {
			f.windowData[i] = 0.54 - 0.46*math.Cos(2.0*math.Pi*float64(i)/(n-1.0))
		}

	case BlackmanWindow:
		for i := 0; i < f.size; i++ {
			val := 0.42 - 0.5*math.Cos(2.0*math.Pi*float64(i)/(n-1.0)) +
				0.08*math.Cos(4.0*math.Pi*float64(i)/(n-1.0))
			if val < 0 {
				val = 0
			}
			f.windowData[i] = val
		}

	case BlackmanHarrisWindow:
		a0, a1, a2, a3 := 0.35875, 0.48829, 0.14128, 0.01168
		for i := 0; i < f.size; i++ {
			f.windowData[i] = a0 - a1*math.Cos(2.0*math.Pi*float64(i)/(n-1.0)) +
				a2*math.Cos(4.0*math.Pi*float64(i)/(n-1.0)) -
				a3*math.Cos(6.0*math.Pi*float64(i)/(n-1.0))
		}

	case KaiserWindow:
		// Kaiser window with beta = 8.6 (good sidelobe suppression)
		beta := 8.6
		for i := 0; i < f.size; i++ {
			x := 2.0*float64(i)/(n-1.0) - 1.0
			f.windowData[i] = bessel0(beta*math.Sqrt(1.0-x*x)) / bessel0(beta)
		}

	case FlatTopWindow:
		a0, a1, a2, a3, a4 := 0.21557895, 0.41663158, 0.277263158, 0.083578947, 0.006947368
		for i := 0; i < f.size; i++ {
			val := a0 - a1*math.Cos(2.0*math.Pi*float64(i)/(n-1.0)) +
				a2*math.Cos(4.0*math.Pi*float64(i)/(n-1.0)) -
				a3*math.Cos(6.0*math.Pi*float64(i)/(n-1.0)) +
				a4*math.Cos(8.0*math.Pi*float64(i)/(n-1.0))
			if val < 0 {
				val = 0
			}
			f.windowData[i] = val
		}
	}
}

// Forward performs a forward FFT on the windowed input (zero-padded to the
// size) and returns the magnitude and phase spectra (size/2+1 bins). The
// slices are reused by the next call.
func (f *FFT) Forward(input []float64) (magnitude, phase []float64) {
	n := min(len(input), f.size)
	for i, x := range input[:n] {
		f.frame[i] = x * f.windowData[i]
	}
	clear(f.frame[n:])
	f.real.Forward(f.frame, f.spectrum)
	for i, z := range f.spectrum {
		f.magnitude[i] = math.Hypot(real(z), imag(z))
		f.phase[i] = math.Atan2(imag(z), real(z))
	}
	return f.magnitude, f.phase
}

// ForwardComplex performs a forward FFT on windowed complex input
// (zero-padded to the size) and returns the size bins.
func (f *FFT) ForwardComplex(input []complex128) []complex128 {
	f.makeComplex()
	n := min(len(input), f.size)
	for i, z := range input[:n] {
		f.work[i] = z * complex(f.windowData[i], 0)
	}
	clear(f.work[n:])
	f.complex.Forward(f.work)
	return append([]complex128(nil), f.work...)
}

func (f *FFT) makeComplex() {
	if f.complex == nil {
		f.complex, _ = fft.NewComplex(f.size)
		f.work = make([]complex128, f.size)
	}
}

// Inverse performs an inverse FFT of the size bins re + i·im and returns
// the real part of the signal.
func (f *FFT) Inverse(re, im []float64) []float64 {
	f.makeComplex()
	for i := range f.work {
		f.work[i] = complex(re[i], im[i])
	}
	f.complex.Inverse(f.work)
	result := make([]float64, f.size)
	for i, z := range f.work {
		result[i] = real(z)
	}
	return result
}

// GetMagnitudeDB returns the magnitude spectrum in decibels
func (f *FFT) GetMagnitudeDB() []float64 {
	db := make([]float64, len(f.magnitude))
	for i, mag := range f.magnitude {
		if mag > 0 {
			db[i] = 20.0 * math.Log10(mag)
		} else {
			db[i] = -120.0 // Floor at -120 dB
		}
	}
	return db
}

// GetFrequencyBin returns the frequency corresponding to a given FFT bin
func (f *FFT) GetFrequencyBin(bin int, sampleRate float64) float64 {
	return float64(bin) * sampleRate / float64(f.size)
}

// bessel0 computes the modified Bessel function of the first kind, order 0
func bessel0(x float64) float64 {
	if x == 0.0 {
		return 1.0
	}

	ax := math.Abs(x)

	if ax < 3.75 {
		y := x / 3.75
		y *= y
		return 1.0 + y*(3.5156229+y*(3.0899424+y*(1.2067492+
			y*(0.2659732+y*(0.360768e-1+y*0.45813e-2)))))
	}

	y := 3.75 / ax
	return (math.Exp(ax) / math.Sqrt(ax)) * (0.39894228 + y*(0.1328592e-1+
		y*(0.225319e-2+y*(-0.157565e-2+y*(0.916281e-2+
			y*(-0.2057706e-1+y*(0.2635537e-1+y*(-0.1647633e-1+
				y*0.392377e-2))))))))
}

// PowerSpectrum calculates the power spectrum from magnitude spectrum
func PowerSpectrum(magnitude []float64) []float64 {
	power := make([]float64, len(magnitude))
	for i, mag := range magnitude {
		power[i] = mag * mag
	}
	return power
}

// ApplyHannWindow applies a Hann window to the input data
func ApplyHannWindow(data []float64) []float64 {
	n := len(data)
	windowed := make([]float64, n)
	for i := 0; i < n; i++ {
		window := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(n-1)))
		windowed[i] = data[i] * window
	}
	return windowed
}

// CrossCorrelation computes the cross-correlation of two signals of the
// same length with FFTs: element n-1+k is Σ a[i]·b[i+k] for lags k from
// -(n-1) to n-1, so b delayed by d samples peaks at n-1+d.
func CrossCorrelation(a, b []float64) []float64 {
	n := len(a)
	if len(b) != n {
		panic("signals must have the same length")
	}
	size := 2
	for size < 2*n {
		size <<= 1
	}
	r, _ := fft.NewReal(size)
	paddedA := make([]float64, size)
	paddedB := make([]float64, size)
	copy(paddedA, a)
	copy(paddedB, b)
	specA := make([]complex128, r.Bins())
	specB := make([]complex128, r.Bins())
	r.Forward(paddedA, specA)
	r.Forward(paddedB, specB)
	for k, x := range specA {
		specA[k] = complex(real(x), -imag(x)) * specB[k]
	}
	correlation := paddedA
	r.Inverse(specA, correlation)

	result := make([]float64, 2*n-1)
	// Negative lags wrap to the end.
	copy(result, correlation[size-n+1:])
	copy(result[n-1:], correlation[:n])
	return result
}
