// Package convolution provides zero-latency FIR convolution with long
// impulse responses, for convolution reverbs, cabinet simulation and
// linear-phase filters.
package convolution

import (
	"fmt"

	"github.com/th-release/vst3go/v2/pkg/dsp/fft"
)

// stageRatio is how much larger each FFT stage's block is than the one
// before; maxStages bounds the stages after the first.
const (
	stageRatio = 8
	maxStages  = 3
)

// Convolver convolves a mono stream with an impulse response without
// latency, using non-uniformly partitioned convolution. The first block of
// the response is applied directly in the time domain. The rest is split
// into FFT stages of growing block sizes (the block size, 8× and 64× it),
// each a uniformly partitioned overlap-save convolution of its part of the
// response: short blocks where the response starts, so the result is
// ready in time, and long ones (fewer partitions, cheaper per sample) for
// the tail. A long stage's part starts two of its blocks into the
// response, which leaves a block's time to compute it: the work (FFTs and
// partition products) is spread over that time, a share at every block
// boundary, so the cost per call stays flat.
//
// The block size trades the direct part (block-size multiplies per sample)
// against the FFT stages; 32 to 128 suits most responses. New allocates
// everything; Process does not allocate. Build a new Convolver off the
// audio thread to change the response, and swap it in.
type Convolver struct {
	block  int
	head   []float64 // first block of the response, reversed
	hist   []float64 // last block inputs, stored twice for a contiguous window
	write  int
	input  []float64 // input history for the stages, a power-of-two ring
	mask   int
	n      int // samples processed, modulo the ring (a multiple of every stage block)
	stages []*stage
}

// stage is a uniformly partitioned overlap-save convolution of the response
// from offset with blocks of size block. Its result for the input block
// ending at time T plays from T+offset-block: at once for the first stage
// (offset = block), one block later for the others (offset = 2·block),
// which compute it in slices over that block.
type stage struct {
	block, slices int
	fft           *fft.Real
	partitions    [][]complex128 // spectra of the response blocks
	inputs        [][]complex128 // input spectra, newest at newest
	newest        int
	window        []float64 // 2·block: previous and current input block
	acc           []complex128
	time          []float64
	out, next     []float64 // block playing, block being computed
	pos           int       // in out
}

// New returns a Convolver for ir with the given block size, a power of two
// of at least 8.
func New(ir []float32, blockSize int) (*Convolver, error) {
	if blockSize < 8 || blockSize&(blockSize-1) != 0 {
		return nil, fmt.Errorf("convolution: block size %d is not a power of two of at least 8", blockSize)
	}
	b := blockSize
	c := &Convolver{block: b, head: make([]float64, b), hist: make([]float64, 2*b)}
	for i := 0; i < b && i < len(ir); i++ {
		c.head[b-1-i] = float64(ir[i])
	}
	// Stage k has blocks of b·8^k and starts at 2·b·8^k (the first at b);
	// each covers the response up to where the next starts, the last to the
	// end.
	largest := b
	for k, start := 0, b; start < len(ir) && k < maxStages; k++ {
		size := b
		for range k {
			size *= stageRatio
		}
		end := len(ir)
		if k+1 < maxStages {
			end = min(end, 2*size*stageRatio)
		}
		st, err := newStage(ir[start:end], size, size/b)
		if err != nil {
			return nil, err
		}
		c.stages = append(c.stages, st)
		largest = size
		start = end
	}
	ring := 1
	for ring < 2*largest {
		ring <<= 1
	}
	c.input, c.mask = make([]float64, ring), ring-1
	return c, nil
}

