package dynamics

import (
	"math"
)

// maxLookahead is the longest lookahead SetLookahead accepts, in seconds.
const maxLookahead = 0.010

// truePeakTaps is the length of each polyphase branch of the 4x true peak
// interpolator.
const truePeakTaps = 6

// Limiter is a brick-wall peak limiter.
//
// The gain for each sample is the lowest gain any sample inside the
// lookahead window needs, recovered with the release time and then
// averaged over the window, so the gain ramps down ahead of a peak and the
// output never exceeds the ceiling. With lookahead 0 the attack is
// instantaneous. With true peak enabled, peaks between samples are
// estimated with 4x oversampling; they are caught in time once the
// lookahead is at least 3 samples.
//
// All buffers are sized in NewLimiter for the maximum lookahead, so every
// method is safe to call from the audio thread.
type Limiter struct {
	sampleRate float64

	// Parameters
	threshold float64 // Ceiling threshold in dB
	ceiling   float64 // Ceiling as linear gain
	release   float64 // Release time in seconds
	lookahead float64 // Lookahead time in seconds
	truePeak  bool    // Enable true peak detection

	releaseCoeff float64

	// delaySamples is the lookahead in samples; the gain window holds
	// delaySamples+1 values.
	delaySamples int
	delayL       []float32
	delayR       []float32
	delayIndex   int

	// Sliding window minimum of the required gain (monotonic queue).
	minPos  []int
	minVal  []float64
	minHead int
	minLen  int
	frame   int

	// Moving average of the released gain over the window.
	avgBuf []float64
	avgSum float64
	avgPos int

	released float64

	// True peak interpolation history per channel and polyphase kernels.
	historyL [truePeakTaps]float32
	historyR [truePeakTaps]float32
	kernels  [3][truePeakTaps]float64

	// State
	gainReduction float64 // Current gain reduction in dB
}

// NewLimiter creates a new brick-wall limiter
func NewLimiter(sampleRate float64) *Limiter {
	capacity := int(math.Ceil(maxLookahead*sampleRate)) + 1
	l := &Limiter{
		sampleRate: sampleRate,
		release:    0.050, // 50ms default release
		truePeak:   true,  // True peak detection enabled by default
		delayL:     make([]float32, capacity),
		delayR:     make([]float32, capacity),
		minPos:     make([]int, capacity),
		minVal:     make([]float64, capacity),
		avgBuf:     make([]float64, capacity),
	}
	l.SetThreshold(-0.3)
	l.SetRelease(l.release)
	l.initTruePeakKernels()
	l.SetLookahead(0.005) // 5ms default lookahead
	return l
}

// initTruePeakKernels builds Lanczos-3 kernels for the three points at 1/4,
// 2/4 and 3/4 between the two middle samples of the history.
func (l *Limiter) initTruePeakKernels() {
	const a = truePeakTaps / 2
	for phase := 0; phase < 3; phase++ {
		frac := float64(phase+1) / 4
		sum := 0.0
		for k := 0; k < truePeakTaps; k++ {
			x := float64(k-(a-1)) - frac
			w := lanczos(x, a)
			l.kernels[phase][k] = w
			sum += w
		}
		for k := range l.kernels[phase] {
			l.kernels[phase][k] /= sum
		}
	}
}

func lanczos(x float64, a int) float64 {
	if x == 0 {
		return 1
	}
	if math.Abs(x) >= float64(a) {
		return 0
	}
	px := math.Pi * x
	return float64(a) * math.Sin(px) * math.Sin(px/float64(a)) / (px * px)
}

// SetThreshold sets the limiter ceiling in dB
func (l *Limiter) SetThreshold(dB float64) {
	l.threshold = math.Min(0.0, dB) // Can't be positive
	l.ceiling = math.Pow(10.0, l.threshold/20.0)
}

// SetRelease sets the release time in seconds
func (l *Limiter) SetRelease(seconds float64) {
	l.release = math.Max(0.001, seconds)
	l.releaseCoeff = math.Exp(-1.0 / (l.release * l.sampleRate))
}

// SetLookahead sets the lookahead time in seconds (0 to 10ms). Changing it
// clears the delay line.
func (l *Limiter) SetLookahead(seconds float64) {
	l.lookahead = math.Max(0.0, math.Min(maxLookahead, seconds))
	samples := min(int(math.Round(l.lookahead*l.sampleRate)), len(l.delayL)-1)
	if samples != l.delaySamples {
		l.delaySamples = samples
		l.Reset()
	}
}

// Latency returns the delay the lookahead adds, in samples
func (l *Limiter) Latency() int {
	return l.delaySamples
}

// SetTruePeak enables or disables true peak detection
func (l *Limiter) SetTruePeak(enabled bool) {
	l.truePeak = enabled
}

