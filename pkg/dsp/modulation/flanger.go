package modulation

import (
	"math"
)

// Flanger limits, in milliseconds.
const (
	flangerMaxDelayMs = 10.0
	flangerMaxDepthMs = 10.0
)

// Flanger implements a classic flanger effect with feedback. The delay
// lines are sized for the longest delay when it is created, so every
// setter is safe on the audio thread. In stereo the right channel's sweep
// is inverted, which widens the image.
type Flanger struct {
	sampleRate float64

	// Parameters
	rate     float64 // LFO rate in Hz
	depth    float64 // Modulation depth in ms
	delay    float64 // Base delay time in ms (center delay)
	feedback float64 // Feedback amount (-1 to 1, negative for phase inversion)
	mix      float64 // Wet/dry mix (0-1)
	manual   float64 // Manual control for static flanging (0-1)

	delayL, delayR []float32
	writePos       int

	lfo *LFO

	feedbackL, feedbackR float32

	manualMode bool
}

// NewFlanger creates a new flanger effect
func NewFlanger(sampleRate float64) *Flanger {
	size := int(math.Ceil((flangerMaxDelayMs+flangerMaxDepthMs)*sampleRate/1000)) + 2
	f := &Flanger{
		sampleRate: sampleRate,
		rate:       0.5, // 0.5 Hz default
		depth:      2.0, // 2ms modulation depth
		delay:      5.0, // 5ms center delay
		feedback:   0.5, // 50% feedback
		mix:        0.5, // 50% wet
		manual:     0.5, // Center position
		delayL:     make([]float32, size),
		delayR:     make([]float32, size),
	}
	f.lfo = NewLFO(sampleRate)
	f.lfo.SetWaveform(WaveformTriangle) // Triangle is classic for flangers
	f.lfo.SetFrequency(f.rate)
	return f
}

// SetRate sets the LFO rate in Hz
func (f *Flanger) SetRate(hz float64) {
	f.rate = max(0.01, min(20.0, hz))
	f.lfo.SetFrequency(f.rate)
}

// SetDepth sets the modulation depth in milliseconds
func (f *Flanger) SetDepth(ms float64) {
	f.depth = max(0.0, min(flangerMaxDepthMs, ms))
}

// SetDelay sets the center delay time in milliseconds
func (f *Flanger) SetDelay(ms float64) {
	f.delay = max(0.1, min(flangerMaxDelayMs, ms))
}

// SetFeedback sets the feedback amount (-1 to 1)
func (f *Flanger) SetFeedback(feedback float64) {
	f.feedback = max(-0.99, min(0.99, feedback))
}

// SetMix sets the wet/dry mix (0=dry, 1=wet)
func (f *Flanger) SetMix(mix float64) {
	f.mix = max(0.0, min(1.0, mix))
}

// SetManual sets the manual control position (0-1)
func (f *Flanger) SetManual(position float64) {
	f.manual = max(0.0, min(1.0, position))
}

// SetManualMode enables/disables manual mode: the delay stays where Manual
// puts it instead of following the LFO.
func (f *Flanger) SetManualMode(enabled bool) {
	f.manualMode = enabled
}

// modulation returns the sweep position (-1 to 1) for the next sample.
func (f *Flanger) modulation() float64 {
	if f.manualMode {
		return 2.0*f.manual - 1.0
	}
	return f.lfo.Process()
}

// delaySamples converts a sweep position to a delay in samples.
func (f *Flanger) delaySamples(modulation float64) float64 {
	samples := (f.delay + f.depth*modulation) * f.sampleRate / 1000.0
	return max(1.0, min(float64(len(f.delayL)-2), samples))
}

// tick writes one input sample with feedback into a channel's delay line
// and returns the wet sample delaySamples behind it.
func (f *Flanger) tick(line []float32, fb *float32, input float32, delaySamples float64) float32 {
	line[f.writePos] = input + *fb*float32(f.feedback)
	size := len(line)
	pos := float64(f.writePos) - delaySamples
	if pos < 0 {
		pos += float64(size)
	}
	i := int(pos)
	frac := float32(pos - float64(i))
	// pos can round up to size.
	if i >= size {
		i -= size
	}
	j := i + 1
	if j == size {
		j = 0
	}
	wet := line[i]*(1-frac) + line[j]*frac
	*fb = wet
	return wet
}

// Process processes a mono sample
func (f *Flanger) Process(input float32) float32 {
	wet := f.tick(f.delayL, &f.feedbackL, input, f.delaySamples(f.modulation()))
	if f.writePos++; f.writePos == len(f.delayL) {
		f.writePos = 0
	}
	return input*(1-float32(f.mix)) + wet*float32(f.mix)
}

// ProcessStereo processes a stereo sample; the right channel sweeps in the
// opposite direction (in manual mode both stay at the manual position).
func (f *Flanger) ProcessStereo(inputL, inputR float32) (outputL, outputR float32) {
	m := f.modulation()
	mR := -m
	if f.manualMode {
		mR = m
	}
	wetL := f.tick(f.delayL, &f.feedbackL, inputL, f.delaySamples(m))
	wetR := f.tick(f.delayR, &f.feedbackR, inputR, f.delaySamples(mR))
	if f.writePos++; f.writePos == len(f.delayL) {
		f.writePos = 0
	}
	dry, wet := 1-float32(f.mix), float32(f.mix)
	return inputL*dry + wetL*wet, inputR*dry + wetR*wet
}

// ProcessBuffer processes a buffer of samples
func (f *Flanger) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = f.Process(input[i])
	}
}

// ProcessStereoBuffer processes stereo buffers. Inputs and outputs may be
// the same slices.
func (f *Flanger) ProcessStereoBuffer(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		outputL[i], outputR[i] = f.ProcessStereo(inputL[i], inputR[i])
	}
}

// Reset resets the flanger state
func (f *Flanger) Reset() {
	clear(f.delayL)
	clear(f.delayR)
	f.writePos = 0
	f.feedbackL, f.feedbackR = 0, 0
	f.lfo.Reset()
}
