package reverb

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fastmath"
)

// FDN implements a Feedback Delay Network reverb: several delay lines whose
// outputs are mixed by an orthogonal feedback matrix, scaled by the decay
// gain, lowpass damped and fed back into the lines.
//
// The FDN allocates only in NewFDN; Process and ProcessStereo are safe to
// call from the audio thread.
type FDN struct {
	// Number of delay lines (typically 4, 8, or 16)
	numDelays int

	// Delay lines
	delayLines   [][]float32
	delayTimes   []int
	writeIndices []int

	// The feedback matrix is orthogonal: a normalized Hadamard matrix for
	// power-of-two sizes, a Householder reflection otherwise, applied by
	// mix in O(n log n) and O(n). Decay and diffusion are applied when the
	// matrix is used, never stored into it.
	hadamard bool

	// Input and output gains
	inputGains  []float64
	outputGains []float64

	// Damping filters (one per delay line)
	dampingFilters []*DampingFilter

	// Global parameters
	decay      float64
	damping    float64
	diffusion  float64
	modulation float64
	wetLevel   float64
	dryLevel   float64

	// feedbackGain is the per-pass gain derived from decay.
	feedbackGain float64

	// Modulation LFOs
	modPhases []float64
	modDepth  float64
	modRate   float64

	sampleRate float64

	// Per-sample scratch, sized in NewFDN.
	delayOutputs   []float32
	feedbackInputs []float32
}

// maxDampingCoeff is the one-pole coefficient a damping of 1 reaches.
const maxDampingCoeff = 0.9

// DampingFilter implements a one-pole lowpass filter for damping:
// y[n] = (1-c)*x[n] + c*y[n-1]. It has unity gain at DC for every setting.
type DampingFilter struct {
	state float32
	coeff float64
}

// NewDampingFilter creates a damping filter with damping 0.5.
func NewDampingFilter() *DampingFilter {
	d := &DampingFilter{}
	d.SetDamping(0.5)
	return d
}

// SetDamping sets the damping (0-1, where 0 passes the signal unchanged and
// 1 is the lowest cutoff).
func (d *DampingFilter) SetDamping(damping float64) {
	d.coeff = max(0.0, min(1.0, damping)) * maxDampingCoeff
}

// Process applies the damping filter
func (d *DampingFilter) Process(input float32) float32 {
	d.state = flushDenormal(input*float32(1.0-d.coeff) + d.state*float32(d.coeff))
	return d.state
}

// Reset clears the filter state
func (d *DampingFilter) Reset() {
	d.state = 0
}

// NewFDN creates a new Feedback Delay Network reverb
func NewFDN(numDelays int, sampleRate float64) *FDN {
	numDelays = max(numDelays, 1)
	f := &FDN{
		numDelays:      numDelays,
		delayLines:     make([][]float32, numDelays),
		delayTimes:     make([]int, numDelays),
		writeIndices:   make([]int, numDelays),
		inputGains:     make([]float64, numDelays),
		outputGains:    make([]float64, numDelays),
		dampingFilters: make([]*DampingFilter, numDelays),
		modPhases:      make([]float64, numDelays),
		delayOutputs:   make([]float32, numDelays),
		feedbackInputs: make([]float32, numDelays),
		decay:          0.5,
		damping:        0.5,
		diffusion:      0.5,
		modulation:     0.0,
		wetLevel:       0.3,
		dryLevel:       0.7,
		modDepth:       5.0, // samples
		modRate:        0.5, // Hz
		sampleRate:     sampleRate,
	}

	// Delay times follow prime ratios so the lines do not share resonances.
	primes := []int{23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71, 73, 79, 83, 89}
	baseDelay := int(sampleRate * 0.01) // 10ms base delay

	for i := 0; i < numDelays; i++ {
		// Lines past len(primes) get an octave-up copy of the ratio series.
		delayTime := max(baseDelay*primes[i%len(primes)]*(1+i/len(primes))/23, 2)
		f.delayTimes[i] = delayTime
		// Room for the longest modulated read plus the interpolation tap.
		f.delayLines[i] = make([]float32, delayTime+int(math.Ceil(f.modDepth))+2)

		f.dampingFilters[i] = NewDampingFilter()
		f.modPhases[i] = float64(i) * 2.0 * math.Pi / float64(numDelays)

		f.inputGains[i] = 1.0 / math.Sqrt(float64(numDelays))
		f.outputGains[i] = 1.0 / math.Sqrt(float64(numDelays))
	}

	f.hadamard = numDelays&(numDelays-1) == 0
	f.updateInternalParameters()

	return f
}

