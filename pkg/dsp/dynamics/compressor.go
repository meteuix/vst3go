// Package dynamics provides dynamics processing effects like compressors, limiters, and gates
package dynamics

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/envelope"
)

// KneeType defines the compressor knee characteristic
type KneeType int

const (
	// KneeHard provides hard knee compression
	KneeHard KneeType = iota
	// KneeSoft provides soft knee compression
	KneeSoft
)

// Compressor implements a feed-forward compressor with flexible controls.
// All buffers are allocated in NewCompressor, so every method is safe to
// call from the audio thread.
type Compressor struct {
	sampleRate float64

	// Parameters
	threshold  float64  // Threshold in dB
	ratio      float64  // Compression ratio (e.g., 4.0 for 4:1)
	attack     float64  // Attack time in seconds
	release    float64  // Release time in seconds
	kneeWidth  float64  // Knee width in dB (0 for hard knee)
	makeupGain float64  // Makeup gain in dB
	kneeType   KneeType // Knee type
	lookahead  float64  // Lookahead time in seconds

	// Envelope detector
	detector *envelope.Detector

	// Lookahead delay lines, sized for the maximum lookahead in
	// NewCompressor
	delayL       []float32
	delayR       []float32
	delayIndex   int
	delaySamples int

	// State
	lastGainReduction float64 // For metering
}

// NewCompressor creates a new compressor
func NewCompressor(sampleRate float64) *Compressor {
	c := &Compressor{
		sampleRate: sampleRate,
		threshold:  -20.0, // -20 dB default
		ratio:      4.0,   // 4:1 default
		attack:     0.005, // 5ms default
		release:    0.050, // 50ms default
		kneeWidth:  2.0,   // 2dB soft knee default
		makeupGain: 0.0,
		kneeType:   KneeSoft,
		detector:   envelope.NewDetector(sampleRate, envelope.ModePeak),
		delayL:     make([]float32, int(math.Ceil(maxLookahead*sampleRate))),
		delayR:     make([]float32, int(math.Ceil(maxLookahead*sampleRate))),
	}

	// Configure detector for compressor use
	c.detector.SetType(envelope.TypeLogarithmic) // More musical response
	c.detector.SetTimeConstants(c.attack, c.release)

	return c
}

// SetThreshold sets the compression threshold in dB
func (c *Compressor) SetThreshold(dB float64) {
	c.threshold = dB
}

// SetRatio sets the compression ratio (1.0 = no compression, inf = limiting)
func (c *Compressor) SetRatio(ratio float64) {
	c.ratio = math.Max(1.0, ratio)
}

// SetAttack sets the attack time in seconds
func (c *Compressor) SetAttack(seconds float64) {
	c.attack = math.Max(0.0001, seconds)
	c.detector.SetAttack(c.attack)
}

// SetRelease sets the release time in seconds
func (c *Compressor) SetRelease(seconds float64) {
	c.release = math.Max(0.001, seconds)
	c.detector.SetRelease(c.release)
}

// SetKnee sets the knee type and width
func (c *Compressor) SetKnee(kneeType KneeType, widthDB float64) {
	c.kneeType = kneeType
	c.kneeWidth = math.Max(0.0, widthDB)
}

// SetMakeupGain sets the makeup gain in dB
func (c *Compressor) SetMakeupGain(dB float64) {
	c.makeupGain = dB
}

// SetLookahead sets the lookahead time in seconds (0 to 10ms). Changing it
// clears the delay lines.
func (c *Compressor) SetLookahead(seconds float64) {
	c.lookahead = math.Max(0.0, math.Min(maxLookahead, seconds))
	samples := min(int(math.Round(c.lookahead*c.sampleRate)), len(c.delayL))
	if samples != c.delaySamples {
		c.delaySamples = samples
		clear(c.delayL)
		clear(c.delayR)
		c.delayIndex = 0
	}
}

// Latency returns the delay the lookahead adds, in samples
func (c *Compressor) Latency() int {
	return c.delaySamples
}

// GetGainReduction returns the current gain reduction in dB (for metering)
func (c *Compressor) GetGainReduction() float64 {
	return c.lastGainReduction
}

// computeGain returns the gain reduction in dB for an input level, using
// the quadratic soft knee of Giannoulis, Massberg and Reiss (2012) when the
// knee is soft.
func (c *Compressor) computeGain(inputDB float64) float64 {
	slope := 1.0 - 1.0/c.ratio
	overshoot := inputDB - c.threshold

	if c.kneeType == KneeSoft && c.kneeWidth > 0 && math.Abs(overshoot) <= c.kneeWidth/2 {
		x := overshoot + c.kneeWidth/2
		return slope * x * x / (2 * c.kneeWidth)
	}
	if overshoot <= 0 {
		return 0.0
	}
	return overshoot * slope
}

// gain runs the detector on one detection sample and returns the linear
// gain, makeup included.
func (c *Compressor) gain(detection float32) float32 {
	env := c.detector.Detect(detection)
	inputDB := -96.0
	if env > 0 {
		inputDB = 20.0 * math.Log10(float64(env))
	}
	c.lastGainReduction = c.computeGain(inputDB)
	return float32(math.Pow(10.0, (c.makeupGain-c.lastGainReduction)/20.0))
}

// delay stores input in a channel's lookahead delay line and returns the
// sample leaving it. advanceDelay moves to the next slot.
func (c *Compressor) delay(line []float32, input float32) float32 {
	if c.delaySamples == 0 {
		return input
	}
	out := line[c.delayIndex]
	line[c.delayIndex] = input
	return out
}

func (c *Compressor) advanceDelay() {
	if c.delaySamples == 0 {
		return
	}
	c.delayIndex++
	if c.delayIndex == c.delaySamples {
		c.delayIndex = 0
	}
}

// Process processes a single sample. With lookahead, detection runs on the
// incoming sample and the gain is applied to the delayed one.
func (c *Compressor) Process(input float32) float32 {
	g := c.gain(input)
	out := c.delay(c.delayL, input)
	c.advanceDelay()
	return out * g
}

// ProcessBuffer processes a buffer of samples
func (c *Compressor) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = c.Process(input[i])
	}
}

// ProcessStereo processes stereo buffers with linked compression
func (c *Compressor) ProcessStereo(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		l, r := inputL[i], inputR[i]
		g := c.gain(float32(math.Max(math.Abs(float64(l)), math.Abs(float64(r)))))
		outputL[i] = c.delay(c.delayL, l) * g
		outputR[i] = c.delay(c.delayR, r) * g
		c.advanceDelay()
	}
}

// ProcessSidechain processes input using a sidechain signal for detection
func (c *Compressor) ProcessSidechain(input, sidechain, output []float32) {
	for i := range input {
		g := c.gain(sidechain[i])
		output[i] = c.delay(c.delayL, input[i]) * g
		c.advanceDelay()
	}
}

// ProcessStereoSidechain compresses a stereo pair by the level of a
// separate detection signal (key), for example the louder channel of a
// sidechain input. The same gain is applied to both channels.
func (c *Compressor) ProcessStereoSidechain(inputL, inputR, key, outputL, outputR []float32) {
	for i := range inputL {
		g := c.gain(key[i])
		outputL[i] = c.delay(c.delayL, inputL[i]) * g
		outputR[i] = c.delay(c.delayR, inputR[i]) * g
		c.advanceDelay()
	}
}

// Reset resets the compressor state
func (c *Compressor) Reset() {
	c.detector.Reset()
	c.lastGainReduction = 0.0
	c.delayIndex = 0
	clear(c.delayL)
	clear(c.delayR)
}