func newStage(ir []float32, block, slices int) (*stage, error) {
	f, err := fft.NewReal(2 * block)
	if err != nil {
		return nil, err
	}
	s := &stage{
		block:  block,
		slices: slices,
		fft:    f,
		window: make([]float64, 2*block),
		acc:    make([]complex128, block+1),
		time:   make([]float64, 2*block),
		out:    make([]float64, block),
		next:   make([]float64, block),
	}
	for start := 0; start < len(ir); start += block {
		clear(s.time)
		for i, x := range ir[start:min(start+block, len(ir))] {
			s.time[i] = float64(x)
		}
		spectrum := make([]complex128, block+1)
		f.Forward(s.time, spectrum)
		s.partitions = append(s.partitions, spectrum)
		s.inputs = append(s.inputs, make([]complex128, block+1))
	}
	return s, nil
}

// BlockSize returns the direct part's length and the first stage's block.
func (c *Convolver) BlockSize() int { return c.block }

// Latency is always 0.
func (c *Convolver) Latency() int { return 0 }

// Process convolves in into out (same length); in and out may be the same
// slice.
func (c *Convolver) Process(in, out []float32) {
	b := c.block
	head := c.head[:b]
	for i, x := range in {
		v := float64(x)
		c.hist[c.write], c.hist[c.write+b] = v, v
		c.write++
		if c.write == b {
			c.write = 0
		}
		c.input[c.n&c.mask] = v
		y := dot(head, c.hist[c.write:c.write+b])
		for _, st := range c.stages {
			y += st.out[st.pos]
			st.pos++
		}
		out[i] = float32(y)
		c.n = (c.n + 1) & c.mask
		if c.n%b == 0 {
			c.boundary()
		}
	}
}

// dot returns Σ a[i]·b[i] with four running sums (no chain of dependent
// additions).
func dot(a, b []float64) float64 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// boundary runs at every multiple of the block size: each stage takes the
// slice of work due now.
func (c *Convolver) boundary() {
	for _, st := range c.stages {
		phase := (c.n / c.block) % st.slices
		if phase == 0 {
			// A stage block ended: start on it.
			st.begin(c.input, c.mask, c.n)
		}
		st.slice(phase)
	}
}

// begin takes the input block that ended at sample count n (modulo the
// ring).
func (s *stage) begin(input []float64, mask, n int) {
	w := s.window
	for i := range w {
		w[i] = input[(n-len(w)+i)&mask]
	}
	s.newest++
	if s.newest == len(s.inputs) {
		s.newest = 0
	}
	s.fft.Forward(w, s.inputs[s.newest])
	if s.slices > 1 {
		// The block computed over the last one plays now.
		s.out, s.next = s.next, s.out
		s.pos = 0
	}
}

// slice multiplies its share of the bins of every partition with the
// matching input spectra; the last slice transforms the sum back.
func (s *stage) slice(phase int) {
	bins := len(s.acc)
	lo, hi := phase*bins/s.slices, (phase+1)*bins/s.slices
	acc := s.acc[lo:hi]
	clear(acc)
	idx := s.newest
	for _, h := range s.partitions {
		mac(acc, s.inputs[idx][lo:hi], h[lo:hi])
		idx--
		if idx < 0 {
			idx = len(s.inputs) - 1
		}
	}
	if phase < s.slices-1 {
		return
	}
	s.fft.Inverse(s.acc, s.time)
	if s.slices == 1 {
		copy(s.out, s.time[s.block:])
		s.pos = 0
	} else {
		copy(s.next, s.time[s.block:])
	}
}

// mac adds x[k]·h[k] to acc[k], written out in real arithmetic, which the
// compiler turns into fused multiply-adds.
func mac(acc, x, h []complex128) {
	x, h = x[:len(acc)], h[:len(acc)]
	for k := range acc {
		xr, xi, hr, hi := real(x[k]), imag(x[k]), real(h[k]), imag(h[k])
		acc[k] = complex(real(acc[k])+xr*hr-xi*hi, imag(acc[k])+xr*hi+xi*hr)
	}
}

// Reset clears the stream.
func (c *Convolver) Reset() {
	clear(c.hist)
	clear(c.input)
	for _, st := range c.stages {
		clear(st.out)
		clear(st.next)
		for _, x := range st.inputs {
			clear(x)
		}
		st.pos = 0
	}
	c.write, c.n = 0, 0
}
