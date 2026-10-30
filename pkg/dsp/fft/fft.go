// Package fft provides allocation-free fast Fourier transforms for
// power-of-two sizes: a complex transform and a real transform built on a
// complex transform of half the size. Tables are computed in the
// constructor; the transforms run in place or into caller buffers and are
// safe on the audio thread. An instance is not safe for concurrent use.
package fft

import (
	"fmt"
	"math"
	"unsafe"
)

// Complex is a complex FFT of a fixed size: a radix-4 Stockham transform
// (no bit-reversal pass; contiguous reads and writes) with a radix-2 pass
// when the size is not a power of four.
type Complex struct {
	n      int
	stages []stage
	work   []complex128
}

// stage is one radix-4 pass over sub-transforms of length n (stride s):
// twiddles holds w^p, w^2p and w^3p for p < n/4, w = exp(-2πi/n).
type stage struct {
	n, s     int
	twiddles []complex128
}

// NewComplex returns a complex FFT of size n, a power of two of at least 1.
func NewComplex(n int) (*Complex, error) {
	if n < 1 || n&(n-1) != 0 {
		return nil, fmt.Errorf("fft: size %d is not a power of two", n)
	}
	c := &Complex{n: n, work: make([]complex128, n)}
	for m, s := n, 1; m >= 4; m, s = m/4, s*4 {
		st := stage{n: m, s: s, twiddles: make([]complex128, 3*(m/4))}
		for p := range m / 4 {
			for j := 1; j <= 3; j++ {
				sin, cos := math.Sincos(-2 * math.Pi * float64(j*p) / float64(m))
				st.twiddles[3*p+j-1] = complex(cos, sin)
			}
		}
		c.stages = append(c.stages, st)
	}
	return c, nil
}

// Size returns the transform size.
func (c *Complex) Size() int { return c.n }

// Forward replaces x (length Size) with its discrete Fourier transform
// X[k] = Σ x[n]·exp(-2πikn/N).
func (c *Complex) Forward(x []complex128) { c.transform(x[:c.n]) }

// Inverse replaces X (length Size) with its inverse transform, scaled by
// 1/N so Inverse undoes Forward.
func (c *Complex) Inverse(x []complex128) {
	x = x[:c.n]
	// conj(FFT(conj(X)))/N
	for i, z := range x {
		x[i] = complex(real(z), -imag(z))
	}
	c.transform(x)
	scale := 1 / float64(c.n)
	for i, z := range x {
		x[i] = complex(real(z)*scale, -imag(z)*scale)
	}
}

func (c *Complex) transform(x []complex128) {
	src, dst := x, c.work
	for _, st := range c.stages {
		radix4(st, src, dst)
		src, dst = dst, src
	}
	// What remains are sub-transforms of length 2 (a radix-2 pass into x)
	// or 1.
	if c.n>>(2*len(c.stages)) == 2 {
		s := c.n / 2
		a, b := src[:s], src[s:2*s]
		for q := range s {
			u, v := a[q], b[q]
			x[q], x[q+s] = u+v, u-v
		}
	} else if &src[0] != &x[0] {
		copy(x, src)
	}
}

