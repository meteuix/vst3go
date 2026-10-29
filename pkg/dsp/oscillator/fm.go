package oscillator

import "math"

// Operator is a sine oscillator for phase-modulation (DX-style FM)
// synthesis: its frequency is a ratio of the note frequency plus a fixed
// detune, and Next takes the phase modulation from other operators in
// radians. Feedback feeds the operator's own output (averaged over two
// samples, as in the DX7, to tame it) back into its phase. Connect
// operators into any algorithm by passing outputs as modulation.
type Operator struct {
	sampleRate float64
	ratio      float64
	detune     float64 // Hz
	feedback   float64 // radians per unit output
	phase      float64 // cycles
	prev       [2]float64
}

// NewOperator returns an operator at ratio 1 with no detune or feedback.
func NewOperator(sampleRate float64) *Operator {
	return &Operator{sampleRate: sampleRate, ratio: 1}
}

// SetRatio sets the frequency as a multiple of the note frequency.
func (o *Operator) SetRatio(ratio float64) { o.ratio = math.Max(0, ratio) }

// SetDetune adds a fixed offset in Hz.
func (o *Operator) SetDetune(hz float64) { o.detune = hz }

// SetFeedback sets self-modulation; 0 is a sine, about 1.5 approaches a
// sawtooth.
func (o *Operator) SetFeedback(amount float64) { o.feedback = math.Max(0, amount) }

// Next returns the next output (-1..1) for a note at noteHz, phase
// modulated by mod radians.
func (o *Operator) Next(noteHz, mod float64) float64 {
	fb := o.feedback * 0.5 * (o.prev[0] + o.prev[1])
	y := math.Sin(2*math.Pi*o.phase + mod + fb)
	o.prev[1], o.prev[0] = o.prev[0], y
	o.phase += (noteHz*o.ratio + o.detune) / o.sampleRate
	o.phase -= math.Floor(o.phase)
	return y
}

// Reset restarts the phase and clears the feedback.
func (o *Operator) Reset() {
	o.phase = 0
	o.prev = [2]float64{}
}
