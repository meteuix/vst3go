package modulation

import (
	"math"
)

// Chorus limits.
const (
	chorusMaxVoices  = 4
	chorusMaxDelayMs = 50.0
	chorusMaxDepthMs = 10.0
)

// Chorus implements a multi-voice stereo chorus. All voices read one delay
// line per channel, each with its own LFO phase, and are panned across the
// stereo field by Spread. The delay lines are sized for the longest delay
// when the chorus is created, so every setter is safe on the audio thread.
type Chorus struct {
	sampleRate float64

	// Parameters
	rate     float64 // LFO rate in Hz
	depth    float64 // Modulation depth in ms
	delay    float64 // Base delay time in ms
	mix      float64 // Wet/dry mix (0-1)
	feedback float64 // Feedback amount (0-0.5)
	spread   float64 // Stereo spread (0-1)
	voices   int     // Number of chorus voices

	delayL, delayR []float32
	writePos       int

	lfos       [chorusMaxVoices]*LFO
	panL, panR [chorusMaxVoices]float32

	// Feedback state
	feedbackL float32
	feedbackR float32
}

// NewChorus creates a new chorus effect
func NewChorus(sampleRate float64) *Chorus {
	size := int(math.Ceil((chorusMaxDelayMs+chorusMaxDepthMs)*sampleRate/1000)) + 2
	c := &Chorus{
		sampleRate: sampleRate,
		rate:       0.5,
		depth:      2.0,
		delay:      20.0,
		mix:        0.5,
		spread:     1.0,
		delayL:     make([]float32, size),
		delayR:     make([]float32, size),
	}
	for v := range c.lfos {
		c.lfos[v] = NewLFO(sampleRate)
		c.lfos[v].SetWaveform(WaveformSine)
		c.lfos[v].SetFrequency(c.rate)
	}
	c.SetVoices(2)
	return c
}

// SetRate sets the LFO rate in Hz
func (c *Chorus) SetRate(hz float64) {
	c.rate = max(0.01, min(10.0, hz))
	for _, lfo := range c.lfos {
		lfo.SetFrequency(c.rate)
	}
}

// SetDepth sets the modulation depth in milliseconds
func (c *Chorus) SetDepth(ms float64) {
	c.depth = max(0.0, min(chorusMaxDepthMs, ms))
}

// SetDelay sets the base delay time in milliseconds
func (c *Chorus) SetDelay(ms float64) {
	c.delay = max(1.0, min(chorusMaxDelayMs, ms))
}

// SetMix sets the wet/dry mix (0=dry, 1=wet)
func (c *Chorus) SetMix(mix float64) {
	c.mix = max(0.0, min(1.0, mix))
}

// SetFeedback sets the feedback amount
func (c *Chorus) SetFeedback(feedback float64) {
	c.feedback = max(0.0, min(0.5, feedback))
}

// SetSpread sets the stereo spread: 0 keeps every voice in the center, 1
// spreads them from left to right.
func (c *Chorus) SetSpread(spread float64) {
	c.spread = max(0.0, min(1.0, spread))
	c.updatePans()
}

// SetVoices sets the number of chorus voices (1-4). Changing it spaces the
// voices' LFO phases evenly again.
func (c *Chorus) SetVoices(voices int) {
	voices = max(1, min(chorusMaxVoices, voices))
	if voices == c.voices {
		return
	}
	c.voices = voices
	for v := range voices {
		c.lfos[v].SetPhase(float64(v) / float64(voices))
	}
	c.updatePans()
}

// updatePans places the voices across the stereo field with a constant
// power pan law, normalized so a centered voice has unity gain.
func (c *Chorus) updatePans() {
	for v := range c.voices {
		pan := 0.0 // -0.5 (left) to 0.5 (right)
		if c.voices > 1 {
			pan = (float64(v)/float64(c.voices-1) - 0.5) * c.spread
		}
		angle := (pan + 0.5) * math.Pi / 2
		c.panL[v] = float32(math.Cos(angle) * math.Sqrt2 / float64(c.voices))
		c.panR[v] = float32(math.Sin(angle) * math.Sqrt2 / float64(c.voices))
	}
}

// read returns the delay line's sample delaySamples behind the write
// position, linearly interpolated.
func (c *Chorus) read(line []float32, delaySamples float64) float32 {
	size := len(line)
	pos := float64(c.writePos) - delaySamples
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
	return line[i]*(1-frac) + line[j]*frac
}

// Process processes mono input
func (c *Chorus) Process(input float32) (outputL, outputR float32) {
	return c.ProcessStereo(input, input)
}

// ProcessStereo processes stereo input
func (c *Chorus) ProcessStereo(inputL, inputR float32) (outputL, outputR float32) {
	c.delayL[c.writePos] = inputL + c.feedbackL*float32(c.feedback)
	c.delayR[c.writePos] = inputR + c.feedbackR*float32(c.feedback)

	maxDelay := float64(len(c.delayL) - 2)
	var wetL, wetR float32
	for v := range c.voices {
		delayMs := c.delay + c.depth*c.lfos[v].Process()
		delaySamples := max(1.0, min(maxDelay, delayMs*c.sampleRate/1000.0))
		wetL += c.read(c.delayL, delaySamples) * c.panL[v]
		wetR += c.read(c.delayR, delaySamples) * c.panR[v]
	}
	c.feedbackL = wetL
	c.feedbackR = wetR
	if c.writePos++; c.writePos == len(c.delayL) {
		c.writePos = 0
	}

	dry, wet := float32(1.0-c.mix), float32(c.mix)
	return inputL*dry + wetL*wet, inputR*dry + wetR*wet
}

// ProcessBuffer processes a mono buffer
func (c *Chorus) ProcessBuffer(input, outputL, outputR []float32) {
	for i := range input {
		outputL[i], outputR[i] = c.Process(input[i])
	}
}

// ProcessStereoBuffer processes stereo buffers. Inputs and outputs may be
// the same slices.
func (c *Chorus) ProcessStereoBuffer(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		outputL[i], outputR[i] = c.ProcessStereo(inputL[i], inputR[i])
	}
}

// Reset resets the chorus state
func (c *Chorus) Reset() {
	clear(c.delayL)
	clear(c.delayR)
	for v, lfo := range c.lfos {
		lfo.Reset()
		lfo.SetPhase(float64(v) / float64(c.voices))
	}
	c.writePos = 0
	c.feedbackL = 0
	c.feedbackR = 0
}
