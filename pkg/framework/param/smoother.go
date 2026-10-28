package param

import "math"

// SmoothingMode selects how a Ramp moves toward its target.
type SmoothingMode uint8

const (
	// SmoothLinear moves in equal steps; use it for gains, mix and pan.
	SmoothLinear SmoothingMode = iota
	// SmoothMultiplicative moves in equal ratios, which sounds even for
	// frequencies and times. Values must be positive; a ramp to or from
	// zero jumps instead.
	SmoothMultiplicative
)

// Ramp moves a value to a target over a fixed time, one sample per Next,
// to avoid zipper noise when parameters change. A new target restarts the
// ramp from the current value, so every change takes the same time.
//
// A zero Ramp has no ramp time and jumps to each target. Ramp is not safe for
// concurrent use; it belongs to the audio thread.
type Ramp struct {
	mode      SmoothingMode
	length    int
	remaining int
	current   float64
	target    float64
	step      float64
}

// NewRamp returns a ramp of the given mode that reaches each new target
// after seconds at sampleRate, starting at value.
func NewRamp(mode SmoothingMode, sampleRate, seconds, value float64) Ramp {
	r := Ramp{mode: mode}
	r.Reset(sampleRate, seconds)
	r.SetCurrentAndTarget(value)
	return r
}

// Reset sets the ramp time and stops a running ramp at its target.
func (r *Ramp) Reset(sampleRate, seconds float64) {
	r.length = int(math.Floor(sampleRate * seconds))
	if r.length < 0 {
		r.length = 0
	}
	r.SetCurrentAndTarget(r.target)
}

// SetCurrentAndTarget jumps to value without ramping.
func (r *Ramp) SetCurrentAndTarget(value float64) {
	r.current, r.target, r.remaining = value, value, 0
}

// SetTarget starts a ramp from the current value to target.
func (r *Ramp) SetTarget(target float64) {
	if target == r.target {
		return
	}
	r.target = target
	if r.length == 0 || (r.mode == SmoothMultiplicative && (target <= 0 || r.current <= 0)) {
		r.SetCurrentAndTarget(target)
		return
	}
	r.remaining = r.length
	if r.mode == SmoothMultiplicative {
		r.step = math.Exp(math.Log(target/r.current) / float64(r.length))
	} else {
		r.step = (target - r.current) / float64(r.length)
	}
}

// Next advances one sample and returns the new value.
func (r *Ramp) Next() float64 {
	if r.remaining == 0 {
		return r.target
	}
	r.remaining--
	switch {
	case r.remaining == 0:
		r.current = r.target
	case r.mode == SmoothMultiplicative:
		r.current *= r.step
	default:
		r.current += r.step
	}
	return r.current
}

// Skip advances n samples and returns the value reached.
func (r *Ramp) Skip(n int) float64 {
	if n >= r.remaining {
		r.SetCurrentAndTarget(r.target)
		return r.target
	}
	if n <= 0 {
		return r.current
	}
	r.remaining -= n
	if r.mode == SmoothMultiplicative {
		r.current *= math.Pow(r.step, float64(n))
	} else {
		r.current += r.step * float64(n)
	}
	return r.current
}

// Fill writes the next len(dst) values to dst.
func (r *Ramp) Fill(dst []float32) {
	if r.remaining == 0 {
		v := float32(r.target)
		for i := range dst {
			dst[i] = v
		}
		return
	}
	for i := range dst {
		dst[i] = float32(r.Next())
	}
}

// Fill64 writes the next len(dst) values to dst.
func (r *Ramp) Fill64(dst []float64) {
	if r.remaining == 0 {
		for i := range dst {
			dst[i] = r.target
		}
		return
	}
	for i := range dst {
		dst[i] = r.Next()
	}
}

// Current returns the value at the current sample.
func (r *Ramp) Current() float64 { return r.current }

// Target returns the value the ramp is moving to.
func (r *Ramp) Target() float64 { return r.target }

// IsSmoothing reports whether the ramp has not reached its target yet.
func (r *Ramp) IsSmoothing() bool { return r.remaining > 0 }
