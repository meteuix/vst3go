// Package convolution provides zero-latency FIR convolution with long
// impulse responses, for convolution reverbs, cabinet simulation and
// linear-phase filters.
package convolution

import (
	"fmt"

	"github.com/th-release/vst3go/pkg/dsp/fft"
)

// Convolver convolves a mono stream with an impulse response without
// latency. The first block of the response is applied directly in the
// time domain; the rest is split into equal partitions applied with FFTs
// (uniformly partitioned overlap-save), one block ahead of when it is
// needed.
//
// The block size trades the direct part (block-size multiplies per sample)
// against the FFT part (one transform pair per block plus one complex
// multiply per bin and partition); 64 to 256 suits most responses. New
// allocates everything; Process does not allocate. Build a new Convolver
// off the audio thread to change the response, and swap it in.
type Convolver struct {
	block int
	head  []float64 // first block of the response, reversed
	hist  []float64 // last block inputs, stored twice for a contiguous window
	write int

	fft        *fft.Real
	partitions [][]complex128 // spectra of the remaining blocks
	inputs     [][]complex128 // input spectra, newest at newest
	newest     int
	window     []float64 // previous and current input block
	acc        []complex128
	time       []float64
	tail       []float64 // FFT part of the current output block
	pos        int
}

// New returns a Convolver for ir with the given block size, a power of two
// of at least 8.
func New(ir []float32, blockSize int) (*Convolver, error) {
	if blockSize < 8 || blockSize&(blockSize-1) != 0 {
		return nil, fmt.Errorf("convolution: block size %d is not a power of two of at least 8", blockSize)
	}
	f, err := fft.NewReal(2 * blockSize)
	if err != nil {
		return nil, err
	}
	b := blockSize
	c := &Convolver{
		block:  b,
		head:   make([]float64, b),
		hist:   make([]float64, 2*b),
		fft:    f,
		window: make([]float64, 2*b),
		acc:    make([]complex128, b+1),
		time:   make([]float64, 2*b),
		tail:   make([]float64, b),
	}
	for i := 0; i < b && i < len(ir); i++ {
		c.head[b-1-i] = float64(ir[i])
	}
	for start := b; start < len(ir); start += b {
		clear(c.time)
		for i, x := range ir[start:min(start+b, len(ir))] {
			c.time[i] = float64(x)
		}
		spectrum := make([]complex128, b+1)
		f.Forward(c.time, spectrum)
		c.partitions = append(c.partitions, spectrum)
		c.inputs = append(c.inputs, make([]complex128, b+1))
	}
	return c, nil
}

// BlockSize returns the partition size.
func (c *Convolver) BlockSize() int { return c.block }

// Latency is always 0.
func (c *Convolver) Latency() int { return 0 }

// Process convolves in into out (same length); in and out may be the same
// slice.
func (c *Convolver) Process(in, out []float32) {
	b := c.block
	for i, x := range in {
		v := float64(x)
		c.hist[c.write], c.hist[c.write+b] = v, v
		c.write = (c.write + 1) % b
		y := c.tail[c.pos]
		for j, h := range c.head {
			y += h * c.hist[c.write+j]
		}
		out[i] = float32(y)
		c.window[b+c.pos] = v
		c.pos++
		if c.pos == b {
			c.pos = 0
			c.advance()
		}
	}
}

// advance takes the block just completed and computes the FFT part of the
// next output block.
func (c *Convolver) advance() {
	if len(c.partitions) == 0 {
		return
	}
	b := c.block
	c.newest = (c.newest + 1) % len(c.inputs)
	c.fft.Forward(c.window, c.inputs[c.newest])
	copy(c.window, c.window[b:])

	clear(c.acc)
	idx := c.newest
	for _, h := range c.partitions {
		x := c.inputs[idx]
		for k, hk := range h {
			c.acc[k] += x[k] * hk
		}
		idx--
		if idx < 0 {
			idx = len(c.inputs) - 1
		}
	}
	c.fft.Inverse(c.acc, c.time)
	copy(c.tail, c.time[b:])
}

// Reset clears the stream.
func (c *Convolver) Reset() {
	clear(c.hist)
	clear(c.window)
	clear(c.tail)
	for _, x := range c.inputs {
		clear(x)
	}
	c.write, c.pos = 0, 0
}
