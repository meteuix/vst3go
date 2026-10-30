package distortion

import (
	"math"

	"github.com/th-release/vst3go/pkg/dsp/fastmath"
)

// Tape flutter delay: a fixed center delay the flutter swings around.
const (
	flutterCenter = 5.0 // samples
	flutterDepth  = 3.0 // samples at full Flutter
)

// TapeSaturation models tape: pre-emphasis, program-dependent
// compression, a bounded soft saturation curve, flutter (a wandering
// delay), hiss and de-emphasis. Stereo channels have separate filter,
// compression and delay state and share the flutter, as both tracks of a
// tape do. With Flutter on, the signal is delayed by about 5 samples.
type TapeSaturation struct {
	// User parameters
	saturation  float64
	compression float64
	flutter     float64
	warmth      float64
	mix         float64
	output      float64

	sampleRate float64

	// Compression envelope coefficients
	attackCoeff, releaseCoeff float64

	// Flutter LFO
	flutterPhase float64
	flutterRate  float64

	state [2]tapeChannel

	noiseLevel  float64
	randomState uint64
}

// tapeChannel is the per-channel state.
type tapeChannel struct {
	preEmphasis, deEmphasis float64
	envelope                float64
	delay                   [16]float64 // flutter delay line
	writePos                int
}

func NewTapeSaturation(sampleRate float64) *TapeSaturation {
	t := &TapeSaturation{
		saturation:   0.5,
		compression:  0.5,
		flutter:      0.0,
		warmth:       0.5,
		mix:          1.0,
		output:       1.0,
		sampleRate:   sampleRate,
		attackCoeff:  1 - math.Exp(-1/(0.002*sampleRate)), // 2 ms
		releaseCoeff: 1 - math.Exp(-1/(0.100*sampleRate)), // 100 ms
		noiseLevel:   0.0001,
		randomState:  1,
	}
	t.flutterRate = 0.3 + t.random()*0.2 // 0.3-0.5 Hz
	return t
}
func (t *TapeSaturation) SetSaturation(saturation float64) {
	t.saturation = max(0.0, min(1.0, saturation))
}

func (t *TapeSaturation) SetCompression(compression float64) {
	t.compression = max(0.0, min(1.0, compression))
}

func (t *TapeSaturation) SetFlutter(flutter float64) {
	t.flutter = max(0.0, min(1.0, flutter))
}

func (t *TapeSaturation) SetWarmth(warmth float64) {
	t.warmth = max(0.0, min(1.0, warmth))
}

func (t *TapeSaturation) SetMix(mix float64) {
	t.mix = max(0.0, min(1.0, mix))
}

func (t *TapeSaturation) SetOutput(output float64) {
	t.output = max(0.0, min(2.0, output))
}

func (t *TapeSaturation) Process(input float64) float64 {
	return t.processChannel(input, &t.state[0], t.flutterDelay())
}

func (t *TapeSaturation) processChannel(input float64, c *tapeChannel, delay float64) float64 {
	// Pre-emphasis (boost highs before saturation)
	emphasized := t.preEmphasize(c, input)

	compressed := t.tapeCompress(c, emphasized)
	saturated := t.tapeSaturate(compressed)
	fluttered := t.applyFlutter(c, saturated, delay)

	// Add subtle tape noise
	withNoise := fluttered + (t.random()*2.0-1.0)*t.noiseLevel*t.saturation

	// De-emphasis (cut highs after saturation)
	wet := t.deEmphasize(c, withNoise)

	return (wet*t.mix + input*(1.0-t.mix)) * t.output
}

func (t *TapeSaturation) ProcessBlock(input, output []float64) {
	for i := range input {
		output[i] = t.Process(input[i])
	}
}

func (t *TapeSaturation) ProcessStereo(inputL, inputR, outputL, outputR []float64) {
	for i := range inputL {
		delay := t.flutterDelay()
		outputL[i] = t.processChannel(inputL[i], &t.state[0], delay)
		outputR[i] = t.processChannel(inputR[i], &t.state[1], delay)
	}
}

