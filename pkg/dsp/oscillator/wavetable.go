package oscillator

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/pkg/dsp/fft"
)

const (
	tableSize   = 2048
	tableLevels = 11 // level l keeps harmonics up to tableSize/2 >> l
)

// WavetableBank holds single-cycle frames, each band-limited in advance
// into one table per octave so playback does not alias. It is immutable
// once built and can be shared by any number of Wavetable oscillators,
// including across threads.
type WavetableBank struct {
	// tables[frame][level] has tableSize+1 samples; the last repeats the
	// first for interpolation.
	tables [][][]float32
}

// NewWavetableBank builds a bank from single-cycle frames of any length
// (at least 2 samples each). Frames are resampled to 2048 points through
// their spectrum and normalized together so the loudest peaks at 1.
// It allocates and runs FFTs; build banks off the audio thread.
func NewWavetableBank(frames [][]float32) (*WavetableBank, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("oscillator: wavetable bank needs at least one frame")
	}
	f, err := fft.NewReal(tableSize)
	if err != nil {
		return nil, err
	}
	bank := &WavetableBank{tables: make([][][]float32, len(frames))}
	spectrum := make([]complex128, f.Bins())
	band := make([]complex128, f.Bins())
	time := make([]float64, tableSize)
	peak := 0.0
	for i, frame := range frames {
		if len(frame) < 2 {
			return nil, fmt.Errorf("oscillator: wavetable frame %d has %d samples", i, len(frame))
		}
		if err := cycleSpectrum(frame, spectrum); err != nil {
			return nil, err
		}
		spectrum[0] = 0 // no DC
		bank.tables[i] = make([][]float32, tableLevels)
		for l := range tableLevels {
			copy(band, spectrum)
			for k := (tableSize / 2 >> l) + 1; k < len(band); k++ {
				band[k] = 0
			}
			f.Inverse(band, time)
			table := make([]float32, tableSize+1)
			for j, x := range time {
				table[j] = float32(x)
				if l == 0 {
					peak = max(peak, math.Abs(x))
				}
			}
			table[tableSize] = table[0]
			bank.tables[i][l] = table
		}
	}
	if peak > 0 {
		for _, levels := range bank.tables {
			for _, table := range levels {
				for j := range table {
					table[j] /= float32(peak)
				}
			}
		}
	}
	return bank, nil
}

// cycleSpectrum writes the spectrum of one cycle, scaled as if it had
// tableSize samples, up to the harmonic below the table's Nyquist bin.
func cycleSpectrum(frame []float32, out []complex128) error {
	n := len(frame)
	clear(out)
	limit := min(n/2, tableSize/2-1)
	scale := complex(float64(tableSize)/float64(n), 0)
	if n&(n-1) == 0 {
		f, err := fft.NewReal(n)
		if err != nil {
			return err
		}
		in := make([]float64, n)
		for j, x := range frame {
			in[j] = float64(x)
		}
		spec := make([]complex128, f.Bins())
		f.Forward(in, spec)
		copy(out[:limit+1], spec)
	} else {
		for k := 0; k <= limit; k++ {
			var acc complex128
			for j, x := range frame {
				s, c := math.Sincos(-2 * math.Pi * float64(k*j) / float64(n))
				acc += complex(float64(x)*c, float64(x)*s)
			}
			out[k] = acc
		}
	}
	for k := range out[:limit+1] {
		out[k] *= scale
	}
	// An even frame's Nyquist bin stands for the whole harmonic; in the
	// larger table it is one half of a conjugate pair.
	if n%2 == 0 && n/2 == limit {
		out[limit] /= 2
	}
	return nil
}

// Frames returns the number of frames.
func (b *WavetableBank) Frames() int { return len(b.tables) }

// BasicShapes returns single cycles of a sine, triangle, sawtooth and
// square, in that order, for a morphing bank.
func BasicShapes() [][]float32 {
	const n = 2048
	shapes := make([][]float32, 4)
	for s := range shapes {
		shapes[s] = make([]float32, n)
	}
	for i := range n {
		p := float64(i) / n
		shapes[0][i] = float32(math.Sin(2 * math.Pi * p))
		shapes[1][i] = float32(1 - 4*math.Abs(math.Mod(p+0.25, 1)-0.5))
		shapes[2][i] = float32(2*math.Mod(p+0.5, 1) - 1)
		if p < 0.5 {
			shapes[3][i] = 1
		} else {
			shapes[3][i] = -1
		}
	}
	return shapes
}

// Wavetable plays a WavetableBank: it morphs between adjacent frames by
// position and plays the octave table with the most harmonics that all
// stay below the Nyquist frequency at the current pitch. It does not
// allocate.
type Wavetable struct {
	bank       *WavetableBank
	sampleRate float64
	phase      float64
	increment  float64
	position   float64
	level      int
}

// NewWavetable returns an oscillator on bank at 440 Hz, position 0.
func NewWavetable(bank *WavetableBank, sampleRate float64) *Wavetable {
	w := &Wavetable{bank: bank, sampleRate: sampleRate}
	w.SetFrequency(440)
	return w
}

// SetBank switches to another bank; the phase continues.
func (w *Wavetable) SetBank(bank *WavetableBank) {
	w.bank = bank
	w.SetPosition(w.position)
}

// SetFrequency sets the frequency in Hz.
func (w *Wavetable) SetFrequency(hz float64) {
	w.increment = math.Abs(hz) / w.sampleRate
	// Level l holds harmonics up to 1024/2^l; the highest must stay
	// below Nyquist: 1024/2^l · increment ≤ 0.5.
	need := math.Log2(float64(tableSize) * w.increment)
	w.level = max(0, min(tableLevels-1, int(math.Ceil(need))))
}

// SetPosition morphs across the frames: 0 is the first, 1 the last.
func (w *Wavetable) SetPosition(position float64) {
	w.position = max(0, min(1, position))
}

// SetPhase sets the phase in cycles (0..1).
func (w *Wavetable) SetPhase(phase float64) { w.phase = phase - math.Floor(phase) }

// Next returns the next sample.
func (w *Wavetable) Next() float32 {
	pos := w.position * float64(len(w.bank.tables)-1)
	f0 := int(pos)
	morph := float32(pos - float64(f0))
	f1 := min(f0+1, len(w.bank.tables)-1)

	idx := w.phase * tableSize
	i := int(idx)
	frac := float32(idx - float64(i))
	y := w.read(f0, i, frac)
	if morph > 0 {
		y += morph * (w.read(f1, i, frac) - y)
	}
	w.phase += w.increment
	if w.phase >= 1 {
		w.phase -= math.Floor(w.phase)
	}
	return y
}

func (w *Wavetable) read(frame, i int, frac float32) float32 {
	levels := w.bank.tables[frame]
	t := levels[w.level]
	return t[i] + frac*(t[i+1]-t[i])
}

// Process fills buf with samples.
func (w *Wavetable) Process(buf []float32) {
	for i := range buf {
		buf[i] = w.Next()
	}
}

// Reset sets the phase to 0.
func (w *Wavetable) Reset() { w.phase = 0 }