// mix replaces x with M·x for the orthogonal feedback matrix M: the
// normalized Hadamard matrix (H[i][j] = (-1)^popcount(i&j)/√n, a fast
// Walsh-Hadamard transform) when the size is a power of two, otherwise the
// Householder reflection I - (2/n)·1·1ᵀ.
func (f *FDN) mix(x []float32) {
	n := len(x)
	if f.hadamard {
		for h := 1; h < n; h *= 2 {
			for i := 0; i < n; i += 2 * h {
				a, b := x[i:i+h], x[i+h:i+2*h]
				for j := range a {
					a[j], b[j] = a[j]+b[j], a[j]-b[j]
				}
			}
		}
		scale := float32(1 / math.Sqrt(float64(n)))
		for i := range x {
			x[i] *= scale
		}
		return
	}
	var sum float32
	for _, v := range x {
		sum += v
	}
	sum *= 2 / float32(n)
	for i := range x {
		x[i] -= sum
	}
}

// SetDecay sets the decay time (0-1, where 0 = short, 1 = long)
func (f *FDN) SetDecay(decay float64) {
	f.decay = max(0.0, min(1.0, decay))
	f.updateInternalParameters()
}

// SetDamping sets the damping amount (0-1)
func (f *FDN) SetDamping(damping float64) {
	f.damping = max(0.0, min(1.0, damping))
	f.updateInternalParameters()
}

// SetDiffusion sets the diffusion amount (0-1): 0 keeps the delay lines
// independent, 1 mixes them fully through the feedback matrix.
func (f *FDN) SetDiffusion(diffusion float64) {
	f.diffusion = max(0.0, min(1.0, diffusion))
}

// SetModulation sets the modulation amount (0-1)
func (f *FDN) SetModulation(modulation float64) {
	f.modulation = max(0.0, min(1.0, modulation))
}

// SetWetLevel sets the wet signal level (0-1)
func (f *FDN) SetWetLevel(level float64) {
	f.wetLevel = max(0.0, min(1.0, level))
}

// SetDryLevel sets the dry signal level (0-1)
func (f *FDN) SetDryLevel(level float64) {
	f.dryLevel = max(0.0, min(1.0, level))
}

// updateInternalParameters updates the damping filters and the feedback
// gain after a parameter change.
func (f *FDN) updateInternalParameters() {
	for i := 0; i < f.numDelays; i++ {
		f.dampingFilters[i].SetDamping(f.damping)
	}
	// Higher decay = more feedback = longer tail. The feedback matrix is
	// orthogonal, so any gain below 1 keeps the network stable.
	f.feedbackGain = 0.4 + f.decay*0.58 // 0.4 to 0.98
}