// radix4 runs one Stockham pass from src to dst.
func radix4(st stage, src, dst []complex128) {
	n1, s := st.n/4, st.s
	q4 := n1 * s // offset of each input quarter
	tw := st.twiddles[:3*n1]
	if s == 1 {
		// The first pass: one butterfly per p.
		a, b, c, d := src[:n1], src[n1:2*n1], src[2*n1:3*n1], src[3*n1:4*n1]
		dst = dst[:4*n1]
		for p := range a {
			av, bv, cv, dv := a[p], b[p], c[p], d[p]
			apc, amc := av+cv, av-cv
			bpd, bmd := bv+dv, bv-dv
			jbmd := complex(-imag(bmd), real(bmd))
			w := tw[3*p : 3*p+3]
			y := dst[4*p : 4*p+4]
			y[0] = apc + bpd
			y[1] = w[0] * (amc - jbmd)
			y[2] = w[1] * (apc - bpd)
			y[3] = w[2] * (amc + jbmd)
		}
		return
	}
	for p := range n1 {
		w1, w2, w3 := tw[3*p], tw[3*p+1], tw[3*p+2]
		in := p * s
		out := 4 * p * s
		a := src[in : in+s]
		b := src[in+q4 : in+q4+s][:len(a)]
		c := src[in+2*q4 : in+2*q4+s][:len(a)]
		d := src[in+3*q4 : in+3*q4+s][:len(a)]
		y0 := dst[out : out+s][:len(a)]
		y1 := dst[out+s : out+2*s][:len(a)]
		y2 := dst[out+2*s : out+3*s][:len(a)]
		y3 := dst[out+3*s : out+4*s][:len(a)]
		for q := range a {
			av, bv, cv, dv := a[q], b[q], c[q], d[q]
			apc, amc := av+cv, av-cv
			bpd, bmd := bv+dv, bv-dv
			jbmd := complex(-imag(bmd), real(bmd))
			y0[q] = apc + bpd
			y1[q] = w1 * (amc - jbmd)
			y2[q] = w2 * (apc - bpd)
			y3[q] = w3 * (amc + jbmd)
		}
	}
}

// Real is an FFT of real signals of a fixed even size N, returning the
// N/2+1 non-negative-frequency bins.
type Real struct {
	n       int
	half    *Complex
	work    []complex128 // N/2 points
	twiddle []complex128 // exp(-2πik/N) for k < N/2
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
	w, tw := r.work[:m], r.twiddle[:m]
	// The even and odd samples as the real and imaginary parts.
	copy(w, unsafe.Slice((*complex128)(unsafe.Pointer(&in[0])), m))
	r.half.Forward(w)
	// Split the half-size transform Z into the even (E) and odd (O) sample
	// spectra: X[k] = E[k] + W^k·O[k] and X[m-k] = conj(E[k] - W^k·O[k]),
	// with E[k] = (Z[k] + conj(Z[m-k]))/2 and O[k] = -i(Z[k] - conj(Z[m-k]))/2.
	z0 := w[0]
	out[0] = complex(real(z0)+imag(z0), 0)
	out[m] = complex(real(z0)-imag(z0), 0)
	for k := 1; k <= m/2; k++ {
		zk, zm := w[k], w[m-k]
		er, ei := 0.5*(real(zk)+real(zm)), 0.5*(imag(zk)-imag(zm))
		t := tw[k] * complex(0.5*(imag(zk)+imag(zm)), 0.5*(real(zm)-real(zk)))
		out[k] = complex(er+real(t), ei+imag(t))
		if k != m-k {
			out[m-k] = complex(er-real(t), imag(t)-ei)
		}
	}
}

// Inverse writes the real signal (length N) of the spectrum in (length
// N/2+1) to out, scaled so Inverse undoes Forward. The imaginary parts of
// bins 0 and N/2 are ignored.
func (r *Real) Inverse(in []complex128, out []float64) {
	m := r.n / 2
	in, out = in[:m+1], out[:r.n]
	w, tw := r.work[:m], r.twiddle[:m]
	// Z[k] = E[k] + i·O[k] and Z[m-k] = conj(E[k] - i·O[k]), with
	// E[k] = (X[k] + conj(X[m-k]))/2 and O[k] = conj(W^k)(X[k] - conj(X[m-k]))/2.
	x0, xm := real(in[0]), real(in[m])
	w[0] = complex(0.5*(x0+xm), 0.5*(x0-xm))
	for k := 1; k <= m/2; k++ {
		xk, xc := in[k], in[m-k]
		er, ei := 0.5*(real(xk)+real(xc)), 0.5*(imag(xk)-imag(xc))
		o := complex(0.5*(real(xk)-real(xc)), 0.5*(imag(xk)+imag(xc))) * complex(real(tw[k]), -imag(tw[k]))
		w[k] = complex(er-imag(o), ei+real(o))
		if k != m-k {
			w[m-k] = complex(er+imag(o), real(o)-ei)
		}
	}
	r.half.Inverse(w)
	copy(unsafe.Slice((*complex128)(unsafe.Pointer(&out[0])), m), w)
}
