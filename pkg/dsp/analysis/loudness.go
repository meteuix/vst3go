package analysis

import "math"

// Loudness histograms span -70 LUFS (the absolute gate) to +10 LUFS in
// 0.01 LU bins; louder blocks land in the top bin.
const (
	loudnessFloor      = -70.0
	loudnessBinWidth   = 0.01
	loudnessBins       = 8000
	momentarySteps     = 4  // 400 ms
	shortTermSteps     = 30 // 3 s
	integratedRelative = -10.0
	rangeRelative      = -20.0
)

// LUFSMeter measures loudness per ITU-R BS.1770-4 and EBU R 128: momentary
// (400 ms), short-term (3 s) and gated integrated loudness, plus the
// loudness range (EBU Tech 3342). Readings advance every 100 ms of input.
//
// Channels are weighted 1.0 except, for five channels (L, R, C, Ls, Rs),
// 1.41 on the surrounds and, for six or more (L, R, C, LFE, Ls, Rs, ...),
// 0 on the LFE and 1.41 on Ls and Rs.
//
// Integrated loudness and loudness range come from fixed-size histograms
// with 0.01 LU resolution, so the meter runs indefinitely without
// allocating after construction.
type LUFSMeter struct {
	channels int
	weights  []float64
	stage1   []kFilter // K-weighting high shelf, per channel
	stage2   []kFilter // K-weighting high-pass (RLB), per channel

	stepFrames int // frames per 100 ms step
	frames     int // frames accumulated into the current step
	energy     float64

	steps     int // completed steps since the last reset
	history   [shortTermSteps]float64
	writePos  int
	momentary float64 // mean-square power of the last 400 ms
	shortTerm float64 // mean-square power of the last 3 s

	integrated loudnessHistogram // 400 ms blocks
	shortTerms loudnessHistogram // 3 s blocks for the loudness range

	out published[loudnessReading]
}

// loudnessReading is the set of readings published after each step.
type loudnessReading struct {
	momentary, shortTerm, integrated, lra float64
}

type kFilter struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *kFilter) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// loudnessHistogram counts gating blocks per 0.01 LU bin and keeps the exact
// power sum of each bin.
type loudnessHistogram struct {
	count []int
	power []float64
	n     int     // blocks in all bins
	sum   float64 // power of all blocks
}

func newLoudnessHistogram() loudnessHistogram {
	return loudnessHistogram{count: make([]int, loudnessBins), power: make([]float64, loudnessBins)}
}

// add records a block's mean-square power if it passes the absolute gate.
func (h *loudnessHistogram) add(power float64) {
	l := powerToLUFS(power)
	if l < loudnessFloor {
		return
	}
	bin := loudnessBin(l)
	h.count[bin]++
	h.power[bin] += power
	h.n++
	h.sum += power
}

// gated returns the bin of the relative gate (the power mean of all blocks
// plus offset LU), or -1 when there are no blocks.
func (h *loudnessHistogram) gated(offset float64) int {
	if h.n == 0 {
		return -1
	}
	return loudnessBin(max(loudnessFloor, powerToLUFS(h.sum/float64(h.n))+offset))
}

func (h *loudnessHistogram) reset() {
	clear(h.count)
	clear(h.power)
	h.n, h.sum = 0, 0
}

func loudnessBin(lufs float64) int {
	return min(loudnessBins-1, int((lufs-loudnessFloor)/loudnessBinWidth))
}

func powerToLUFS(power float64) float64 {
	if power <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(power)
}

// NewLUFSMeter creates a loudness meter for interleaved audio with the
// given channel count.
func NewLUFSMeter(sampleRate float64, channels int) *LUFSMeter {
	lm := &LUFSMeter{
		channels:   channels,
		weights:    make([]float64, channels),
		stage1:     make([]kFilter, channels),
		stage2:     make([]kFilter, channels),
		stepFrames: max(1, int(math.Round(0.1*sampleRate))),
		integrated: newLoudnessHistogram(),
		shortTerms: newLoudnessHistogram(),
	}
	for ch := range lm.weights {
		lm.weights[ch] = 1
		switch {
		case channels == 5 && ch >= 3:
			lm.weights[ch] = 1.41
		case channels >= 6 && ch == 3:
			lm.weights[ch] = 0
		case channels >= 6 && (ch == 4 || ch == 5):
			lm.weights[ch] = 1.41
		}
	}

	// ITU-R BS.1770-4 K-weighting, generalized to any sample rate.
	k := math.Tan(math.Pi * 1681.974450955533 / sampleRate)
	q := 0.7071752369554196
	vh := math.Pow(10, 3.999843853973347/20)
	vb := math.Pow(vh, 0.4996667741545416)
	a0 := 1 + k/q + k*k
	shelf := kFilter{
		b0: (vh + vb*k/q + k*k) / a0,
		b1: 2 * (k*k - vh) / a0,
		b2: (vh - vb*k/q + k*k) / a0,
		a1: 2 * (k*k - 1) / a0,
		a2: (1 - k/q + k*k) / a0,
	}
	k = math.Tan(math.Pi * 38.13547087602444 / sampleRate)
	q = 0.5003270373238773
	a0 = 1 + k/q + k*k
	highpass := kFilter{
		b0: 1, b1: -2, b2: 1,
		a1: 2 * (k*k - 1) / a0,
		a2: (1 - k/q + k*k) / a0,
	}
	for ch := range channels {
		lm.stage1[ch] = shelf
		lm.stage2[ch] = highpass
	}
	lm.out.init(func(*loudnessReading) {})
	lm.publish()
	return lm
}

