package dynamics

import "math"

// Detector time constants of the TransientShaper, in seconds.
const (
	transientFastAttack = 0.001 // follows onsets
	transientSlowAttack = 0.025 // lags onsets: the gap is the attack phase
	transientRelease    = 0.100 // shared by the attack pair
	sustainShortRelease = 0.025 // follows decays
	sustainLongRelease  = 0.250 // lags decays: the gap is the sustain phase
	transientMaxGainDB  = 24.0
	transientFloorDB    = -90.0
)

// follower is a one-pole peak envelope follower with separate attack and
// release coefficients.
type follower struct{ env, attack, release float64 }

func (f *follower) set(sampleRate, attack, release float64) {
	f.attack = math.Exp(-1 / (attack * sampleRate))
	f.release = math.Exp(-1 / (release * sampleRate))
}

func (f *follower) step(level float64) float64 {
	c := f.release
	if level > f.env {
		c = f.attack
	}
	f.env = level + (f.env-level)*c
	return f.env
}

func followerDB(env float64) float64 {
	return max(transientFloorDB, gainToDB(env+1e-12))
}

// TransientShaper changes the level of a signal's attacks and sustain
// independently of its absolute level, like an SPL Transient Designer. Two
// envelope followers with different attack times differ during onsets; two
// with different release times differ during decays. Their differences in
// dB, scaled by the Attack and Sustain amounts, give the gain: +1 doubles
// the dB contrast of the phase, -1 flattens it. Steady signals pass at
// unity. Stereo processing is linked.
type TransientShaper struct {
	attackAmount  float64
	sustainAmount float64

	fast, slow  follower // attack detection
	short, long follower // sustain detection
	gainDB      float64
}

// NewTransientShaper creates a transient shaper with neutral settings.
func NewTransientShaper(sampleRate float64) *TransientShaper {
	t := &TransientShaper{}
	t.fast.set(sampleRate, transientFastAttack, transientRelease)
	t.slow.set(sampleRate, transientSlowAttack, transientRelease)
	t.short.set(sampleRate, transientFastAttack, sustainShortRelease)
	t.long.set(sampleRate, transientFastAttack, sustainLongRelease)
	return t
}

// SetAttack sets the attack amount, -1 (softer) to 1 (punchier).
func (t *TransientShaper) SetAttack(amount float64) {
	t.attackAmount = max(-1, min(1, amount))
}

// SetSustain sets the sustain amount, -1 (drier) to 1 (longer tails).
func (t *TransientShaper) SetSustain(amount float64) {
	t.sustainAmount = max(-1, min(1, amount))
}

// GainDB returns the gain applied to the last sample, in dB.
func (t *TransientShaper) GainDB() float64 { return t.gainDB }

// gain runs the detectors on one detection level and returns the linear gain.
func (t *TransientShaper) gain(level float64) float32 {
	attack := followerDB(t.fast.step(level)) - followerDB(t.slow.step(level))
	sustain := followerDB(t.long.step(level)) - followerDB(t.short.step(level))
	db := t.attackAmount*attack + t.sustainAmount*sustain
	t.gainDB = max(-transientMaxGainDB, min(transientMaxGainDB, db))
	return float32(dbToGain(t.gainDB))
}

// ProcessBuffer shapes a mono buffer. input and output may be the same.
func (t *TransientShaper) ProcessBuffer(input, output []float32) {
	for i, x := range input {
		output[i] = x * t.gain(math.Abs(float64(x)))
	}
}

// ProcessStereo shapes a stereo pair with one gain from the louder
// channel. Inputs and outputs may be the same slices.
func (t *TransientShaper) ProcessStereo(inputL, inputR, outputL, outputR []float32) {
	for i := range inputL {
		l, r := inputL[i], inputR[i]
		g := t.gain(max(math.Abs(float64(l)), math.Abs(float64(r))))
		outputL[i] = l * g
		outputR[i] = r * g
	}
}

// Reset clears the detectors.
func (t *TransientShaper) Reset() {
	t.fast.env, t.slow.env, t.short.env, t.long.env = 0, 0, 0, 0
	t.gainDB = 0
}