// tapeSaturate is a soft clipper with emphasized 3rd harmonic. The cubic
// stage, 1.5s - 0.5s^3, is monotonic on the tanh output's [-1, 1] range,
// so the curve is bounded and never folds back.
func (t *TapeSaturation) tapeSaturate(x float64) float64 {
	drive := 1.0 + t.saturation*4.0
	s := math.Tanh(x * drive * 0.7)
	return s*(1-0.2*t.saturation) + (1.5*s-0.5*s*s*s)*0.2*t.saturation
}

// tapeCompress is a program-dependent compressor with a 2 ms attack and a
// 100 ms release above a 0.5 threshold.
func (t *TapeSaturation) tapeCompress(c *tapeChannel, x float64) float64 {
	absX := math.Abs(x)
	coeff := t.releaseCoeff
	if absX > c.envelope {
		coeff = t.attackCoeff
	}
	c.envelope += (absX - c.envelope) * coeff

	threshold := 0.5
	ratio := 2.0 + t.compression*3.0 // 2:1 to 5:1
	if c.envelope > threshold {
		compressed := threshold + (c.envelope-threshold)/ratio
		return x * compressed / c.envelope
	}
	return x
}

// flutterDelay advances the flutter LFO by one sample and returns the
// delay in samples, or 0 when Flutter is off.
func (t *TapeSaturation) flutterDelay() float64 {
	if t.flutter < 0.01 {
		return 0
	}
	t.flutterPhase += 2.0 * math.Pi * t.flutterRate / t.sampleRate
	if t.flutterPhase > 2.0*math.Pi {
		t.flutterPhase -= 2.0 * math.Pi
		// Occasionally change flutter rate slightly
		if t.random() < 0.1 {
			t.flutterRate = 0.3 + t.random()*0.2
		}
	}
	depth := t.flutter * flutterDepth
	modulation := fastmath.Sin2Pi(t.flutterPhase*(0.5/math.Pi))*depth + (t.random()*2.0-1.0)*depth*0.3
	return flutterCenter + modulation
}

// applyFlutter delays x by delay samples (linearly interpolated); a zero
// delay bypasses it.
func (t *TapeSaturation) applyFlutter(c *tapeChannel, x, delay float64) float64 {
	size := len(c.delay)
	c.delay[c.writePos] = x
	if c.writePos++; c.writePos == size {
		c.writePos = 0
	}
	if delay == 0 {
		return x
	}
	i := int(delay)
	frac := delay - float64(i)
	// The newest sample is at writePos-1: newer is i samples before it.
	newer := c.writePos - 1 - i
	for newer < 0 {
		newer += size
	}
	older := newer - 1
	if older < 0 {
		older += size
	}
	return c.delay[newer]*(1.0-frac) + c.delay[older]*frac
}

func (t *TapeSaturation) preEmphasize(c *tapeChannel, x float64) float64 {
	// CCIR pre-emphasis approximation: more warmth, more high boost
	cutoff := 0.15 + t.warmth*0.1
	high := x - c.preEmphasis
	c.preEmphasis += high * cutoff
	return x + high*t.warmth*0.3
}

func (t *TapeSaturation) deEmphasize(c *tapeChannel, x float64) float64 {
	// CCIR de-emphasis approximation: low-pass
	cutoff := 0.8 - t.warmth*0.5
	c.deEmphasis += (x - c.deEmphasis) * cutoff
	return c.deEmphasis
}

// random returns the next uniform value in [0, 1) of a 64-bit linear
// congruential generator.
func (t *TapeSaturation) random() float64 {
	t.randomState = t.randomState*6364136223846793005 + 1442695040888963407
	return float64(t.randomState>>11) / (1 << 53)
}

func (t *TapeSaturation) Reset() {
	t.state = [2]tapeChannel{}
	t.flutterPhase = 0.0
}
