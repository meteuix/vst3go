// Package fft provides allocation-free fast Fourier transforms for
// power-of-two sizes: a complex transform and a real transform built on a
// complex transform of half the size. Tables are computed in the
// constructor; the transforms run in place or into caller buffers and are
// safe on the audio thread. An instance is not safe for concurrent use.
package fft

import (
	"fmt"
	"math"
	"math/bits"
)

// Complex is an in-place radix-2 complex FFT of a fixed size.
type Complex struct {
	n       int
	twiddle []complex128 // exp(-2πik/n) for k < n/2
	reverse []int        // bit-reversed index of every index
}

// NewComplex returns a complex FFT of size n, a power of two of at least 1.
func NewComplex(n int) (*Complex, error) {
	if n < 1 || n&(n-1) != 0 {
		return nil, fmt.Errorf("fft: size %d is not a power of two", n)
	}
	c := &Complex{n: n, twiddle: make([]complex128, n/2), reverse: make([]int, n)}
	for k := range c.twiddle {
		s, co := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		c.twiddle[k] = complex(co, s)
	}
	shift := 64 - bits.Len(uint(n-1))
	for i := range c.reverse {
		if n > 1 {
			c.reverse[i] = int(bits.Reverse64(uint64(i)) >> shift)
		}
	}
	return c, nil
}

// Size returns the transform size.
func (c *Complex) Size() int { return c.n }

// Forward replaces x (length Size) with its discrete Fourier transform
// X[k] = Σ x[n]·exp(-2πikn/N).
func (c *Complex) Forward(x []complex128) { c.transform(x, false) }

// Inverse replaces X (length Size) with its inverse transform, scaled by
// 1/N so Inverse undoes Forward.
func (c *Complex) Inverse(x []complex128) {
	c.transform(x, true)
	scale := complex(1/float64(c.n), 0)
	for i := range x[:c.n] {
		x[i] *= scale
	}
}

func (c *Complex) transform(x []complex128, inverse bool) {
	x = x[:c.n]
	for i, j := range c.reverse {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= c.n; size <<= 1 {
		half, step := size/2, c.n/size
		for start := 0; start < c.n; start += size {
			for k := range half {
				w := c.twiddle[k*step]
				if inverse {
					w = complex(real(w), -imag(w))
				}
				a, b := x[start+k], x[start+k+half]*w
				x[start+k], x[start+k+half] = a+b, a-b
			}
		}
	}
}

// Real is an FFT of real signals of a fixed even size N, returning the
// N/2+1 non-negative-frequency bins.
type Real struct {
	n       int
	half    *Complex
	work    []complex128 // N/2 points
	twiddle []complex128 // exp(-2πik/N) for k <= N/4
}

// NewReal returns a real FFT of size n, a power of two of at least 2.
func NewReal(n int) (*Real, error) {
	if n < 2 || n&(n-1) != 0 {
		return nil, fmt.Errorf("fft: real size %d is not a power of two of at least 2", n)
	}
	half, err := NewComplex(n / 2)
	if err != nil {
		return nil, err
	}
	r := &Real{n: n, half: half, work: make([]complex128, n/2), twiddle: make([]complex128, n/2)}
	for k := range r.twiddle {
		s, co := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		r.twiddle[k] = complex(co, s)
	}
	return r, nil
}

// Size returns the transform size N.
func (r *Real) Size() int { return r.n }

// Bins returns the number of spectrum bins, N/2+1.
func (r *Real) Bins() int { return r.n/2 + 1 }

// Forward writes the spectrum of in (length N) to out (length N/2+1). Bin
// k holds frequency k·sampleRate/N; bins 0 and N/2 are real.
func (r *Real) Forward(in []float64, out []complex128) {
	m := r.n / 2
	in, out = in[:r.n], out[:m+1]
	for i := range m {
		r.work[i] = complex(in[2*i], in[2*i+1])
	}
	r.half.Forward(r.work)
	// Split the half-size transform Z into the even (E) and odd (O) sample
	// spectra: X[k] = E[k] + W^k·O[k].
	z0 := r.work[0]
	out[0] = complex(real(z0)+imag(z0), 0)
	out[m] = complex(real(z0)-imag(z0), 0)
	for k := 1; k < m; k++ {
		zk, zc := r.work[k], conj(r.work[m-k])
		e := (zk + zc) * 0.5
		o := (zk - zc) * complex(0, -0.5)
		out[k] = e + r.twiddle[k]*o
	}
}

// Inverse writes the real signal (length N) of the spectrum in (length
// N/2+1) to out, scaled so Inverse undoes Forward. The imaginary parts of
// bins 0 and N/2 are ignored.
func (r *Real) Inverse(in []complex128, out []float64) {
	m := r.n / 2
	in, out = in[:m+1], out[:r.n]
	for k := range m {
		xk, xc := in[k], conj(in[m-k])
		if k == 0 {
			xk, xc = complex(real(in[0]), 0), complex(real(in[m]), 0)
		}
		e := (xk + xc) * 0.5
		o := (xk - xc) * 0.5 * conj(r.twiddle[k])
		r.work[k] = e + o*complex(0, 1)
	}
	r.half.Inverse(r.work)
	for i, z := range r.work {
		out[2*i], out[2*i+1] = real(z), imag(z)
	}
}

func conj(z complex128) complex128 { return complex(real(z), -imag(z)) }
