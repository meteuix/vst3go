package resample

import "fmt"

// halfbandOrders holds m for each 2x stage (filter length 4m+3), from the
// stage nearest the host rate up. The first stage carries the sharp
// transition just below the host Nyquist frequency; later stages only
// need to reject images far above the audio band.
var halfbandOrders = [...]int{17, 7, 6, 6}

const halfbandBeta = 10.0 // about 100 dB stopband

// halfband is a 2x up- and down-sampling stage. A half-band filter of
// length 4m+3 has a centre tap of 1/2 and zeros at every other position,
// so only the 2m+2 even taps are stored.
type halfband struct {
	taps []float64 // h[0], h[2], ..., h[4m+2]
	m    int

	upHist   []float64 // input history, stored twice
	upWrite  int
	evenHist []float64 // down: even-indexed high-rate samples, stored twice
	oddDelay []float64 // down: odd-indexed samples, m+1 pairs
	downPos  int
	oddPos   int
}

func newHalfband(m int) *halfband {
	c := 2*m + 1
	h := &halfband{
		taps:     make([]float64, 2*m+2),
		m:        m,
		upHist:   make([]float64, 2*(2*m+2)),
		evenHist: make([]float64, 2*(2*m+2)),
		oddDelay: make([]float64, m+1),
	}
	sum := 0.0
	for i := range h.taps {
		t := float64(2*i - c)
		h.taps[i] = 0.5 * sinc(t/2) * kaiser(t/float64(c+1), halfbandBeta)
		sum += h.taps[i]
	}
	// The even taps sum to 1/2 for unity gain at DC.
	for i := range h.taps {
		h.taps[i] *= 0.5 / sum
	}
	return h
}

// up writes 2·len(in) samples to out.
func (h *halfband) up(in []float64, out []float64) {
	t := len(h.taps)
	for k, x := range in {
		h.upHist[h.upWrite], h.upHist[h.upWrite+t] = x, x
		if h.upWrite++; h.upWrite == t {
			h.upWrite = 0
		}
		// Window oldest..newest is upHist[upWrite : upWrite+t]; tap i
		// applies to x[k-i], the (t-1-i)-th oldest.
		win := h.upHist[h.upWrite : h.upWrite+t]
		out[2*k] = 2 * symmetricDot(h.taps, win)
		out[2*k+1] = win[t-1-h.m]
	}
}

// down writes len(in)/2 samples to out.
func (h *halfband) down(in []float64, out []float64) {
	t := len(h.taps)
	for k := range out {
		e, o := in[2*k], in[2*k+1]
		h.evenHist[h.downPos], h.evenHist[h.downPos+t] = e, e
		if h.downPos++; h.downPos == t {
			h.downPos = 0
		}
		y := symmetricDot(h.taps, h.evenHist[h.downPos:h.downPos+t])
		// The centre tap meets the odd sample m+1 pairs back.
		y += 0.5 * h.oddDelay[h.oddPos]
		h.oddDelay[h.oddPos] = o
		if h.oddPos++; h.oddPos == len(h.oddDelay) {
			h.oddPos = 0
		}
		out[k] = y
	}
}

// symmetricDot returns Σ taps[i]·win[t-1-i] for taps symmetric about
// their centre (taps[i] = taps[t-1-i], t even): Σ taps[i]·(win[i] +
// win[t-1-i]) over the first half, half the multiplies, in two running
// sums.
func symmetricDot(taps, win []float64) float64 {
	t := len(taps)
	win = win[:t]
	half := taps[:t/2]
	var s0, s1 float64
	i := 0
	for ; i+2 <= len(half); i += 2 {
		s0 += half[i] * (win[i] + win[t-1-i])
		s1 += half[i+1] * (win[i+1] + win[t-2-i])
	}
	if i < len(half) {
		s0 += half[i] * (win[i] + win[t-1-i])
	}
	return s0 + s1
}

func (h *halfband) reset() {
	clear(h.upHist)
	clear(h.evenHist)
	clear(h.oddDelay)
	h.upWrite, h.downPos, h.oddPos = 0, 0, 0
}

