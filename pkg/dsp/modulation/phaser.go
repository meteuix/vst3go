package modulation

import (
	"math"
)

// AllPassFilter implements a first-order all-pass filter for phaser stages
type AllPassFilter struct {
	a1    float64 // Coefficient
	state float32 // Filter state
}

// NewAllPassFilter creates a new all-pass filter
func NewAllPassFilter() *AllPassFilter {
	return &AllPassFilter{}
}

// SetFrequency sets the filter frequency
func (f *AllPassFilter) SetFrequency(freq, sampleRate float64) {
	// Calculate coefficient for all-pass filter
	// Using bilinear transform: a1 = (1 - tan(pi*fc/fs)) / (1 + tan(pi*fc/fs))
	tanFreq := math.Tan(math.Pi * freq / sampleRate)
	f.a1 = (1.0 - tanFreq) / (1.0 + tanFreq)
}

// Process processes a sample through the all-pass filter
func (f *AllPassFilter) Process(input float32) float32 {
	// First-order all-pass filter
	// y[n] = a1*x[n] + x[n-1] - a1*y[n-1]
	// But we need to store the right state

	// Calculate output
	output := float32(f.a1)*input + f.state

	// Update state for next sample
	// state = x[n] - a1*y[n]
	f.state = input - float32(f.a1)*output

	return output
}

// Reset resets the filter state
func (f *AllPassFilter) Reset() {
	f.state = 0
}

const phaserMaxStages = 8

// Phaser implements a classic phaser effect with multiple all-pass filter
// stages. The sweep runs between a minimum and maximum frequency, set
// either from a center frequency and depth (SetCenterFrequency, SetDepth)
// or directly (SetFrequencyRange); the last call wins. In stereo the right
// channel sweeps opposite to the left. All stages are allocated up front,
// so every setter is safe on the audio thread.
type Phaser struct {
	sampleRate float64

	// Parameters
	rate       float64 // LFO rate in Hz
	depth      float64 // Modulation depth (0-1)
	centerFreq float64 // Center frequency for modulation
	feedback   float64 // Feedback amount (-1 to 1)
	mix        float64 // Wet/dry mix (0-1)
	stages     int     // Number of all-pass stages (2, 4, 6, or 8)

	// All-pass stages and feedback state per channel
	filters [2][phaserMaxStages]AllPassFilter
	fbState [2]float32

	lfo *LFO

	// Frequency range for modulation
	minFreq float64
	maxFreq float64
}

// NewPhaser creates a new phaser effect
func NewPhaser(sampleRate float64) *Phaser {
	p := &Phaser{
		sampleRate: sampleRate,
		rate:       0.5,    // 0.5 Hz default
		depth:      0.5,    // 50% depth
		centerFreq: 1000.0, // 1kHz center
		feedback:   0.5,    // 50% feedback
		mix:        0.5,    // 50% wet
		stages:     4,      // 4 stages default
	}
	p.lfo = NewLFO(sampleRate)
	p.lfo.SetWaveform(WaveformSine)
	p.lfo.SetFrequency(p.rate)
	p.updateFrequencyRange()
	return p
}

// SetRate sets the LFO rate in Hz
func (p *Phaser) SetRate(hz float64) {
	p.rate = max(0.01, min(10.0, hz))
	p.lfo.SetFrequency(p.rate)
}

// SetDepth sets the modulation depth (0-1): the sweep spans depth times
// the center frequency.
func (p *Phaser) SetDepth(depth float64) {
	p.depth = max(0.0, min(1.0, depth))
	p.updateFrequencyRange()
}

// SetCenterFrequency sets the center frequency for modulation
func (p *Phaser) SetCenterFrequency(freq float64) {
	p.centerFreq = max(100.0, min(4000.0, freq))
	p.updateFrequencyRange()
}

// SetFrequencyRange sets the min and max frequencies for modulation
func (p *Phaser) SetFrequencyRange(minFreq, maxFreq float64) {
	p.minFreq = max(20.0, min(p.sampleRate/4, minFreq))
	p.maxFreq = max(p.minFreq+100, min(p.sampleRate/2, maxFreq))
	p.centerFreq = (p.minFreq + p.maxFreq) / 2
}

// SetFeedback sets the feedback amount (-1 to 1)
func (p *Phaser) SetFeedback(feedback float64) {
	p.feedback = max(-0.99, min(0.99, feedback))
}

// SetMix sets the wet/dry mix (0=dry, 1=wet)
func (p *Phaser) SetMix(mix float64) {
	p.mix = max(0.0, min(1.0, mix))
}

// SetStages sets the number of all-pass stages (2, 4, 6, or 8)
func (p *Phaser) SetStages(stages int) {
	stages = max(2, min(phaserMaxStages, stages))
	p.stages = stages - stages%2
}

// updateFrequencyRange updates the frequency range based on center and depth
func (p *Phaser) updateFrequencyRange() {
	freqRange := p.centerFreq * p.depth
	p.minFreq = max(20.0, p.centerFreq-freqRange/2)
	p.maxFreq = min(p.sampleRate/4, p.centerFreq+freqRange/2)
}

// sweep returns the all-pass frequency for a sweep position (-1 to 1),
// spaced exponentially between the minimum and maximum frequency.
func (p *Phaser) sweep(position float64) float64 {
	return p.minFreq * math.Pow(p.maxFreq/p.minFreq, (position+1)/2)
}

// channel runs one channel's feedback all-pass cascade at freq and returns
// the wet sample.
func (p *Phaser) channel(ch int, input float32, freq float64) float32 {
	stages := p.filters[ch][:p.stages]
	stages[0].SetFrequency(freq, p.sampleRate)
	a1 := stages[0].a1
	wet := input + p.fbState[ch]*float32(p.feedback)
	for i := range stages {
		stages[i].a1 = a1
		wet = stages[i].Process(wet)
	}
	p.fbState[ch] = wet
	return wet
}

// Process processes a mono sample
func (p *Phaser) Process(input float32) float32 {
	wet := p.channel(0, input, p.sweep(p.lfo.Process()))
	return input*float32(1-p.mix) + wet*float32(p.mix)
}

// ProcessStereo processes a stereo sample; the right channel sweeps
// opposite to the left.
func (p *Phaser) ProcessStereo(inputL, inputR float32) (outputL, outputR float32) {
	m := p.lfo.Process()
	wetL := p.channel(0, inputL, p.sweep(m))
	wetR := p.channel(1, inputR, p.sweep(-m))
	dry, wet := float32(1-p.mix), float32(p.mix)
	return inputL*dry + wetL*wet, inputR*dry + wetR*wet
}

// ProcessBuffer processes a buffer of samples
func (p *Phaser) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = p.Process(input[i])
	}
}

// ProcessStereoBuffer processes stereo buffers. Inputs and outputs may be
// the same slices.
func (p *Phaser) ProcessStereoBuffer(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		outputL[i], outputR[i] = p.ProcessStereo(inputL[i], inputR[i])
	}
}

// Reset resets the phaser state
func (p *Phaser) Reset() {
	p.filters = [2][phaserMaxStages]AllPassFilter{}
	p.fbState = [2]float32{}
	p.lfo.Reset()
}