// Process adds interleaved samples ([ch0, ch1, ch0, ch1, ...]); a trailing
// partial frame is ignored.
func (lm *LUFSMeter) Process(samples []float64) {
	for i := 0; i+lm.channels <= len(samples); i += lm.channels {
		for ch, x := range samples[i : i+lm.channels] {
			y := lm.stage2[ch].process(lm.stage1[ch].process(x))
			lm.energy += lm.weights[ch] * y * y
		}
		lm.frames++
		if lm.frames == lm.stepFrames {
			lm.completeStep()
		}
	}
}

// completeStep closes a 100 ms step and updates every reading.
func (lm *LUFSMeter) completeStep() {
	lm.history[lm.writePos] = lm.energy
	lm.writePos = (lm.writePos + 1) % shortTermSteps
	lm.energy, lm.frames = 0, 0
	lm.steps++

	// Windows not yet filled count the missing steps as silence.
	momentary, shortTerm := 0.0, 0.0
	for i := range shortTermSteps {
		e := lm.history[(lm.writePos-1-i+shortTermSteps)%shortTermSteps]
		if i < momentarySteps {
			momentary += e
		}
		shortTerm += e
	}
	lm.momentary = momentary / float64(momentarySteps*lm.stepFrames)
	lm.shortTerm = shortTerm / float64(shortTermSteps*lm.stepFrames)
	if lm.steps >= momentarySteps {
		lm.integrated.add(lm.momentary)
	}
	if lm.steps >= shortTermSteps {
		lm.shortTerms.add(lm.shortTerm)
	}
	lm.publish()
}

// publish computes the readings for readers.
func (lm *LUFSMeter) publish() {
	*lm.out.write() = loudnessReading{
		momentary:  powerToLUFS(lm.momentary),
		shortTerm:  powerToLUFS(lm.shortTerm),
		integrated: lm.integratedLUFS(),
		lra:        lm.loudnessRange(),
	}
	lm.out.publish()
}

// GetMomentaryLUFS returns the loudness of the last 400 ms, or -Inf before
// the first 100 ms step and for silence.
func (lm *LUFSMeter) GetMomentaryLUFS() float64 { return lm.out.get().momentary }

// GetShortTermLUFS returns the loudness of the last 3 s.
func (lm *LUFSMeter) GetShortTermLUFS() float64 { return lm.out.get().shortTerm }

// GetIntegratedLUFS returns the gated loudness of everything since the
// last reset: the power mean of the 400 ms blocks (75% overlap) above
// -70 LUFS and above the ungated mean minus 10 LU. It is -Inf until the
// first block passes the gates.
func (lm *LUFSMeter) GetIntegratedLUFS() float64 { return lm.out.get().integrated }

func (lm *LUFSMeter) integratedLUFS() float64 {
	h := &lm.integrated
	gate := h.gated(integratedRelative)
	if gate < 0 {
		return math.Inf(-1)
	}
	n, sum := 0, 0.0
	for i := gate; i < loudnessBins; i++ {
		n += h.count[i]
		sum += h.power[i]
	}
	if n == 0 {
		return math.Inf(-1)
	}
	return powerToLUFS(sum / float64(n))
}

// GetLoudnessRange returns the loudness range in LU (EBU Tech 3342): the
// spread between the 10th and 95th percentiles of the 3 s short-term
// loudness values above -70 LUFS and above their mean minus 20 LU. It is
// 0 until a full 3 s window has been measured.
func (lm *LUFSMeter) GetLoudnessRange() float64 { return lm.out.get().lra }

func (lm *LUFSMeter) loudnessRange() float64 {
	h := &lm.shortTerms
	gate := h.gated(rangeRelative)
	if gate < 0 {
		return 0
	}
	n := 0
	for i := gate; i < loudnessBins; i++ {
		n += h.count[i]
	}
	if n == 0 {
		return 0
	}
	low := h.percentileBin(gate, int(math.Round(float64(n-1)*0.10)))
	high := h.percentileBin(gate, int(math.Round(float64(n-1)*0.95)))
	return float64(high-low) * loudnessBinWidth
}

// percentileBin returns the bin holding the rank-th block (from zero) at
// or above bin from.
func (h *loudnessHistogram) percentileBin(from, rank int) int {
	for i := from; i < loudnessBins; i++ {
		rank -= h.count[i]
		if rank < 0 {
			return i
		}
	}
	return loudnessBins - 1
}

// Reset clears the filters and all measurements.
func (lm *LUFSMeter) Reset() {
	for ch := range lm.stage1 {
		lm.stage1[ch].x1, lm.stage1[ch].x2, lm.stage1[ch].y1, lm.stage1[ch].y2 = 0, 0, 0, 0
		lm.stage2[ch].x1, lm.stage2[ch].x2, lm.stage2[ch].y1, lm.stage2[ch].y2 = 0, 0, 0, 0
	}
	lm.frames, lm.energy, lm.steps, lm.writePos = 0, 0, 0, 0
	lm.history = [shortTermSteps]float64{}
	lm.momentary, lm.shortTerm = 0, 0
	lm.integrated.reset()
	lm.shortTerms.reset()
	lm.publish()
}
