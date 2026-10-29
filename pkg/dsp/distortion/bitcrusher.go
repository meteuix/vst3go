package distortion

import (
	"math"
)

type DitherType int

const (
	DitherNone DitherType = iota
	DitherWhite
	DitherTriangular
)

type Bitcrusher struct {
	// User parameters
	bitDepth         float64
	sampleRateReduce float64
	antiAlias        bool
	dither           DitherType
	mix              float64
	output           float64

	// Internal state
	originalSampleRate float64

	// Sample rate reduction state, per channel: holdPhase counts down the
	// samples until the next one is taken.
	holdPhase [2]float64
	held      [2]float64

	// Anti-aliasing filter (one-pole low-pass), per channel
	filterAlpha float64
	filterState [2]float64

	randomState uint64 // dither noise generator
}

func NewBitcrusher(sampleRate float64) *Bitcrusher {
	return &Bitcrusher{
		bitDepth:           16.0,
		sampleRateReduce:   1.0,
		antiAlias:          true,
		dither:             DitherNone,
		mix:                1.0,
		output:             1.0,
		originalSampleRate: sampleRate,
		randomState:        1,
	}
}

func (b *Bitcrusher) SetBitDepth(bits float64) {
	b.bitDepth = math.Max(1.0, math.Min(32.0, bits))
}

func (b *Bitcrusher) SetSampleRateReduction(factor float64) {
	b.sampleRateReduce = math.Max(1.0, math.Min(100.0, factor))
	// One-pole low-pass just below the reduced rate's Nyquist frequency.
	omega := 2.0 * math.Pi * 0.45 / b.sampleRateReduce
	b.filterAlpha = math.Sin(omega) / (math.Sin(omega) + math.Cos(omega))
}

func (b *Bitcrusher) SetAntiAlias(enable bool) {
	b.antiAlias = enable
}

func (b *Bitcrusher) SetDither(dither DitherType) {
	b.dither = dither
}

func (b *Bitcrusher) SetMix(mix float64) {
	b.mix = math.Max(0.0, math.Min(1.0, mix))
}

func (b *Bitcrusher) SetOutput(output float64) {
	b.output = math.Max(0.0, math.Min(2.0, output))
}

func (b *Bitcrusher) Process(input float64) float64 {
	return b.processChannel(input, 0)
}

func (b *Bitcrusher) processChannel(input float64, channel int) float64 {
	processed := input

	// Apply anti-aliasing filter before sample rate reduction if enabled
	if b.antiAlias && b.sampleRateReduce > 1.0 {
		processed = b.applyAntiAliasFilter(processed, channel)
	}

	// Sample rate reduction
	processed = b.applySampleRateReduction(processed, channel)

	// Bit depth reduction
	processed = b.applyBitReduction(processed)

	// Mix with dry signal
	mixed := processed*b.mix + input*(1.0-b.mix)

	return mixed * b.output
}

func (b *Bitcrusher) ProcessBlock(input, output []float64) {
	for i := range input {
		output[i] = b.Process(input[i])
	}
}

func (b *Bitcrusher) ProcessStereo(inputL, inputR, outputL, outputR []float64) {
	for i := range inputL {
		outputL[i] = b.processChannel(inputL[i], 0)
		outputR[i] = b.processChannel(inputR[i], 1)
	}
}

func (b *Bitcrusher) applyBitReduction(x float64) float64 {
	if b.bitDepth >= 32.0 {
		return x
	}

	// Calculate quantization levels
	levels := math.Pow(2.0, b.bitDepth)

	// Add dither before quantization
	dithered := x + b.generateDither()

	// Scale to quantization range
	scaled := dithered * 0.5 * levels

	// Quantize
	quantized := math.Round(scaled) / levels * 2.0

	// Ensure output stays in bounds
	return math.Max(-1.0, math.Min(1.0, quantized))
}

// applySampleRateReduction holds one input sample for sampleRateReduce
// samples; fractional factors alternate between shorter and longer holds
// so the average rate is exact.
func (b *Bitcrusher) applySampleRateReduction(x float64, channel int) float64 {
	if b.sampleRateReduce <= 1.0 {
		return x
	}
	if b.holdPhase[channel] <= 0 {
		b.held[channel] = x
		b.holdPhase[channel] += b.sampleRateReduce
	}
	b.holdPhase[channel]--
	return b.held[channel]
}

func (b *Bitcrusher) applyAntiAliasFilter(x float64, channel int) float64 {
	b.filterState[channel] += b.filterAlpha * (x - b.filterState[channel])
	return b.filterState[channel]
}

// generateDither returns dither noise scaled to the quantization step:
// white is uniform over one step, triangular spans two.
func (b *Bitcrusher) generateDither() float64 {
	step := 2.0 / math.Pow(2.0, b.bitDepth)
	switch b.dither {
	case DitherWhite:
		return (b.random() - 0.5) * step
	case DitherTriangular:
		return (b.random() + b.random() - 1.0) * step
	default:
		return 0.0
	}
}

// random returns the next uniform value in [0, 1) of a 64-bit linear
// congruential generator, from its 53 high bits.
func (b *Bitcrusher) random() float64 {
	b.randomState = b.randomState*6364136223846793005 + 1442695040888963407
	return float64(b.randomState>>11) / (1 << 53)
}

func (b *Bitcrusher) Reset() {
	b.holdPhase = [2]float64{}
	b.held = [2]float64{}
	b.filterState = [2]float64{}
}

// Utility functions for specific bit-crushing effects

func QuantizeToSteps(x float64, steps int) float64 {
	if steps <= 1 {
		return 0.0
	}

	// Map to steps
	scaled := (x + 1.0) * 0.5 * float64(steps-1)
	quantized := math.Round(scaled)

	// Map back to [-1, 1]
	return quantized/float64(steps-1)*2.0 - 1.0
}

// FoldbackDistortion folds x back into [-threshold, threshold].
func FoldbackDistortion(x float64, threshold float64) float64 {
	return fold(x, threshold)
}

func GateEffect(x, threshold float64) float64 {
	// Hard gate effect often used with bit crushing
	if math.Abs(x) < threshold {
		return 0.0
	}
	return x
}
