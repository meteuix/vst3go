// Command wavetablesynth is an 8-voice wavetable synthesizer: each voice
// plays two detuned band-limited wavetable oscillators morphing through
// sine, triangle, sawtooth and square (Position), with an ADSR envelope.
// The wavetable bank is built once in Initialize and shared by every
// voice; notes are sample-accurate and stolen oldest-first.
//
// Build: scripts/build_vst3.sh examples/wavetablesynth WavetableSynth
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/envelope"
	"github.com/meteuix/vst3go/v2/pkg/dsp/oscillator"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/framework/voice"
	"github.com/meteuix/vst3go/v2/pkg/midi"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramPosition uint32 = iota
	paramDetune
	paramAttack
	paramDecay
	paramSustain
	paramRelease
	paramVolume
)

const numVoices = 8

type wavetablePlugin struct{}

func (wavetablePlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.wavetablesynth",
		Name:     "Wavetable Synth",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryInstrumentSynth,
	}
}

func (wavetablePlugin) CreateProcessor() plugin.Processor {
	p := &wavetableProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewGenerator())}
	p.Parameters().Add(
		param.New(paramPosition, "Position").Range(0, 100).Default(0).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.New(paramDetune, "Detune").Range(0, 50).Default(7).Unit("ct").Build(),
		param.New(paramAttack, "Attack").Range(0.001, 2).Default(0.005).Logarithmic().Unit("s").Build(),
		param.New(paramDecay, "Decay").Range(0.001, 2).Default(0.2).Logarithmic().Unit("s").Build(),
		param.New(paramSustain, "Sustain").Range(0, 100).Default(80).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.New(paramRelease, "Release").Range(0.001, 5).Default(0.2).Logarithmic().Unit("s").Build(),
		param.New(paramVolume, "Volume").Range(0, 100).Default(50).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Smooth(0.01).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.allocator.Reset() })
	return p
}

type wavetableProcessor struct {
	*frameworkplugin.BaseProcessor
	voices    [numVoices]*wavetableVoice
	allocator *voice.Allocator
	scratch   []float32
	env       [4]float64 // attack, decay, sustain, release last applied
}

func (p *wavetableProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	bank, err := oscillator.NewWavetableBank(oscillator.BasicShapes())
	if err != nil {
		return err
	}
	vs := make([]voice.Voice, numVoices)
	for i := range p.voices {
		p.voices[i] = &wavetableVoice{
			osc: [2]*oscillator.Wavetable{oscillator.NewWavetable(bank, sampleRate), oscillator.NewWavetable(bank, sampleRate)},
			env: envelope.New(sampleRate),
		}
		vs[i] = p.voices[i]
	}
	p.allocator = voice.NewAllocator(vs)
	p.scratch = make([]float32, maxBlockSize)
	p.env = [4]float64{-1}
	return nil
}

// MIDIControllerChannels delivers the sustain pedal and All Notes Off.
func (p *wavetableProcessor) MIDIControllerChannels() int { return 1 }

func (p *wavetableProcessor) GetTailSamples() int32 { return int32(5 * p.SampleRate()) }

func (p *wavetableProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) == 0 || n == 0 {
		return
	}
	ctx.Clear()
	env := [4]float64{
		ctx.ParamPlain(paramAttack), ctx.ParamPlain(paramDecay),
		ctx.ParamPlain(paramSustain) / 100, ctx.ParamPlain(paramRelease),
	}
	position := ctx.ParamPlain(paramPosition) / 100
	detune := ctx.ParamPlain(paramDetune)
	for _, v := range p.voices {
		if env != p.env {
			v.env.SetADSR(env[0], env[1], env[2], env[3])
		}
		v.position, v.detune = position, detune
	}
	p.env = env

	left := ctx.Output[0]
	events := ctx.GetAllInputEvents()
	offset := ctx.BlockOffset()
	next := 0
	for start := 0; start < n; {
		for next < len(events) && int(events[next].Offset)-offset <= start {
			p.allocator.ProcessEvent(events[next])
			next++
		}
		end := n
		if next < len(events) {
			end = min(int(events[next].Offset)-offset, n)
		}
		p.render(left[start:end])
		start = end
	}
	for ; next < len(events); next++ {
		p.allocator.ProcessEvent(events[next])
	}
	volume := ctx.Smoothed(paramVolume)
	for i := range left[:n] {
		left[i] *= float32(volume.Next() / 100)
	}
	for ch := 1; ch < len(ctx.Output); ch++ {
		copy(ctx.Output[ch], left)
	}
}

func (p *wavetableProcessor) render(out []float32) {
	if len(out) == 0 {
		return
	}
	buf := p.scratch[:len(out)]
	for _, v := range p.voices {
		if v.active {
			v.Process(buf)
			for i, x := range buf {
				out[i] += x
			}
		}
	}
}

// wavetableVoice implements voice.Voice with two detuned oscillators.
type wavetableVoice struct {
	osc      [2]*oscillator.Wavetable
	env      *envelope.ADSR
	note     uint8
	velocity uint8
	gain     float64
	active   bool
	age      int64

	position, detune float64
}

func (v *wavetableVoice) IsActive() bool        { return v.active }
func (v *wavetableVoice) GetNote() uint8        { return v.note }
func (v *wavetableVoice) GetVelocity() uint8    { return v.velocity }
func (v *wavetableVoice) GetAmplitude() float64 { return v.gain }
func (v *wavetableVoice) GetAge() int64         { return v.age }

func (v *wavetableVoice) TriggerNote(note, velocity uint8) {
	v.note, v.velocity = note, velocity
	v.gain = float64(velocity) / 127
	v.active, v.age = true, 0
	v.osc[0].Reset()
	v.osc[1].Reset()
	v.env.Trigger()
}

func (v *wavetableVoice) ChangeNote(note uint8) { v.note = note }
func (v *wavetableVoice) ReleaseNote()          { v.env.Release() }

func (v *wavetableVoice) Stop() {
	v.active = false
	v.env.Reset()
}

// Process writes the voice to output, overwriting it.
func (v *wavetableVoice) Process(output []float32) {
	f := midi.NoteToFrequency(v.note, 440)
	spread := math.Exp2(v.detune / 2400) // half the detune each way
	v.osc[0].SetFrequency(f / spread)
	v.osc[1].SetFrequency(f * spread)
	for _, o := range v.osc {
		o.SetPosition(v.position)
	}
	g := float32(v.gain * 0.5)
	for i := range output {
		if !v.active {
			output[i] = 0
			continue
		}
		output[i] = (v.osc[0].Next() + v.osc[1].Next()) * g * v.env.Next()
		v.age++
		if v.env.GetStage() == envelope.StageIdle {
			v.active = false
		}
	}
}

func init() {
	plugin.Register(wavetablePlugin{})
}

func main() { standalone.Main() }
