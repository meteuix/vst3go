package dynamics

import (
	"math"
)

// Gate implements a noise gate with hysteresis and smooth operation
type Gate struct {
	sampleRate float64

	// Parameters
	threshold  float64 // Open threshold in dB
	hysteresis float64 // Hysteresis in dB (threshold difference for closing)
	attack     float64 // Attack time in seconds
	hold       float64 // Hold time in seconds
	release    float64 // Release time in seconds
	range_     float64 // Range in dB (max attenuation when closed)

	// Side-chain high-pass filter (optional), one state per channel
	hpfEnabled   bool
	hpfFrequency float64
	hpfCoeff     float64
	hpf          [2]onePoleHPF

	// Gate state machine
	state       gateState
	holdCounter int
	holdSamples int
	currentGain float64
	targetGain  float64
	closedGain  float64

	// Smooth gain transitions
	attackCoeff  float64
	releaseCoeff float64

	// State
	gateOpen      bool
	gainReduction float64 // For metering
}

// onePoleHPF is the state of a one-pole high-pass filter.
type onePoleHPF struct{ x1, y1 float64 }

// process runs y[n] = (1+a)/2 * (x[n] - x[n-1]) + a*y[n-1].
func (f *onePoleHPF) process(x, a float64) float64 {
	y := (1+a)/2*(x-f.x1) + a*f.y1
	f.x1, f.y1 = x, y
	return y
}

// gateState represents the current state of the gate
type gateState int

const (
	gateStateClosed gateState = iota
	gateStateAttack
	gateStateOpen
	gateStateHold
	gateStateRelease
)

// NewGate creates a new noise gate
func NewGate(sampleRate float64) *Gate {
	g := &Gate{
		sampleRate: sampleRate,
		threshold:  -40.0, // -40 dB default
		hysteresis: 5.0,   // 5 dB hysteresis
		attack:     0.001, // 1ms attack
		hold:       0.010, // 10ms hold
		release:    0.100, // 100ms release
		range_:     -80.0, // -80 dB range (practically mute)
		state:      gateStateClosed,
	}

	// Initialize gain to closed state
	g.closedGain = math.Pow(10.0, g.range_/20.0)
	g.currentGain = g.closedGain
	g.targetGain = g.currentGain
	g.gainReduction = g.range_

	// Update coefficients
	g.updateCoefficients()
	g.SetHold(g.hold) // Initialize hold samples

	return g
}

// SetThreshold sets the gate opening threshold in dB
func (g *Gate) SetThreshold(dB float64) {
	g.threshold = dB
}

// SetHysteresis sets the hysteresis in dB
func (g *Gate) SetHysteresis(dB float64) {
	g.hysteresis = max(0.0, dB)
}

// SetAttack sets the attack time in seconds
func (g *Gate) SetAttack(seconds float64) {
	g.attack = max(0.0, seconds)
	g.updateCoefficients()
}

// SetHold sets the hold time in seconds
func (g *Gate) SetHold(seconds float64) {
	g.hold = max(0.0, seconds)
	g.holdSamples = int(g.hold * g.sampleRate)
}

// SetRelease sets the release time in seconds
func (g *Gate) SetRelease(seconds float64) {
	g.release = max(0.0, seconds)
	g.updateCoefficients()
}

// SetRange sets the gate range (max attenuation) in dB
func (g *Gate) SetRange(dB float64) {
	g.range_ = min(0.0, dB) // Can't be positive
	g.closedGain = math.Pow(10.0, g.range_/20.0)

	switch g.state {
	case gateStateClosed:
		g.currentGain = g.closedGain
		g.targetGain = g.closedGain
		g.gainReduction = g.range_
	case gateStateRelease:
		g.targetGain = g.closedGain
	}
}

// SetSidechainFilter enables/disables the sidechain high-pass filter
func (g *Gate) SetSidechainFilter(enabled bool, frequency float64) {
	g.hpfEnabled = enabled
	g.hpfFrequency = max(20.0, min(frequency, g.sampleRate/2))
	g.hpfCoeff = math.Exp(-2.0 * math.Pi * g.hpfFrequency / g.sampleRate)
}

// updateCoefficients updates the smoothing coefficients
func (g *Gate) updateCoefficients() {
	// Attack and release coefficients for smooth gain changes
	// Using one-pole smoothing: coeff = exp(-1 / (time * sampleRate))
	if g.attack > 0 {
		g.attackCoeff = math.Exp(-1.0 / (g.attack * g.sampleRate))
	} else {
		g.attackCoeff = 0.0 // Instant attack
	}

	if g.release > 0 {
		g.releaseCoeff = math.Exp(-1.0 / (g.release * g.sampleRate))
	} else {
		g.releaseCoeff = 0.0 // Instant release
	}
}

