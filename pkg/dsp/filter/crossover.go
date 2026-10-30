package filter

import (
	"fmt"
	"math"
)

// Crossover splits a signal into bands with 4th-order Linkwitz-Riley
// filters (24 dB per octave, -6 dB at each crossover frequency). Every band
// is passed through the all-pass responses of the crossovers it does not
// use, so the bands are phase aligned and sum to an all-pass: added back
// together they reproduce the input's magnitude exactly, for multiband
// dynamics and saturation.
//
// Coefficients are computed in float64 and the state is per channel;
// Process and SetFrequency do not allocate.
type Crossover struct {
	sampleRate float64
	freqs      []float64
	lp, hp, ap []sosCoef // per crossover
	// lpState[k][ch] and hpState[k][ch] are two sections each;
	// apState[b][j][ch] is the all-pass of crossover j on band b (j > b).
	lpState [][][2]sosState
	hpState [][][2]sosState
	apState [][][]sosState
}

type sosCoef struct{ b0, b1, b2, a1, a2 float64 }

type sosState struct{ z1, z2 float64 }

func (c *sosCoef) process(s *sosState, x float64) float64 {
	y := c.b0*x + s.z1
	s.z1 = c.b1*x - c.a1*y + s.z2
	s.z2 = c.b2*x - c.a2*y
	return y
}

// NewCrossover returns a crossover for channels channels splitting at the
// given frequencies (ascending, below Nyquist) into len(freqs)+1 bands.
func NewCrossover(sampleRate float64, channels int, freqs ...float64) (*Crossover, error) {
	if len(freqs) == 0 {
		return nil, fmt.Errorf("filter: crossover needs at least one frequency")
	}
	if channels < 1 {
		return nil, fmt.Errorf("filter: crossover for %d channels", channels)
	}
	for i, f := range freqs {
		if !(f > 0 && f < sampleRate/2) || i > 0 && f <= freqs[i-1] {
			return nil, fmt.Errorf("filter: crossover frequencies %v must ascend between 0 and %v Hz", freqs, sampleRate/2)
		}
	}
	n := len(freqs)
	c := &Crossover{
		sampleRate: sampleRate,
		freqs:      append([]float64(nil), freqs...),
		lp:         make([]sosCoef, n),
		hp:         make([]sosCoef, n),
		ap:         make([]sosCoef, n),
		lpState:    make([][][2]sosState, n),
		hpState:    make([][][2]sosState, n),
		apState:    make([][][]sosState, n),
	}
	for k := range n {
		c.lpState[k] = make([][2]sosState, channels)
		c.hpState[k] = make([][2]sosState, channels)
		c.apState[k] = make([][]sosState, n)
		for j := k + 1; j < n; j++ {
			c.apState[k][j] = make([]sosState, channels)
		}
		c.design(k)
	}
	return c, nil
}

// design computes the coefficients of crossover k: Butterworth low- and
// high-pass sections (each used twice for Linkwitz-Riley) and the
// all-pass their sum equals, all from the same prewarped bilinear
// transform so the identity LP² + HP² = AP holds exactly.
func (c *Crossover) design(k int) {
	w := 2 * math.Pi * c.freqs[k] / c.sampleRate
	cosw, alpha := math.Cos(w), math.Sin(w)/(2*math.Sqrt2/2)
	a0 := 1 + alpha
	a1, a2 := -2*cosw/a0, (1-alpha)/a0
	c.lp[k] = sosCoef{b0: (1 - cosw) / 2 / a0, b1: (1 - cosw) / a0, b2: (1 - cosw) / 2 / a0, a1: a1, a2: a2}
	c.hp[k] = sosCoef{b0: (1 + cosw) / 2 / a0, b1: -(1 + cosw) / a0, b2: (1 + cosw) / 2 / a0, a1: a1, a2: a2}
	c.ap[k] = sosCoef{b0: a2, b1: a1, b2: 1, a1: a1, a2: a2}
}

// Bands returns the number of bands.
func (c *Crossover) Bands() int { return len(c.freqs) + 1 }

// Frequency returns crossover frequency i.
func (c *Crossover) Frequency(i int) float64 { return c.freqs[i] }

// SetFrequency moves crossover i, kept between its neighbours (a tenth of
// an octave apart) and below Nyquist. The filter state is kept, so it can
// be swept while processing.
func (c *Crossover) SetFrequency(i int, hz float64) {
	lo, hi := 10.0, c.sampleRate*0.49
	if i > 0 {
		lo = c.freqs[i-1] * math.Pow(2, 0.1)
	}
	if i < len(c.freqs)-1 {
		hi = c.freqs[i+1] / math.Pow(2, 0.1)
	}
	hz = max(lo, min(hi, hz))
	if hz != c.freqs[i] {
		c.freqs[i] = hz
		c.design(i)
	}
}

// Process splits in (channel ch) into bands: bands[b] receives band b,
// lowest first, and must hold len(in) samples. in may alias a band.
func (c *Crossover) Process(in []float32, bands [][]float32, ch int) {
	n := len(c.freqs)
	rest := bands[n][:len(in)]
	copy(rest, in)
	for k := range n {
		band := bands[k][:len(in)]
		lp, hp := &c.lp[k], &c.hp[k]
		ls, hs := &c.lpState[k][ch], &c.hpState[k][ch]
		for i, x := range rest {
			v := float64(x)
			band[i] = float32(lp.process(&ls[1], lp.process(&ls[0], v)))
			rest[i] = float32(hp.process(&hs[1], hp.process(&hs[0], v)))
		}
		for j := k + 1; j < n; j++ {
			ap, as := &c.ap[j], &c.apState[k][j][ch]
			for i, x := range band {
				band[i] = float32(ap.process(as, float64(x)))
			}
		}
	}
}

// Reset clears the filter state.
func (c *Crossover) Reset() {
	for k := range c.freqs {
		clear(c.lpState[k])
		clear(c.hpState[k])
		for j := range c.apState[k] {
			clear(c.apState[k][j])
		}
	}
}
