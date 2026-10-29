// Package resample changes sample rates: Oversampler runs a processor at 2,
// 4, 8 or 16 times the host rate with linear-phase half-band filters, and
// Resampler converts a stream between two arbitrary rates with a windowed
// sinc filter. Both allocate in their constructors only.
package resample

import "math"

// besselI0 is the zeroth-order modified Bessel function of the first kind.
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < sum*1e-17 {
			break
		}
	}
	return sum
}

// kaiser returns the Kaiser window at t in [-1, 1].
func kaiser(t, beta float64) float64 {
	if t <= -1 || t >= 1 {
		return 0
	}
	return besselI0(beta*math.Sqrt(1-t*t)) / besselI0(beta)
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}
