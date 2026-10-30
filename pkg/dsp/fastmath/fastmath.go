// Package fastmath provides faster versions of math functions that audio
// code evaluates per sample, accurate well beyond 32-bit audio.
package fastmath

import "math"

// Sin2Pi returns sin(2π·x) for any finite x, within 1e-13 of math.Sin: x
// is reduced to a quarter period and a degree-17 polynomial (the Taylor
// series, whose next term bounds the error) evaluates it. It is several
// times faster than math.Sin, for oscillators and LFOs whose phase is in
// cycles.
func Sin2Pi(x float64) float64 {
	r := x - math.Round(x) // [-0.5, 0.5]
	// sin(π - θ) = sin θ folds the outer quarters in.
	if r > 0.25 {
		r = 0.5 - r
	} else if r < -0.25 {
		r = -0.5 - r
	}
	y := 2 * math.Pi * r // [-π/2, π/2]
	y2 := y * y
	return y * (1 + y2*(-1.0/6+y2*(1.0/120+y2*(-1.0/5040+y2*(1.0/362880+
		y2*(-1.0/39916800+y2*(1.0/6227020800+y2*(-1.0/1307674368000+y2*(1.0/355687428096000)))))))))
}

// Cos2Pi returns cos(2π·x), as Sin2Pi(x + 1/4).
func Cos2Pi(x float64) float64 { return Sin2Pi(x + 0.25) }
