package analysis

import "math"

// SpectrumAnalyzer provides real-time spectral analysis
type SpectrumAnalyzer struct {
	fftSize      int
	sampleRate   float64
	fft          *FFT
	buffer       []float64
	writePos     int
	hopSize      int
	averaging    AveragingMode
	avgBuffer    [][]float64
	avgWritePos  int
	avgCount     int
	smoothing    float64
	minFreq      float64
	maxFreq      float64
	minBin       int
	maxBin       int
	outputBuffer []float64
	out          published[spectrumReading]
}

// spectrumReading is the spectrum and analysis range readers see.
type spectrumReading struct {
	magnitude      []float64
	minBin, maxBin int
}

// AveragingMode defines how the spectrum is averaged over time
type AveragingMode int

const (
	NoAveraging AveragingMode = iota
	ExponentialAveraging
	LinearAveraging
	PeakHold
)

// NewSpectrumAnalyzer creates a new spectrum analyzer
func NewSpectrumAnalyzer(fftSize int, sampleRate float64, window WindowFunc) *SpectrumAnalyzer {
	sa := &SpectrumAnalyzer{
		fftSize:      fftSize,
		sampleRate:   sampleRate,
		fft:          NewFFT(fftSize, window),
		buffer:       make([]float64, fftSize),
		hopSize:      fftSize / 2, // 50% overlap by default
		averaging:    NoAveraging,
		smoothing:    0.9,
		minFreq:      20.0,
		maxFreq:      sampleRate / 2.0,
		outputBuffer: make([]float64, fftSize/2+1),
	}

	sa.out.init(func(r *spectrumReading) { r.magnitude = make([]float64, fftSize/2+1) })
	sa.updateFrequencyRange()
	return sa
}

// publish hands the current spectrum and range to readers.
func (sa *SpectrumAnalyzer) publish() {
	r := sa.out.write()
	copy(r.magnitude, sa.outputBuffer)
	r.minBin, r.maxBin = sa.minBin, sa.maxBin
	sa.out.publish()
}

// SetHopSize sets the hop size (samples between FFT frames)
func (sa *SpectrumAnalyzer) SetHopSize(hopSize int) {
	if hopSize > 0 && hopSize <= sa.fftSize {
		sa.hopSize = hopSize
	}
}

// SetAveraging sets the averaging mode and buffer size
func (sa *SpectrumAnalyzer) SetAveraging(mode AveragingMode, bufferSize int) {
	sa.averaging = mode
	if mode != NoAveraging && bufferSize > 0 {
		sa.avgBuffer = make([][]float64, bufferSize)
		for i := range sa.avgBuffer {
			sa.avgBuffer[i] = make([]float64, sa.fftSize/2+1)
		}
		sa.avgWritePos = 0
		sa.avgCount = 0
	}
}

// SetSmoothing sets the smoothing factor for exponential averaging (0-1)
func (sa *SpectrumAnalyzer) SetSmoothing(smoothing float64) {
	if smoothing >= 0 && smoothing <= 1 {
		sa.smoothing = smoothing
	}
}

// SetFrequencyRange sets the frequency range to analyze
func (sa *SpectrumAnalyzer) SetFrequencyRange(minFreq, maxFreq float64) {
	sa.minFreq = math.Max(0, minFreq)
	sa.maxFreq = math.Min(sa.sampleRate/2.0, maxFreq)
	sa.updateFrequencyRange()
}

// updateFrequencyRange updates the bin range based on frequency limits
func (sa *SpectrumAnalyzer) updateFrequencyRange() {
	binWidth := sa.sampleRate / float64(sa.fftSize)
	sa.minBin = int(sa.minFreq / binWidth)
	sa.maxBin = int(sa.maxFreq/binWidth) + 1

	if sa.maxBin > sa.fftSize/2 {
		sa.maxBin = sa.fftSize / 2
	}
	sa.publish()
}

// Process adds samples to the analyzer and returns true when new spectrum is available
func (sa *SpectrumAnalyzer) Process(samples []float64) bool {
	spectrumReady := false

	for _, sample := range samples {
		sa.buffer[sa.writePos] = sample
		sa.writePos++

		if sa.writePos >= sa.fftSize {
			// Perform FFT
			magnitude, _ := sa.fft.Forward(sa.buffer)

			// Apply averaging
			sa.applyAveraging(magnitude)

			// Shift buffer by hop size
			if sa.hopSize < sa.fftSize {
				copy(sa.buffer, sa.buffer[sa.hopSize:])
				sa.writePos = sa.fftSize - sa.hopSize
			} else {
				sa.writePos = 0
			}

			spectrumReady = true
		}
	}
	if spectrumReady {
		sa.publish()
	}
	return spectrumReady
}

// applyAveraging applies the selected averaging mode
func (sa *SpectrumAnalyzer) applyAveraging(magnitude []float64) {
	switch sa.averaging {
	case NoAveraging:
		copy(sa.outputBuffer, magnitude)

	case ExponentialAveraging:
		for i := range magnitude {
			sa.outputBuffer[i] = sa.outputBuffer[i]*sa.smoothing + magnitude[i]*(1-sa.smoothing)
		}

	case LinearAveraging:
		if sa.avgBuffer != nil {
			// Store current spectrum
			copy(sa.avgBuffer[sa.avgWritePos], magnitude)
			sa.avgWritePos = (sa.avgWritePos + 1) % len(sa.avgBuffer)

			if sa.avgCount < len(sa.avgBuffer) {
				sa.avgCount++
			}

			// Average all stored spectra
			for i := range sa.outputBuffer {
				sum := 0.0
				for j := 0; j < sa.avgCount; j++ {
					sum += sa.avgBuffer[j][i]
				}
				sa.outputBuffer[i] = sum / float64(sa.avgCount)
			}
		}

	case PeakHold:
		for i := range magnitude {
			if magnitude[i] > sa.outputBuffer[i] {
				sa.outputBuffer[i] = magnitude[i]
			}
		}
	}
}