// Oversampler runs a mono signal at a multiple of its rate: Up converts a
// block to the high rate, the caller processes it (typically a
// nonlinearity that would otherwise alias), and Down filters and returns
// it to the original rate. The round trip delays the signal by Latency
// samples at the original rate and is flat to about 0.4 times the original
// sample rate (19 kHz at 48 kHz), with about 90 dB of image and alias
// rejection; content between 0.4 and 0.6 times the original rate (the
// transition band) can fold back, but only to above 0.4 times it.
type Oversampler struct {
	factor int
	stages []*halfband
	bufs   [][]float64 // bufs[s] holds a block at 2^s times the rate
	high   []float32
	pad    []float64 // delay at the high rate making Latency whole
	padPos int
}

// NewOversampler returns an Oversampler for factor 1, 2, 4, 8 or 16 and
// blocks of up to maxBlock samples.
func NewOversampler(factor, maxBlock int) (*Oversampler, error) {
	stages := 0
	for f := factor; f > 1; f >>= 1 {
		if f&1 != 0 {
			return nil, fmt.Errorf("resample: oversampling factor %d is not a power of two", factor)
		}
		stages++
	}
	if factor < 1 || stages > len(halfbandOrders) {
		return nil, fmt.Errorf("resample: oversampling factor %d is not 1, 2, 4, 8 or 16", factor)
	}
	o := &Oversampler{factor: factor, high: make([]float32, maxBlock*factor)}
	o.bufs = make([][]float64, stages+1)
	for s := range o.bufs {
		o.bufs[s] = make([]float64, maxBlock<<s)
	}
	// Each stage delays by its centre tap (2m+1 samples at its rate) going
	// up and again going down. Pad at the high rate to a whole number of
	// original samples.
	delay := 0
	for s := range stages {
		h := newHalfband(halfbandOrders[s])
		o.stages = append(o.stages, h)
		delay += 2 * (2*h.m + 1) * (factor >> (s + 1))
	}
	if r := delay % factor; r != 0 {
		o.pad = make([]float64, factor-r)
	}
	return o, nil
}

// Factor returns the oversampling factor.
func (o *Oversampler) Factor() int { return o.factor }

// Latency returns the round-trip delay in samples at the original rate.
func (o *Oversampler) Latency() int {
	delay := len(o.pad)
	for s, h := range o.stages {
		delay += 2 * (2*h.m + 1) * (o.factor >> (s + 1))
	}
	return delay / o.factor
}

// Up converts in (at most maxBlock samples) to the high rate and returns
// the high-rate block, len(in)·Factor samples owned by the Oversampler and
// valid until the next Up.
func (o *Oversampler) Up(in []float32) []float32 {
	n := len(in)
	cur := o.bufs[0][:n]
	for i, x := range in {
		cur[i] = float64(x)
	}
	for s, h := range o.stages {
		next := o.bufs[s+1][:n<<(s+1)]
		h.up(cur, next)
		cur = next
	}
	high := o.high[:n*o.factor]
	for i, x := range cur {
		high[i] = float32(x)
	}
	return high
}

// Down converts a high-rate block of len(out)·Factor samples back to out.
func (o *Oversampler) Down(high []float32, out []float32) {
	n := len(out)
	s := len(o.stages)
	cur := o.bufs[s][:n<<s]
	for i, x := range high[:n<<s] {
		cur[i] = float64(x)
	}
	if pad := o.pad; len(pad) > 0 {
		pos := o.padPos
		for i, x := range cur {
			cur[i], pad[pos] = pad[pos], x
			if pos++; pos == len(pad) {
				pos = 0
			}
		}
		o.padPos = pos
	}
	for s := len(o.stages) - 1; s >= 0; s-- {
		next := o.bufs[s][:n<<s]
		o.stages[s].down(cur, next)
		cur = next
	}
	for i, x := range cur {
		out[i] = float32(x)
	}
}

// Reset clears the filters.
func (o *Oversampler) Reset() {
	for _, h := range o.stages {
		h.reset()
	}
	clear(o.pad)
	o.padPos = 0
}
