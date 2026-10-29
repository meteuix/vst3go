package sampler

import (
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/dsp/envelope"
	"github.com/th-release/vst3go/pkg/dsp/resample"
)

// Voice plays one note of a Map. It implements voice.Voice; render its
// output with Render (stereo) or Process (mono). A mono sample plays in
// both channels.
//
// Reads are band-limited: pitching up reads a half-rate copy of the
// sample and a windowed-sinc kernel scaled to the read speed, so notes far
// above the root do not alias. The voice does not allocate.
type Voice struct {
	sampleRate float64
	instrument *atomic.Pointer[Map]
	kernel     *resample.Kernel
	env        *envelope.ADSR

	zone     *Zone
	note     uint8
	velocity uint8
	gain     float64
	active   bool
	age      int64
	level    float64 // last envelope value

	pos  float64 // in frames of the full-rate sample
	step float64 // frames per output sample
	bend float64 // pitch bend in semitones
}

// NewVoice returns a voice playing from the Map in instrument (read when
// a note starts, so the Map can be swapped while voices play).
func NewVoice(sampleRate float64, instrument *atomic.Pointer[Map]) *Voice {
	return &Voice{
		sampleRate: sampleRate,
		instrument: instrument,
		kernel:     resample.SincKernel(),
		env:        envelope.New(sampleRate),
	}
}

// SetADSR configures the amplitude envelope.
func (v *Voice) SetADSR(attack, decay, sustain, release float64) {
	v.env.SetADSR(attack, decay, sustain, release)
}

// SetPitchBend sets the pitch offset in semitones.
func (v *Voice) SetPitchBend(semitones float64) {
	v.bend = semitones
	v.updateStep()
}

func (v *Voice) IsActive() bool        { return v.active }
func (v *Voice) GetNote() uint8        { return v.note }
func (v *Voice) GetVelocity() uint8    { return v.velocity }
func (v *Voice) GetAmplitude() float64 { return v.gain * v.level }
func (v *Voice) GetAge() int64         { return v.age }

// TriggerNote starts a note from the beginning of its zone's sample. A
// note outside every zone stays silent.
func (v *Voice) TriggerNote(note, velocity uint8) {
	v.zone = v.instrument.Load().Find(note, velocity)
	if v.zone == nil {
		v.active = false
		return
	}
	v.note, v.velocity = note, velocity
	vel := float64(velocity) / 127
	v.gain = vel * vel * dbToGain(v.zone.Gain)
	v.pos, v.age = 0, 0
	v.active = true
	v.updateStep()
	v.env.Trigger()
}

// ChangeNote moves the voice to a new pitch without restarting the sample
// or envelope (legato).
func (v *Voice) ChangeNote(note uint8) {
	v.note = note
	v.updateStep()
}

func (v *Voice) ReleaseNote() { v.env.Release() }

func (v *Voice) Stop() {
	v.active = false
	v.zone = nil
	v.env.Reset()
	v.level = 0
}

func (v *Voice) updateStep() {
	if v.zone == nil {
		return
	}
	s := v.zone.Sample
	semis := float64(v.note) - s.rootNote + v.zone.Tune/100 + v.bend
	v.step = math.Exp2(semis/12) * s.sampleRate / v.sampleRate
}

// Process writes the mono output to output, overwriting it.
func (v *Voice) Process(output []float32) {
	clear(output)
	v.Render(output, nil)
}

// Render adds the voice to left and right (right may be nil for mono).
func (v *Voice) Render(left, right []float32) {
	if !v.active {
		return
	}
	s := v.zone.Sample
	// Read from the copy at which this step is between 1 and 2 frames.
	lvl := 0
	if v.step > 1 {
		lvl = min(len(s.levels)-1, int(math.Floor(math.Log2(v.step))))
	}
	scale := float64(int(1) << lvl)
	stepL := v.step / scale
	cutoff := math.Min(1, 1/stepL)
	data := s.levels[lvl]
	stereo := len(data) == 2 && right != nil
	for i := range left {
		e := float64(v.env.Next())
		v.level = e
		g := float32(v.gain * e)
		l, r := v.read(data, v.pos/scale, cutoff, scale, stereo)
		left[i] += l * g
		if right != nil {
			if stereo {
				right[i] += r * g
			} else {
				right[i] += l * g
			}
		}
		v.age++
		v.pos += v.step
		if s.Looped() {
			if v.pos >= float64(s.loopEnd) {
				v.pos -= float64(s.loopEnd - s.loopStart)
			}
		} else if v.pos >= float64(s.Len()) {
			v.Stop()
			return
		}
		if v.env.GetStage() == envelope.StageIdle {
			v.Stop()
			return
		}
	}
}

// read interpolates the level at position p (in its own frames) with the
// kernel scaled to cutoff, wrapping reads past the loop end.
func (v *Voice) read(data [][]float32, p, cutoff, scale float64, stereo bool) (float32, float32) {
	s := v.zone.Sample
	n := len(data[0])
	loopStart := int(math.Round(float64(s.loopStart) / scale))
	loopEnd := min(n, int(math.Round(float64(s.loopEnd)/scale)))
	looped := s.Looped() && loopEnd-loopStart >= 2
	span := float64(v.kernel.Zeros()) / cutoff
	first := int(math.Ceil(p - span))
	last := int(math.Floor(p + span))
	var l, r float64
	for i := first; i <= last; i++ {
		j := i
		if looped && j >= loopEnd {
			j = loopStart + (j-loopStart)%(loopEnd-loopStart)
		}
		if j < 0 || j >= n {
			continue
		}
		w := cutoff * v.kernel.Weight(cutoff*(p-float64(i)))
		l += w * float64(data[0][j])
		if stereo {
			r += w * float64(data[1][j])
		}
	}
	return float32(l), float32(r)
}