// detect returns the detection level of one channel's sample, through the
// side-chain filter when it is enabled.
func (g *Gate) detect(ch int, input float32) float64 {
	x := float64(input)
	if g.hpfEnabled {
		x = g.hpf[ch].process(x, g.hpfCoeff)
	}
	return math.Abs(x)
}

// step runs the gate for one sample with the given detection level and
// returns the gain to apply. Detection is instantaneous; the hold time keeps
// the gate open through the zero crossings of low notes.
func (g *Gate) step(level float64) float32 {
	inputDB := -96.0
	if level > 0 {
		inputDB = gainToDB(level)
	}
	closeDB := g.threshold - g.hysteresis

	switch g.state {
	case gateStateClosed:
		if inputDB > g.threshold {
			g.state = gateStateAttack
			g.targetGain = 1.0
		}
	case gateStateAttack:
		if inputDB < closeDB {
			g.state = gateStateRelease
			g.targetGain = g.closedGain
		}
	case gateStateOpen:
		if inputDB < closeDB {
			g.state = gateStateHold
			g.holdCounter = g.holdSamples
		}
	case gateStateHold:
		if inputDB > closeDB {
			g.state = gateStateOpen
		} else if g.holdCounter > 0 {
			g.holdCounter--
		} else {
			g.state = gateStateRelease
			g.targetGain = g.closedGain
			g.gateOpen = false
		}
	case gateStateRelease:
		if inputDB > g.threshold {
			g.state = gateStateAttack
			g.targetGain = 1.0
		}
	}

	// Smooth gain transitions
	coeff := g.releaseCoeff
	if g.currentGain < g.targetGain {
		coeff = g.attackCoeff
	}
	g.currentGain = g.targetGain + (g.currentGain-g.targetGain)*coeff

	if g.state == gateStateAttack && g.currentGain >= 0.99 {
		g.state = gateStateOpen
		g.gateOpen = true
	} else if g.state == gateStateRelease && g.currentGain <= g.closedGain*1.01 {
		g.state = gateStateClosed
		g.currentGain = g.closedGain
	}

	// Gain reduction for metering
	g.gainReduction = gainToDB(g.currentGain)
	if g.gainReduction > -0.1 {
		g.gainReduction = 0.0
	}
	return float32(g.currentGain)
}

// Process processes a single sample
func (g *Gate) Process(input float32) float32 {
	return input * g.step(g.detect(0, input))
}

// ProcessBuffer processes a buffer of samples
func (g *Gate) ProcessBuffer(input, output []float32) {
	for i := range input {
		output[i] = g.Process(input[i])
	}
}

// ProcessStereo processes stereo buffers with linked gating: the louder
// channel opens and closes the gate for both
func (g *Gate) ProcessStereo(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		l, r := inputL[i], inputR[i]
		gain := g.step(max(g.detect(0, l), g.detect(1, r)))
		outputL[i] = l * gain
		outputR[i] = r * gain
	}
}

// ProcessSidechain gates input by the level of a separate key signal
func (g *Gate) ProcessSidechain(input, key, output []float32) {
	for i := range input {
		output[i] = input[i] * g.step(g.detect(0, key[i]))
	}
}

// ProcessStereoSidechain gates a stereo pair by a stereo key signal: the
// louder key channel, through the side-chain filter, drives both channels.
// For a mono key pass the same slice as keyL and keyR.
func (g *Gate) ProcessStereoSidechain(inputL, inputR, keyL, keyR, outputL, outputR []float32) {
	for i := range inputL {
		gain := g.step(max(g.detect(0, keyL[i]), g.detect(1, keyR[i])))
		outputL[i] = inputL[i] * gain
		outputR[i] = inputR[i] * gain
	}
}

// GetGainReduction returns the current gain reduction in dB
func (g *Gate) GetGainReduction() float64 {
	return g.gainReduction
}

// IsOpen returns true if the gate is currently open
func (g *Gate) IsOpen() bool {
	return g.gateOpen
}

// GetState returns the current gate state for debugging
func (g *Gate) GetState() string {
	switch g.state {
	case gateStateClosed:
		return "closed"
	case gateStateAttack:
		return "attack"
	case gateStateOpen:
		return "open"
	case gateStateHold:
		return "hold"
	case gateStateRelease:
		return "release"
	default:
		return "unknown"
	}
}

// Reset resets the gate state
func (g *Gate) Reset() {
	g.state = gateStateClosed
	g.currentGain = g.closedGain
	g.targetGain = g.currentGain
	g.holdCounter = 0
	g.gateOpen = false
	g.gainReduction = g.range_
	g.hpf = [2]onePoleHPF{}
}
