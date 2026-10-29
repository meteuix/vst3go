package dynamics

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/envelope"
)

// Expander implements a downward expander for reducing low-level signals
type Expander struct {
	sampleRate float64

	// Parameters
	threshold float64 // Threshold in dB
	ratio     float64 // Expansion ratio (e.g., 2.0 for 2:1)
	attack    float64 // Opening time in seconds
	release   float64 // Closing time in seconds
	knee      float64 // Knee width in dB
	range_    float64 // Maximum expansion range in dB

	// Envelope detection
	detector *envelope.Detector

	// Smoothing
	currentGain  float64
	attackCoeff  float64
	releaseCoeff float64

	// State
	gainReduction float64 // Current gain reduction in dB (negative for expansion)
}

// NewExpander creates a new downward expander
func NewExpander(sampleRate float64) *Expander {
	e := &Expander{
		sampleRate:  sampleRate,
		threshold:   -40.0, // -40 dB default
		ratio:       2.0,   // 2:1 default
		attack:      0.001, // 1ms default
		release:     0.100, // 100ms default
		knee:        2.0,   // 2dB soft knee
		range_:      -40.0, // Max 40dB expansion
		currentGain: 1.0,
		detector:    envelope.NewDetector(sampleRate, envelope.ModePeak),
	}

	// Configure detector
	e.detector.SetType(envelope.TypeLogarithmic)
	e.updateTimeConstants()

	return e
}

// SetThreshold sets the expansion threshold in dB
func (e *Expander) SetThreshold(dB float64) {
	e.threshold = dB
}

// SetRatio sets the expansion ratio (1.0 = no expansion)
func (e *Expander) SetRatio(ratio float64) {
	e.ratio = math.Max(1.0, ratio)
}

// SetAttack sets how fast the expander opens, in seconds
func (e *Expander) SetAttack(seconds float64) {
	e.attack = math.Max(0.0, seconds)
	e.updateTimeConstants()
}

// SetRelease sets how fast the expander closes, in seconds
func (e *Expander) SetRelease(seconds float64) {
	e.release = math.Max(0.0, seconds)
	e.updateTimeConstants()
}

// SetKnee sets the knee width in dB
func (e *Expander) SetKnee(dB float64) {
	e.knee = math.Max(0.0, dB)
}

// SetRange sets the maximum expansion range in dB
func (e *Expander) SetRange(dB float64) {
	e.range_ = math.Min(0.0, dB)
}

// GetGainReduction returns the current gain reduction in dB
func (e *Expander) GetGainReduction() float64 {
	return e.gainReduction
}

// updateTimeConstants updates the attack and release coefficients. As in
// Gate, attack is how fast the expander opens (gain rises when the signal
// comes back) and release how fast it closes. The detector follows peaks
// almost instantly and falls with the release time, so the gain does not
// chase the waveform's zero crossings.
func (e *Expander) updateTimeConstants() {
	e.detector.SetTimeConstants(0, e.release)
	e.attackCoeff = smoothingCoeff(e.attack, e.sampleRate)
	e.releaseCoeff = smoothingCoeff(e.release, e.sampleRate)
}

// smoothingCoeff returns the one-pole coefficient for a time constant;
// 0 means instant.
func smoothingCoeff(seconds, sampleRate float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return math.Exp(-1.0 / (seconds * sampleRate))
}

// computeGain calculates the gain for a given input level
func (e *Expander) computeGain(inputDB float64) float64 {
	// Above threshold: no expansion
	if inputDB > e.threshold+e.knee/2 {
		return 0.0
	}

	// Below threshold - knee: full expansion
	if inputDB < e.threshold-e.knee/2 {
		// Expansion formula: gain = (input - threshold) * (ratio - 1)
		// This gives negative gain (reduction) for signals below threshold
		gain := (inputDB - e.threshold) * (e.ratio - 1.0)

		// Limit to range
		if gain < e.range_ {
			gain = e.range_
		}

		return gain
	}

	// In knee region: interpolate
	if e.knee > 0 {
		// Calculate position in knee (0 to 1)
		// 0 = top of knee (threshold + knee/2), 1 = bottom of knee
		kneePos := ((e.threshold + e.knee/2) - inputDB) / e.knee

		// Calculate full expansion at this level
		fullGain := (inputDB - e.threshold) * (e.ratio - 1.0)

		// Quadratic interpolation
		return kneePos * kneePos * fullGain
	}

	return 0.0
}

// gain runs the detector on one detection sample and returns the smoothed
// linear gain.
func (e *Expander) gain(detection float32) float32 {
	env := e.detector.Detect(detection)
	inputDB := -96.0
	if env > 0 {
		inputDB = 20.0 * math.Log10(float64(env))
	}
	targetGain := math.Pow(10.0, e.computeGain(inputDB)/20.0)

	coeff := e.releaseCoeff // closing
	if targetGain > e.currentGain {
		coeff = e.attackCoeff // opening
	}
	e.currentGain = targetGain + (e.currentGain-targetGain)*coeff

	e.gainReduction = 0.0
	if e.currentGain < 1.0 {
		e.gainReduction = 20.0 * math.Log10(e.currentGain)
	}
	return float32(e.currentGain)
}

// Process processes a single sample
func (e *Expander) Process(input float32) float32 {
	return input * e.gain(input)
}

// ProcessBuffer processes a buffer of samples
func (e *Expander) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = e.Process(input[i])
	}
}

// ProcessStereo processes stereo buffers with linked expansion: the louder
// channel drives the gain of both.
func (e *Expander) ProcessStereo(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		g := e.gain(max(float32(math.Abs(float64(inputL[i]))), float32(math.Abs(float64(inputR[i])))))
		outputL[i] = inputL[i] * g
		outputR[i] = inputR[i] * g
	}
}

// Reset resets the expander state
func (e *Expander) Reset() {
	e.detector.Reset()
	e.currentGain = 1.0
	e.gainReduction = 0.0
}
