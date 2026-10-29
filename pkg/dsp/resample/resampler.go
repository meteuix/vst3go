package resample

import (
	"fmt"
	"math"
)

const (
	sincZeros  = 64  // zero crossings on each side of the filter centre
	sincPhases = 512 // filter table resolution per input sample
	sincBeta   = 10.0
	// Fraction of the lower Nyquist frequency the filter passes; the rest
	// is the transition band.
	sincPassband = 0.90
)

// Resampler converts a mono stream from one sample rate to another with a
// Kaiser-windowed sinc filter: 128 taps, about 100 dB stopband from the
// lower Nyquist frequency, flat to 0.9 of it (19.8 kHz when one rate is
// 44.1 kHz).
//
// Output sample k lies at input time k·inRate/outRate, so a stream is
// time-aligned; producing it needs input up to Latency samples ahead.
// Process does not allocate.
type Resampler struct {
	step   float64 // input samples per output sample
	table  []float64
	window []float64 // last 2·sincZeros inputs, stored twice
	write  int
	frac   float64
	need   int // inputs to read before the next output
}

// NewResampler returns a Resampler from inRate to outRate.
func NewResampler(inRate, outRate float64) (*Resampler, error) {
	if !(inRate > 0) || !(outRate > 0) {
		return nil, fmt.Errorf("resample: rates %v and %v must be positive", inRate, outRate)
	}
	r := &Resampler{
		step:   inRate / outRate,
		table:  make([]float64, 2*sincZeros*sincPhases+2),
		window: make([]float64, 4*sincZeros),
	}
	// h(t) for t in [-sincZeros, sincZeros], cut off below the lower of
	// the two Nyquist frequencies (in units of the input rate).
	cutoff := (1 + sincPassband) / 2 * min(1, 1/r.step) // centre of the transition
	for i := range r.table {
		t := float64(i)/sincPhases - sincZeros
		r.table[i] = cutoff * sinc(cutoff*t) * kaiser(t/sincZeros, sincBeta)
	}
	r.Reset()
	return r, nil
}

// Latency returns how many input samples past an output's time Process
// needs before it can produce that output.
func (r *Resampler) Latency() int { return sincZeros }

// Ratio returns output samples per input sample.
func (r *Resampler) Ratio() float64 { return 1 / r.step }

// Process reads from in and writes to out until one runs out, and returns
// how many samples it consumed and produced. Unconsumed input must be
// passed again.
func (r *Resampler) Process(in, out []float32) (consumed, produced int) {
	taps := 2 * sincZeros
	for produced < len(out) {
		for r.need > 0 {
			if consumed == len(in) {
				return consumed, produced
			}
			x := float64(in[consumed])
			r.window[r.write], r.window[r.write+taps] = x, x
			r.write = (r.write + 1) % taps
			consumed++
			r.need--
		}
		// The window holds x[n-taps+1..n]; the output lies at n-sincZeros+frac.
		win := r.window[r.write : r.write+taps]
		pos := (float64(taps-1) + r.frac) * sincPhases
		y := 0.0
		for j, x := range win {
			p := pos - float64(j*sincPhases)
			i := int(p)
			f := p - float64(i)
			y += x * (r.table[i] + f*(r.table[i+1]-r.table[i]))
		}
		out[produced] = float32(y)
		produced++
		r.frac += r.step
		adv := math.Floor(r.frac)
		r.frac -= adv
		r.need = int(adv)
	}
	return consumed, produced
}

// Reset clears the stream; the next output lies at the next input.
func (r *Resampler) Reset() {
	clear(r.window)
	r.write, r.frac = 0, 0
	r.need = sincZeros + 1
}

// Convert resamples a whole signal from inRate to outRate and returns
// round(len(in)·outRate/inRate) samples aligned with the input. It
// allocates and is meant for loading files, not for the audio thread.
func Convert(in []float32, inRate, outRate float64) ([]float32, error) {
	r, err := NewResampler(inRate, outRate)
	if err != nil {
		return nil, err
	}
	out := make([]float32, int(math.Round(float64(len(in))*outRate/inRate)))
	_, produced := r.Process(in, out)
	tail := make([]float32, sincZeros+1)
	for produced < len(out) {
		_, n := r.Process(tail, out[produced:])
		produced += n
	}
	return out, nil
}