// GetGainReduction returns the current gain reduction in dB
func (l *Limiter) GetGainReduction() float64 {
	return l.gainReduction
}

// peak returns the detection level of a new input sample on one channel.
func (l *Limiter) peak(history *[truePeakTaps]float32, input float32) float64 {
	p := math.Abs(float64(input))
	if !l.truePeak {
		return p
	}
	copy(history[:], history[1:])
	history[truePeakTaps-1] = input
	for phase := range l.kernels {
		v := 0.0
		for k, c := range l.kernels[phase] {
			v += c * float64(history[k])
		}
		p = math.Max(p, math.Abs(v))
	}
	return p
}

// gain advances the gain computer by one frame whose detection level is
// peak, and returns the gain for the frame leaving the delay line.
func (l *Limiter) gain(peak float64) float64 {
	required := 1.0
	if peak > l.ceiling {
		required = l.ceiling / peak
	}

	window := l.delaySamples + 1
	capacity := len(l.minPos)

	// Push into the monotonic queue, dropping larger values from the tail.
	for l.minLen > 0 {
		tail := (l.minHead + l.minLen - 1) % capacity
		if l.minVal[tail] < required {
			break
		}
		l.minLen--
	}
	tail := (l.minHead + l.minLen) % capacity
	l.minPos[tail] = l.frame
	l.minVal[tail] = required
	l.minLen++
	// Drop values that left the window.
	for l.minPos[l.minHead] <= l.frame-window {
		l.minHead = (l.minHead + 1) % capacity
		l.minLen--
	}
	l.frame++
	windowMin := l.minVal[l.minHead]

	// Instant attack, exponential release toward the window minimum.
	if windowMin < l.released {
		l.released = windowMin
	} else {
		l.released = windowMin + (l.released-windowMin)*l.releaseCoeff
	}

	// Average over the window: every value averaged is at most the gain
	// the outgoing frame needs, so the average is too.
	l.avgSum += l.released - l.avgBuf[l.avgPos]
	l.avgBuf[l.avgPos] = l.released
	l.avgPos++
	if l.avgPos == window {
		l.avgPos = 0
		// Recompute the sum once per window so rounding cannot drift.
		l.avgSum = 0
		for _, v := range l.avgBuf[:window] {
			l.avgSum += v
		}
	}
	g := math.Min(1.0, l.avgSum/float64(window))

	l.gainReduction = -20.0 * math.Log10(g)
	return g
}

// delay stores input in a channel's delay line and returns the sample
// leaving it.
func (l *Limiter) delay(line []float32, input float32) float32 {
	if l.delaySamples == 0 {
		return input
	}
	out := line[l.delayIndex]
	line[l.delayIndex] = input
	return out
}

func (l *Limiter) advanceDelay() {
	if l.delaySamples == 0 {
		return
	}
	l.delayIndex++
	if l.delayIndex == l.delaySamples {
		l.delayIndex = 0
	}
}

// Process processes a single sample
func (l *Limiter) Process(input float32) float32 {
	g := l.gain(l.peak(&l.historyL, input))
	out := l.delay(l.delayL, input)
	l.advanceDelay()
	return clampToCeiling(out*float32(g), l.ceiling)
}

// ProcessBuffer processes a buffer of samples
func (l *Limiter) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = l.Process(input[i])
	}
}

// ProcessStereo processes stereo buffers with linked limiting: both
// channels get the gain the louder one needs
func (l *Limiter) ProcessStereo(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		peak := math.Max(l.peak(&l.historyL, inputL[i]), l.peak(&l.historyR, inputR[i]))
		g := float32(l.gain(peak))
		outL := l.delay(l.delayL, inputL[i])
		outR := l.delay(l.delayR, inputR[i])
		l.advanceDelay()
		outputL[i] = clampToCeiling(outL*g, l.ceiling)
		outputR[i] = clampToCeiling(outR*g, l.ceiling)
	}
}

// clampToCeiling removes the float32 rounding of the gain multiply, which
// can leave a limited sample a few ulps above the ceiling.
func clampToCeiling(x float32, ceiling float64) float32 {
	c := float32(ceiling)
	if x > c {
		return c
	}
	if x < -c {
		return -c
	}
	return x
}

// Reset resets the limiter state
func (l *Limiter) Reset() {
	clear(l.delayL)
	clear(l.delayR)
	l.delayIndex = 0
	l.minHead = 0
	l.minLen = 0
	l.frame = 0
	for i := range l.avgBuf {
		l.avgBuf[i] = 1
	}
	l.avgSum = float64(l.delaySamples + 1)
	l.avgPos = 0
	l.released = 1
	l.historyL = [truePeakTaps]float32{}
	l.historyR = [truePeakTaps]float32{}
	l.gainReduction = 0.0
}
