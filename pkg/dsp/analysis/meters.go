package analysis

import "math"

// PeakMeter measures peak signal levels.
type PeakMeter struct {
	peak       float64
	hold       float64
	holdTime   float64
	decayRate  float64
	sampleRate float64
	holdCount  int
	out        published[peakReading]
}

type peakReading struct{ peak, hold float64 }

// NewPeakMeter creates a new peak meter
func NewPeakMeter(sampleRate float64) *PeakMeter {
	pm := &PeakMeter{
		sampleRate: sampleRate,
		holdTime:   3.0,  // 3 seconds default
		decayRate:  20.0, // 20 dB/second
	}
	pm.out.init(func(*peakReading) {})
	return pm
}

// SetHoldTime sets the peak hold time in seconds
func (pm *PeakMeter) SetHoldTime(seconds float64) { pm.holdTime = seconds }

// SetDecayRate sets the peak decay rate in dB/second
func (pm *PeakMeter) SetDecayRate(dbPerSecond float64) { pm.decayRate = dbPerSecond }

// Process updates the peak meter with new samples
func (pm *PeakMeter) Process(samples []float64) {
	blockPeak := 0.0
	for _, sample := range samples {
		blockPeak = max(blockPeak, math.Abs(sample))
	}

	// Decay, converting dB per second to a linear factor per sample.
	decayPerSample := pm.decayRate / pm.sampleRate / 20.0 * math.Ln10
	pm.peak *= math.Exp(-decayPerSample * float64(len(samples)))
	pm.peak = max(pm.peak, blockPeak)

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
	pm.publish()
}

func (pm *PeakMeter) publish() {
	*pm.out.write() = peakReading{peak: pm.peak, hold: pm.hold}
	pm.out.publish()
}

// GetPeak returns the current peak level (linear)
func (pm *PeakMeter) GetPeak() float64 { return pm.out.get().peak }

// GetPeakDB returns the current peak level in decibels
func (pm *PeakMeter) GetPeakDB() float64 { return linearToDB(pm.GetPeak()) }

// GetHold returns the held peak level (linear)
func (pm *PeakMeter) GetHold() float64 { return pm.out.get().hold }

// GetHoldDB returns the held peak level in decibels
func (pm *PeakMeter) GetHoldDB() float64 { return linearToDB(pm.GetHold()) }

// Reset clears the peak and hold values
func (pm *PeakMeter) Reset() {
	pm.peak, pm.hold, pm.holdCount = 0, 0, 0
	pm.publish()
}

// RMSMeter measures RMS (Root Mean Square) levels
type RMSMeter struct {
	windowSize int
	buffer     []float64
	writePos   int
	sum        float64
	count      int
	out        published[float64]
}

// NewRMSMeter creates a new RMS meter with specified window size
func NewRMSMeter(windowSizeSamples int) *RMSMeter {
	rm := &RMSMeter{
		windowSize: windowSizeSamples,
		buffer:     make([]float64, windowSizeSamples),
	}
	rm.out.init(func(*float64) {})
	return rm
}

// Process updates the RMS meter with new samples
func (rm *RMSMeter) Process(samples []float64) {
	for _, sample := range samples {
		old := rm.buffer[rm.writePos]
		rm.sum += sample*sample - old*old
		rm.buffer[rm.writePos] = sample
		rm.writePos = (rm.writePos + 1) % rm.windowSize
		if rm.count < rm.windowSize {
			rm.count++
		}
	}
	rm.publish()
}

func (rm *RMSMeter) publish() {
	rms := 0.0
	if rm.count > 0 {
		rms = math.Sqrt(max(0, rm.sum) / float64(rm.count))
	}
	*rm.out.write() = rms
	rm.out.publish()
}

// GetRMS returns the current RMS level (linear)
func (rm *RMSMeter) GetRMS() float64 { return rm.out.get() }

// GetRMSDB returns the current RMS level in decibels
func (rm *RMSMeter) GetRMSDB() float64 { return linearToDB(rm.GetRMS()) }

// Reset clears the RMS buffer
func (rm *RMSMeter) Reset() {
	clear(rm.buffer)
	rm.sum, rm.count, rm.writePos = 0, 0, 0
	rm.publish()
}

// linearToDB converts an amplitude to dB, -Inf for silence.
func linearToDB(x float64) float64 {
	if x > 0 {
		return 20.0 * math.Log10(x)
	}
	return math.Inf(-1)
}
