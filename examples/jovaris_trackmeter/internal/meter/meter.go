// Package meter implements measurement only. It never owns or modifies audio.
package meter

import "math"

const FloorDB = -120.0

// Channel is owned exclusively by the audio thread.
type Channel struct {
	b0, b1, b2, a1, a2          float64
	z1, z2, vu, peak, hold      float64
	peakDecay                   float64
	clipDuration, clipRemaining int
	clipped                     bool
}

type Reading struct {
	PeakDB, HoldDB, VU float64
	Clip, RecordedClip bool
}

func New(sampleRate float64) Channel {
	if sampleRate <= 0 || math.IsNaN(sampleRate) || math.IsInf(sampleRate, 0) {
		sampleRate = 48000
	}
	// Average-rectified, sine-calibrated second-order pointer model.
	// Damping .82 gives about 1.1% step overshoot; first maximum ~300 ms.
	// This is a trial model, not a claim of IEC/ANSI VU conformance.
	const damping = .82
	omega := math.Pi / (.3 * math.Sqrt(1-damping*damping))
	k := math.Tan(omega / (2 * sampleRate))
	norm := 1 / (1 + 2*damping*k + k*k)
	b0 := k * k * norm
	return Channel{
		b0: b0, b1: 2 * b0, b2: b0,
		a1: 2 * (k*k - 1) * norm, a2: (1 - 2*damping*k + k*k) * norm,
		peakDecay:    math.Pow(10, -20/(20*sampleRate)),
		clipDuration: int(sampleRate),
	}
}

func (c *Channel) Sample(x float64) {
	// A nonfinite sample does not poison the meter. Audio still passes unchanged.
	if math.IsNaN(x) || math.IsInf(x, 0) {
		x = 0
	}
	x = math.Abs(x)
	c.peak = math.Max(x, c.peak*c.peakDecay)
	c.hold = math.Max(x, c.hold)
	if c.clipRemaining > 0 {
		c.clipRemaining--
	}
	if x >= 1 {
		c.clipped = true
		c.clipRemaining = c.clipDuration
	}
	// pi/(2*sqrt(2)) converts sine mean-absolute amplitude to RMS equivalent.
	// -18 dBFS RMS sine is 0 VU, NOT a -18 dBFS-peak sine.
	x *= math.Pi / (2 * math.Sqrt2)
	y := c.b0*x + c.z1
	c.z1 = c.b1*x - c.a1*y + c.z2
	c.z2 = c.b2*x - c.a2*y
	c.vu = math.Max(0, y)
	if x == 0 && math.Abs(c.z1)+math.Abs(c.z2) < 1e-20 {
		c.z1 = 0
		c.z2 = 0
		c.vu = 0
	}
}

// ResetPeak clears only peak/clip history and leaves VU dynamics untouched.
func (c *Channel) ResetPeak() {
	c.peak = 0
	c.hold = 0
	c.clipped = false
	c.clipRemaining = 0
}

func DB(x float64) float64 {
	if x <= 1e-6 {
		return FloorDB
	}
	return 20 * math.Log10(x)
}

func (c *Channel) Reading() Reading {
	return Reading{PeakDB: DB(c.peak), HoldDB: DB(c.hold), VU: DB(c.vu) + 18,
		Clip: c.clipRemaining > 0, RecordedClip: c.clipped}
}
