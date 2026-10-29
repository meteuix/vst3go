package resample

import (
	"math"
	"sync"
)

const (
	kernelZeros  = 24
	kernelPhases = 512
	kernelBeta   = 8.0
)

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
	table []float64
}

var (
	kernelOnce   sync.Once
	sharedKernel *Kernel
)

// SincKernel returns the shared kernel: 24 zero crossings on each side
// (48 taps at full bandwidth), about 80 dB stopband, flat to about 0.9 of
// the cutoff with the transition band centred on it. Content in the
// transition band can fold back, but only into the top tenth of the
// output band.
func SincKernel() *Kernel {
	kernelOnce.Do(func() {
		k := &Kernel{table: make([]float64, kernelZeros*kernelPhases+2)}
		for i := range k.table {
			t := float64(i) / kernelPhases
			k.table[i] = sinc(t) * kaiser(t/kernelZeros, kernelBeta)
		}
		sharedKernel = k
	})
	return sharedKernel
}

// Zeros returns how many zero crossings the kernel spans on each side.
func (k *Kernel) Zeros() int { return kernelZeros }

// Weight returns the kernel at offset t (0 outside ±Zeros).
func (k *Kernel) Weight(t float64) float64 {
	p := math.Abs(t) * kernelPhases
	i := int(p)
	if i >= kernelZeros*kernelPhases {
		return 0
	}
	f := p - float64(i)
	return k.table[i] + f*(k.table[i+1]-k.table[i])
}
