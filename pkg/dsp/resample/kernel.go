package resample

import (
	"math"
	"sync"
)

const kernelPhases = 512

// Kernel is a tabulated Kaiser-windowed sinc for reading a buffer at
// fractional positions: the weight of the sample at offset t from the
// read position is Weight(t) for |t| < Zeros. For reads that step through
// the buffer faster than one sample per output (pitching a sample up),
// scale offsets and weights by cutoff c = 1/step: weight c·Weight(c·t)
// over |t| < Zeros/c band-limits the read so it does not alias.
//
// The table is built once and shared; Weight does not allocate and is
// safe for concurrent use.
type Kernel struct {
	zeros int
	// pairs holds, for each table position j, the kernel there and the
	// difference to the next position, for linear interpolation with one
	// multiply-add from adjacent memory.
	pairs []float64
	// rows holds the same pairs phase-major: row ph lists positions ph,
	// ph+phases, ph+2·phases, ..., the taps of one read at full
	// bandwidth, next to each other.
	rows []float64
}

// SincKernel returns the shared high-quality kernel: 24 zero crossings on
// each side (48 taps at full bandwidth), about 80 dB stopband, flat to
// about 0.9 of the cutoff with the transition band centred on it. Content
// in the transition band can fold back, but only into the top tenth of
// the output band.
var SincKernel = sync.OnceValue(func() *Kernel { return newKernel(24, 8) })

// ShortSincKernel returns the shared fast kernel: 8 zero crossings on each
// side (16 taps at full bandwidth, a third of SincKernel's work), flat
// (within 0.1 dB) to about 0.75 of the cutoff and 80 dB down from about
// 1.3 times it. Content between 1 and 1.3 times the cutoff can fold back
// into the top quarter of the output band.
var ShortSincKernel = sync.OnceValue(func() *Kernel { return newKernel(8, 8) })

func newKernel(zeros int, beta float64) *Kernel {
	table := make([]float64, zeros*kernelPhases+2)
	for i := range table {
		t := float64(i) / kernelPhases
		table[i] = sinc(t) * kaiser(t/float64(zeros), beta)
	}
	k := &Kernel{zeros: zeros, pairs: make([]float64, 2*(len(table)-1)), rows: make([]float64, 2*zeros*kernelPhases)}
	for j := range len(table) - 1 {
		k.pairs[2*j], k.pairs[2*j+1] = table[j], table[j+1]-table[j]
	}
	for ph := range kernelPhases {
		for m := range zeros {
			r := 2 * (ph*zeros + m)
			copy(k.rows[r:r+2], k.pairs[2*(m*kernelPhases+ph):])
		}
	}
	return k
}

// Zeros returns how many zero crossings the kernel spans on each side.
func (k *Kernel) Zeros() int { return k.zeros }

// Weight returns the kernel at offset t (0 outside ±Zeros).
func (k *Kernel) Weight(t float64) float64 {
	p := math.Abs(t) * kernelPhases
	i := int(p)
	if i >= k.zeros*kernelPhases {
		return 0
	}
	return k.pairs[2*i] + (p-float64(i))*k.pairs[2*i+1]
}

// fixedOne is 1 in the 32.32 fixed point Interpolate walks the table in.
const fixedOne = 1 << 32

// Interpolate reads x0 (and x1, unless nil, with the same weights) at the
// fractional position p with cutoff c: Σ c·Weight(c·(p-i))·x[i] over the
// taps i within Zeros/c of p, which must all be inside the slices. It
// walks the table with a fixed stride, in fixed point, instead of calling
// Weight per tap.
func (k *Kernel) Interpolate(x0, x1 []float32, p, c float64) (y0, y1 float64) {
	limit := int64(k.zeros) * kernelPhases * fixedOne
	step := int64(c * kernelPhases * fixedOne)
	center := int(math.Floor(p))
	frac := p - float64(center)
	// Taps at and before p, walking back, then after it: the table offset
	// starts at the tap's distance from p and grows by step.
	left := int64(frac * c * kernelPhases * fixedOne)
	right := int64((1 - frac) * c * kernelPhases * fixedOne)
	if x1 == nil {
		a0, b0 := k.side(x0, nil, center, -1, left, step, limit)
		a1, b1 := k.side(x0, nil, center+1, 1, right, step, limit)
		_, _ = b0, b1
		return (a0 + a1) * c, 0
	}
	a0, b0 := k.side(x0, x1, center, -1, left, step, limit)
	a1, b1 := k.side(x0, x1, center+1, 1, right, step, limit)
	return (a0 + a1) * c, (b0 + b1) * c
}

