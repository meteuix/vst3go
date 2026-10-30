package main

import (
	"github.com/meteuix/vst3go/v2/pkg/dsp/envelope"
	"github.com/meteuix/vst3go/v2/pkg/dsp/oscillator"
	"github.com/meteuix/vst3go/v2/pkg/midi"
)

// synthVoice is a sine oscillator with an ADSR amplitude envelope. It
// implements voice.Voice.
type synthVoice struct {
	osc    *oscillator.Oscillator
	ampEnv *envelope.ADSR

	note      uint8
	velocity  uint8
	amplitude float64
	active    bool
	age       int64
}

func newSynthVoice(sampleRate float64) *synthVoice {
	return &synthVoice{
		osc:    oscillator.New(sampleRate),
		ampEnv: envelope.New(sampleRate),
	}
}

func (v *synthVoice) IsActive() bool        { return v.active }
func (v *synthVoice) GetNote() uint8        { return v.note }
func (v *synthVoice) GetVelocity() uint8    { return v.velocity }
func (v *synthVoice) GetAmplitude() float64 { return v.amplitude }
func (v *synthVoice) GetAge() int64         { return v.age }

func (v *synthVoice) TriggerNote(note uint8, velocity uint8) {
	v.note = note
	v.velocity = velocity
	v.amplitude = float64(velocity) / 127.0
	v.active = true
	v.age = 0
	v.osc.SetFrequency(midi.NoteToFrequency(note, 440.0))
	v.ampEnv.Trigger()
}

// ChangeNote moves the sounding voice to a new pitch without retriggering
// the envelope (legato).
func (v *synthVoice) ChangeNote(note uint8) {
	v.note = note
	v.osc.SetFrequency(midi.NoteToFrequency(note, 440.0))
}

func (v *synthVoice) ReleaseNote() {
	v.ampEnv.Release()
}

func (v *synthVoice) Stop() {
	v.active = false
	v.ampEnv.Reset()
	v.osc.Reset()
	v.note = 0
	v.age = 0
}

// Process writes the voice output to output, overwriting it.
func (v *synthVoice) Process(output []float32) {
	if !v.active {
		clear(output)
		return
	}
	amp := float32(v.amplitude)
	for i := range output {
		if !v.active {
			output[i] = 0
			continue
		}
		output[i] = v.osc.Sine() * amp * v.ampEnv.Next()
		v.age++
		if v.ampEnv.GetStage() == envelope.StageIdle {
			v.active = false
		}
	}
}

// SetADSR configures the amplitude envelope.
func (v *synthVoice) SetADSR(attack, decay, sustain, release float64) {
	v.ampEnv.SetADSR(attack, decay, sustain, release)
}
