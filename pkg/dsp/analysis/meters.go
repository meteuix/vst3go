package analysis

import (
	"math"
	"sync"
)

// PeakMeter measures peak signal levels
type PeakMeter struct {
	peak       float64
	hold       float64
	holdTime   float64
	decayRate  float64
	sampleRate float64
	holdCount  int
	mu         sync.Mutex
}

// NewPeakMeter creates a new peak meter
func NewPeakMeter(sampleRate float64) *PeakMeter {
	return &PeakMeter{
		sampleRate: sampleRate,
		holdTime:   3.0,  // 3 seconds default
		decayRate:  20.0, // 20 dB/second
	}
}

// SetHoldTime sets the peak hold time in seconds
func (pm *PeakMeter) SetHoldTime(seconds float64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.holdTime = seconds
}

// SetDecayRate sets the peak decay rate in dB/second
func (pm *PeakMeter) SetDecayRate(dbPerSecond float64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.decayRate = dbPerSecond
}

// Process updates the peak meter with new samples
func (pm *PeakMeter) Process(samples []float64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Find peak in current block
	blockPeak := 0.0
	for _, sample := range samples {
		absSample := math.Abs(sample)
		if absSample > blockPeak {
			blockPeak = absSample
		}
	}

	// Update peak with decay
	samplesPerSecond := pm.sampleRate
	decayPerSample := pm.decayRate / samplesPerSecond / 20.0 * math.Log(10) // Convert dB to linear
	pm.peak *= math.Exp(-decayPerSample * float64(len(samples)))

	// Update peak if new value is higher
	if blockPeak > pm.peak {
		pm.peak = blockPeak
	}

	// Update hold
	if blockPeak > pm.hold {
		pm.hold = blockPeak
		pm.holdCount = int(pm.holdTime * pm.sampleRate)
	} else {
		pm.holdCount -= len(samples)
		if pm.holdCount <= 0 {
			pm.hold = pm.peak
			pm.holdCount = 0
		}
	}
}

// GetPeak returns the current peak level (linear)
func (pm *PeakMeter) GetPeak() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.peak
}

// GetPeakDB returns the current peak level in decibels
func (pm *PeakMeter) GetPeakDB() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.peak > 0 {
		return 20.0 * math.Log10(pm.peak)
	}
	return -math.Inf(1)
}

// GetHold returns the held peak level (linear)
func (pm *PeakMeter) GetHold() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.hold
}

// GetHoldDB returns the held peak level in decibels
func (pm *PeakMeter) GetHoldDB() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.hold > 0 {
		return 20.0 * math.Log10(pm.hold)
	}
	return -math.Inf(1)
}

// Reset clears the peak and hold values
func (pm *PeakMeter) Reset() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.peak = 0
	pm.hold = 0
	pm.holdCount = 0
}

// RMSMeter measures RMS (Root Mean Square) levels
type RMSMeter struct {
	windowSize int
	buffer     []float64
	writePos   int
	sum        float64
	count      int
	mu         sync.Mutex
}

// NewRMSMeter creates a new RMS meter with specified window size
func NewRMSMeter(windowSizeSamples int) *RMSMeter {
	return &RMSMeter{
		windowSize: windowSizeSamples,
		buffer:     make([]float64, windowSizeSamples),
	}
}

// Process updates the RMS meter with new samples
func (rm *RMSMeter) Process(samples []float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for _, sample := range samples {
		// Remove old value from sum
		oldValue := rm.buffer[rm.writePos]
		rm.sum -= oldValue * oldValue

		// Add new value
		rm.buffer[rm.writePos] = sample
		rm.sum += sample * sample

		// Update position
		rm.writePos = (rm.writePos + 1) % rm.windowSize
		if rm.count < rm.windowSize {
			rm.count++
		}
	}
}

// GetRMS returns the current RMS level (linear)
func (rm *RMSMeter) GetRMS() float64 {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.count == 0 {
		return 0
	}

	return math.Sqrt(rm.sum / float64(rm.count))
}

// GetRMSDB returns the current RMS level in decibels
func (rm *RMSMeter) GetRMSDB() float64 {
	rms := rm.GetRMS()
	if rms > 0 {
		return 20.0 * math.Log10(rms)
	}
	return -math.Inf(1)
}

// Reset clears the RMS buffer
func (rm *RMSMeter) Reset() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for i := range rm.buffer {
		rm.buffer[i] = 0
	}
	rm.sum = 0
	rm.count = 0
	rm.writePos = 0
}