// GetSpectrum returns the current magnitude spectrum
func (sa *SpectrumAnalyzer) GetSpectrum() []float64 {
	var result []float64
	sa.out.read(func(r *spectrumReading) { result = append([]float64(nil), r.magnitude...) })
	return result
}

// GetSpectrumDB returns the spectrum in decibels
func (sa *SpectrumAnalyzer) GetSpectrumDB() []float64 {
	return magnitudesToDB(sa.GetSpectrum())
}

// GetSpectrumInRange returns the spectrum only for the configured frequency range
func (sa *SpectrumAnalyzer) GetSpectrumInRange() []float64 {
	result := []float64{}
	sa.out.read(func(r *spectrumReading) {
		if r.minBin < r.maxBin {
			result = append(result, r.magnitude[r.minBin:r.maxBin]...)
		}
	})
	return result
}

// GetSpectrumDBInRange returns the spectrum in dB for the configured frequency range
func (sa *SpectrumAnalyzer) GetSpectrumDBInRange() []float64 {
	return magnitudesToDB(sa.GetSpectrumInRange())
}

// magnitudesToDB converts magnitudes to dB in place, flooring at -120 dB.
func magnitudesToDB(mags []float64) []float64 {
	for i, mag := range mags {
		if mag > 0 {
			mags[i] = 20.0 * math.Log10(mag)
		} else {
			mags[i] = -120.0
		}
	}
	return mags
}

// GetFrequencyForBin returns the frequency corresponding to a bin index
func (sa *SpectrumAnalyzer) GetFrequencyForBin(bin int) float64 {
	return float64(bin) * sa.sampleRate / float64(sa.fftSize)
}

// GetBinForFrequency returns the bin index for a given frequency
func (sa *SpectrumAnalyzer) GetBinForFrequency(freq float64) int {
	return int(freq * float64(sa.fftSize) / sa.sampleRate)
}

// Reset clears all buffers and averaging history
func (sa *SpectrumAnalyzer) Reset() {
	// Clear buffers
	for i := range sa.buffer {
		sa.buffer[i] = 0
	}
	for i := range sa.outputBuffer {
		sa.outputBuffer[i] = 0
	}

	// Reset positions
	sa.writePos = 0
	sa.avgWritePos = 0
	sa.avgCount = 0

	// Clear averaging buffers
	if sa.avgBuffer != nil {
		for i := range sa.avgBuffer {
			for j := range sa.avgBuffer[i] {
				sa.avgBuffer[i][j] = 0
			}
		}
	}
	sa.publish()
}

// GetPeakFrequency finds the frequency with the highest magnitude
func (sa *SpectrumAnalyzer) GetPeakFrequency() (float64, float64) {
	maxMag := 0.0
	maxBin := 0
	sa.out.read(func(r *spectrumReading) {
		for i := r.minBin; i < r.maxBin && i < len(r.magnitude); i++ {
			if r.magnitude[i] > maxMag {
				maxMag = r.magnitude[i]
				maxBin = i
			}
		}
	})
	return sa.GetFrequencyForBin(maxBin), maxMag
}

// GetBandEnergy calculates the energy in a frequency band
func (sa *SpectrumAnalyzer) GetBandEnergy(minFreq, maxFreq float64) float64 {
	minBin := max(0, sa.GetBinForFrequency(minFreq))
	maxBin := sa.GetBinForFrequency(maxFreq)
	energy := 0.0
	sa.out.read(func(r *spectrumReading) {
		for i := minBin; i <= min(maxBin, len(r.magnitude)-1); i++ {
			energy += r.magnitude[i] * r.magnitude[i]
		}
	})
	return energy
}

// GetOctaveBands returns spectrum data grouped into octave bands
func (sa *SpectrumAnalyzer) GetOctaveBands(centerFreqs []float64) []float64 {
	bands := make([]float64, len(centerFreqs))
	sa.out.read(func(r *spectrumReading) {
		for i, centerFreq := range centerFreqs {
			// Octave band limits
			lowerBin := sa.GetBinForFrequency(centerFreq / math.Sqrt2)
			upperBin := sa.GetBinForFrequency(centerFreq * math.Sqrt2)

			energy := 0.0
			count := 0
			for bin := max(0, lowerBin); bin <= upperBin && bin < len(r.magnitude); bin++ {
				energy += r.magnitude[bin] * r.magnitude[bin]
				count++
			}
			if count > 0 {
				bands[i] = math.Sqrt(energy / float64(count))
			}
		}
	})
	return bands
}

// StandardOctaveBands returns standard octave band center frequencies
func StandardOctaveBands() []float64 {
	return []float64{31.5, 63, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}
}

// StandardThirdOctaveBands returns standard 1/3 octave band center frequencies
func StandardThirdOctaveBands() []float64 {
	bands := []float64{}
	baseFreq := 1000.0

	// Generate bands from 20 Hz to 20 kHz
	for i := -16; i <= 13; i++ {
		freq := baseFreq * math.Pow(2, float64(i)/3.0)
		if freq >= 20 && freq <= 20000 {
			bands = append(bands, freq)
		}
	}

	return bands
}