// side sums the taps from x[i] on, moving by dir, at table offsets pos,
// pos+step, ... below limit. Two taps per iteration go into separate sums,
// so the additions do not wait on each other.
func (k *Kernel) side(x0, x1 []float32, i, dir int, pos, step, limit int64) (y0, y1 float64) {
	pairs := k.pairs
	var s0, s1, t0, t1 float64
	if step == kernelPhases*fixedOne {
		// Full bandwidth: the taps share a fraction and a phase, so they
		// are one row, from tap m0 (pos is below two phases' worth).
		f := float64(uint32(pos)) * (1.0 / fixedOne)
		whole := int(pos >> 32)
		ph, m0 := whole%kernelPhases, whole/kernelPhases
		row := k.rows[2*(ph*k.zeros+m0) : 2*(ph+1)*k.zeros]
		if x1 == nil {
			for ; len(row) >= 4; i, row = i+2*dir, row[4:] {
				s0 += (row[0] + f*row[1]) * float64(x0[i])
				s1 += (row[2] + f*row[3]) * float64(x0[i+dir])
			}
			if len(row) >= 2 {
				s0 += (row[0] + f*row[1]) * float64(x0[i])
			}
			return s0 + s1, 0
		}
		for ; len(row) >= 4; i, row = i+2*dir, row[4:] {
			w, v := row[0]+f*row[1], row[2]+f*row[3]
			s0 += w * float64(x0[i])
			t0 += w * float64(x1[i])
			s1 += v * float64(x0[i+dir])
			t1 += v * float64(x1[i+dir])
		}
		if len(row) >= 2 {
			w := row[0] + f*row[1]
			s0 += w * float64(x0[i])
			t0 += w * float64(x1[i])
		}
		return s0 + s1, t0 + t1
	}
	if x1 == nil {
		for ; pos+step < limit; i, pos = i+2*dir, pos+2*step {
			j, q := 2*int(pos>>32), 2*int((pos+step)>>32)
			w := pairs[j] + float64(uint32(pos))*(1.0/fixedOne)*pairs[j+1]
			v := pairs[q] + float64(uint32(pos+step))*(1.0/fixedOne)*pairs[q+1]
			s0 += w * float64(x0[i])
			s1 += v * float64(x0[i+dir])
		}
		if pos < limit {
			j := 2 * int(pos>>32)
			s0 += (pairs[j] + float64(uint32(pos))*(1.0/fixedOne)*pairs[j+1]) * float64(x0[i])
		}
		return s0 + s1, 0
	}
	for ; pos+step < limit; i, pos = i+2*dir, pos+2*step {
		j, q := 2*int(pos>>32), 2*int((pos+step)>>32)
		w := pairs[j] + float64(uint32(pos))*(1.0/fixedOne)*pairs[j+1]
		v := pairs[q] + float64(uint32(pos+step))*(1.0/fixedOne)*pairs[q+1]
		s0 += w * float64(x0[i])
		t0 += w * float64(x1[i])
		s1 += v * float64(x0[i+dir])
		t1 += v * float64(x1[i+dir])
	}
	if pos < limit {
		j := 2 * int(pos>>32)
		w := pairs[j] + float64(uint32(pos))*(1.0/fixedOne)*pairs[j+1]
		s0 += w * float64(x0[i])
		t0 += w * float64(x1[i])
	}
	return s0 + s1, t0 + t1
}