// tick advances the network by one sample, leaving the delay line outputs
// of this sample in delayOutputs.
func (f *FDN) tick(input float32) {
	n := f.numDelays
	phaseStep := 2.0 * math.Pi * f.modRate / f.sampleRate

	for i := 0; i < n; i++ {
		line := f.delayLines[i]
		size := len(line)

		// Delay in samples, modulated within [delayTime, delayTime+modDepth].
		delay := float64(f.delayTimes[i])
		if f.modulation > 0 {
			delay += f.modDepth * f.modulation * 0.5 * (1 + fastmath.Sin2Pi(f.modPhases[i]*(0.5/math.Pi)))
			f.modPhases[i] += phaseStep
			if f.modPhases[i] >= 2.0*math.Pi {
				f.modPhases[i] -= 2.0 * math.Pi
			}
		}
		whole := int(delay)
		frac := float32(delay - float64(whole))

		// The most recent write is at writeIndex-1 (delay 1).
		idx1 := f.writeIndices[i] - whole
		if idx1 < 0 {
			idx1 += size
		}
		idx2 := idx1 - 1
		if idx2 < 0 {
			idx2 += size
		}
		f.delayOutputs[i] = line[idx1]*(1-frac) + line[idx2]*frac
	}

	// feedback = gain * ((1-diffusion)*I + diffusion*M) * outputs
	gain := float32(f.feedbackGain)
	direct, diffuse := gain*float32(1.0-f.diffusion), gain*float32(f.diffusion)
	mixed := f.feedbackInputs
	copy(mixed, f.delayOutputs)
	f.mix(mixed)
	for i, out := range f.delayOutputs {
		mixed[i] = direct*out + diffuse*mixed[i]
	}

	for i := 0; i < n; i++ {
		in := input*float32(f.inputGains[i]) + f.feedbackInputs[i]
		f.delayLines[i][f.writeIndices[i]] = f.dampingFilters[i].Process(in)
		f.writeIndices[i]++
		if f.writeIndices[i] >= len(f.delayLines[i]) {
			f.writeIndices[i] = 0
		}
	}
}

// Process processes a mono input sample
func (f *FDN) Process(input float32) float32 {
	f.tick(input)
	var wet float32
	for i := 0; i < f.numDelays; i++ {
		wet += f.delayOutputs[i] * float32(f.outputGains[i])
	}
	return input*float32(f.dryLevel) + wet*float32(f.wetLevel)
}

// ProcessStereo processes stereo input. Both channels feed the network; the
// left output sums every delay line and the right output sums them with
// alternating signs, two orthogonal taps that give a decorrelated stereo tail.
func (f *FDN) ProcessStereo(inputL, inputR float32) (outputL, outputR float32) {
	f.tick((inputL + inputR) * 0.5)
	var wetL, wetR float32
	for i := 0; i < f.numDelays; i++ {
		v := f.delayOutputs[i] * float32(f.outputGains[i])
		wetL += v
		if i%2 == 0 {
			wetR += v
		} else {
			wetR -= v
		}
	}
	outputL = inputL*float32(f.dryLevel) + wetL*float32(f.wetLevel)
	outputR = inputR*float32(f.dryLevel) + wetR*float32(f.wetLevel)
	return outputL, outputR
}

// Reset clears all internal state
func (f *FDN) Reset() {
	for i := 0; i < f.numDelays; i++ {
		clear(f.delayLines[i])
		f.writeIndices[i] = 0
		f.dampingFilters[i].Reset()
		f.modPhases[i] = float64(i) * 2.0 * math.Pi / float64(f.numDelays)
	}
}

// Preset methods

// SetPresetSmallRoom configures the FDN for a small room
func (f *FDN) SetPresetSmallRoom() {
	f.SetDecay(0.2)
	f.SetDamping(0.8)
	f.SetDiffusion(0.7)
	f.SetModulation(0.1)
	f.SetWetLevel(0.25)
	f.SetDryLevel(0.75)
}

// SetPresetMediumHall configures the FDN for a medium hall
func (f *FDN) SetPresetMediumHall() {
	f.SetDecay(0.5)
	f.SetDamping(0.5)
	f.SetDiffusion(0.85)
	f.SetModulation(0.15)
	f.SetWetLevel(0.35)
	f.SetDryLevel(0.65)
}

// SetPresetLargeHall configures the FDN for a large hall
func (f *FDN) SetPresetLargeHall() {
	f.SetDecay(0.8)
	f.SetDamping(0.3)
	f.SetDiffusion(0.9)
	f.SetModulation(0.2)
	f.SetWetLevel(0.4)
	f.SetDryLevel(0.6)
}

// SetPresetCathedral configures the FDN for a cathedral
func (f *FDN) SetPresetCathedral() {
	f.SetDecay(0.95)
	f.SetDamping(0.1)
	f.SetDiffusion(0.95)
	f.SetModulation(0.25)
	f.SetWetLevel(0.5)
	f.SetDryLevel(0.5)
}
