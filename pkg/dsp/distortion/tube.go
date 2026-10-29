package distortion

import (
	"math"
)

// TubeSaturation models a tube stage: pre-emphasis, bias, hysteresis, an
// asymmetric (even harmonic) and a symmetric (odd harmonic) transfer
// curve, soft compression and de-emphasis. The input is soft-clipped into
// the curves' [-1, 1] range first, so the output stays bounded however
// hard it is driven. A DC blocker removes the offset the bias and the
// asymmetric curve create. Stereo channels have separate state.
type TubeSaturation struct {
	warmth     float64
	harmonics  float64
	bias       float64
	hysteresis float64
	mix        float64
	output     float64

	dcCoeff float64 // DC blocker pole
	state   [2]tubeChannel
}

// tubeChannel is the per-channel filter and hysteresis state.
type tubeChannel struct {
	prevInput, prevOutput float64 // hysteresis
	preEmphasis           float64
	deEmphasis            float64
	dcIn, dcOut           float64
}

// NewTubeSaturation creates a tube stage for audio at sampleRate.
func NewTubeSaturation(sampleRate float64) *TubeSaturation {
	return &TubeSaturation{
		warmth:     0.5,
		harmonics:  0.5,
		bias:       0.0,
		hysteresis: 0.1,
		mix:        1.0,
		output:     1.0,
		dcCoeff:    math.Exp(-2 * math.Pi * 10 / sampleRate), // 10 Hz
	}
}

func (t *TubeSaturation) SetWarmth(warmth float64) {
	t.warmth = math.Max(0.0, math.Min(1.0, warmth))
}

func (t *TubeSaturation) SetHarmonics(harmonics float64) {
	t.harmonics = math.Max(0.0, math.Min(1.0, harmonics))
}

func (t *TubeSaturation) SetBias(bias float64) {
	t.bias = math.Max(-1.0, math.Min(1.0, bias))
}

func (t *TubeSaturation) SetHysteresis(hysteresis float64) {
	t.hysteresis = math.Max(0.0, math.Min(1.0, hysteresis))
}

func (t *TubeSaturation) SetMix(mix float64) {
	t.mix = math.Max(0.0, math.Min(1.0, mix))
}

func (t *TubeSaturation) SetOutput(output float64) {
	t.output = math.Max(0.0, math.Min(2.0, output))
}

func (t *TubeSaturation) Process(input float64) float64 {
	return t.processChannel(input, &t.state[0])
}

func (t *TubeSaturation) processChannel(input float64, c *tubeChannel) float64 {
	// Pre-emphasis for warmth (boost highs before saturation)
	emphasized := t.preEmphasize(c, input)

	// Bias shifts the operating point, soft-clipped into the curves' range
	biased := math.Tanh(emphasized + t.bias*0.1)

	withHysteresis := t.applyHysteresis(c, biased)
	saturated := t.tubeSaturate(withHysteresis)

	// De-emphasis (reduce highs after saturation for warmth), then remove DC
	wet := t.removeDC(c, t.deEmphasize(c, saturated))

	return (wet*t.mix + input*(1.0-t.mix)) * t.output
}

func (t *TubeSaturation) ProcessBlock(input, output []float64) {
	for i := range input {
		output[i] = t.Process(input[i])
	}
}

func (t *TubeSaturation) ProcessStereo(inputL, inputR, outputL, outputR []float64) {
	for i := range inputL {
		outputL[i] = t.processChannel(inputL[i], &t.state[0])
		outputR[i] = t.processChannel(inputR[i], &t.state[1])
	}
}

func (t *TubeSaturation) tubeSaturate(x float64) float64 {
	// Multiple stages of tube-like saturation

	// First stage: soft clipping with even harmonics
	stage1 := t.evenHarmonicSaturation(x)

	// Second stage: add odd harmonics based on harmonics parameter
	stage2 := stage1*(1.0-t.harmonics*0.5) + t.oddHarmonicSaturation(x)*t.harmonics*0.5

	// Third stage: overall tube compression curve
	compressed := t.tubeCompressionCurve(stage2)

	return compressed
}

func (t *TubeSaturation) evenHarmonicSaturation(x float64) float64 {
	// Generate even harmonics (2nd, 4th, etc.) - characteristic of tubes
	// Using asymmetric transfer function
	if x >= 0 {
		return x - 0.1*x*x + 0.05*x*x*x*x
	} else {
		absX := -x
		return -(absX - 0.15*absX*absX + 0.08*absX*absX*absX*absX)
	}
}

func (t *TubeSaturation) oddHarmonicSaturation(x float64) float64 {
	// Generate odd harmonics (3rd, 5th, etc.)
	// Using symmetric transfer function
	return x - 0.15*x*x*x + 0.05*x*x*x*x*x
}

func (t *TubeSaturation) tubeCompressionCurve(x float64) float64 {
	// Smooth compression curve characteristic of tubes
	// Soft knee compression with gradual onset
	threshold := 0.7
	ratio := 3.0

	absX := math.Abs(x)
	if absX <= threshold {
		return x
	}

	// Soft knee region
	knee := 0.1
	if absX < threshold+knee {
		// Quadratic interpolation in knee region
		factor := (absX - threshold) / knee
		compression := 1.0 + (1.0/ratio-1.0)*factor*factor
		return math.Copysign(threshold+(absX-threshold)*compression, x)
	}

	// Above knee: full compression
	compressed := threshold + (absX-threshold)/ratio
	return math.Copysign(compressed, x)
}

func (t *TubeSaturation) applyHysteresis(c *tubeChannel, x float64) float64 {
	// The output follows the input more slowly when it falls than when it rises
	follow := 1.0 - t.hysteresis*0.5
	if x > c.prevInput {
		follow = 1.0 - t.hysteresis*0.3
	}
	c.prevOutput += (x - c.prevOutput) * follow
	c.prevInput = x
	return c.prevOutput
}

func (t *TubeSaturation) preEmphasize(c *tubeChannel, x float64) float64 {
	// First-order high-pass; higher warmth = more pre-emphasis
	cutoff := 0.1 + t.warmth*0.4
	high := x - c.preEmphasis
	c.preEmphasis += high * cutoff
	return x + high*t.warmth*0.5
}

func (t *TubeSaturation) deEmphasize(c *tubeChannel, x float64) float64 {
	// First-order low-pass; higher warmth = more de-emphasis
	cutoff := 0.9 - t.warmth*0.6
	c.deEmphasis += (x - c.deEmphasis) * cutoff
	return c.deEmphasis
}

// removeDC is a one-pole DC blocker: y = x - x1 + R*y1.
func (t *TubeSaturation) removeDC(c *tubeChannel, x float64) float64 {
	y := x - c.dcIn + t.dcCoeff*c.dcOut
	c.dcIn, c.dcOut = x, y
	return y
}

func (t *TubeSaturation) Reset() {
	t.state = [2]tubeChannel{}
}
