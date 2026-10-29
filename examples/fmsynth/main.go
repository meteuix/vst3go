// Command fmsynth is an 8-voice, four-operator FM (phase modulation)
// synthesizer. Algorithm wires the operators: Stack (4→3→2→1), Pairs
// (4→3 and 2→1), Branch (2, 3 and 4 into 1) or Organ (all four heard).
// Each operator has a frequency ratio and a level; a modulator's level is
// its modulation index (up to 8 radians), a carrier's its loudness.
// Operator 4 has feedback. Carriers follow the amplitude ADSR; modulators
// follow a decay/sustain envelope so the brightness can fall over a note.
//
// Build: scripts/build_vst3.sh examples/fmsynth FMSynth
package main

import (
	"fmt"

	"github.com/th-release/vst3go/pkg/dsp/envelope"
	"github.com/th-release/vst3go/pkg/dsp/oscillator"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/framework/voice"
	"github.com/th-release/vst3go/pkg/midi"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramAlgorithm uint32 = iota
	paramRatio1
	paramRatio2
	paramRatio3
	paramRatio4
	paramLevel1
	paramLevel2
	paramLevel3
	paramLevel4
	paramFeedback
	paramAttack
	paramDecay
	paramSustain
	paramRelease
	paramModDecay
	paramModSustain
	paramVolume
)

const (
	algStack = iota
	algPairs
	algBranch
	algOrgan
)

const (
	numVoices = 8
	maxIndex  = 8.0 // radians of modulation at level 100 %
)

type fmPlugin struct{}

func (fmPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.fmsynth",
		Name:     "FM Synth",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryInstrumentSynth,
	}
}

func percent(id uint32, name string, def float64) *param.Builder {
	return param.New(id, name).Range(0, 100).Default(def).Unit("%").
		Formatter(param.PercentFormatter, param.PercentParser)
}

func (fmPlugin) CreateProcessor() plugin.Processor {
	p := &fmProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewGenerator())}
	reg := p.Parameters()
	reg.Add(param.Choice(paramAlgorithm, "Algorithm", []param.ChoiceOption{
		{Value: algStack, Name: "Stack"},
		{Value: algPairs, Name: "Pairs"},
		{Value: algBranch, Name: "Branch"},
		{Value: algOrgan, Name: "Organ"},
	}).Build())
	for op, def := range []float64{1, 1, 2, 3} {
		reg.Add(param.New(paramRatio1+uint32(op), fmt.Sprintf("Op%d Ratio", op+1)).
			Range(0.5, 16).Default(def).Build())
	}
	for op, def := range []float64{100, 30, 0, 0} {
		reg.Add(percent(paramLevel1+uint32(op), fmt.Sprintf("Op%d Level", op+1), def).Build())
	}
	reg.Add(
		percent(paramFeedback, "Feedback", 0).Build(),
		param.New(paramAttack, "Attack").Range(0.001, 2).Default(0.002).Unit("s").Build(),
		param.New(paramDecay, "Decay").Range(0.001, 4).Default(0.5).Unit("s").Build(),
		percent(paramSustain, "Sustain", 70).Build(),
		param.New(paramRelease, "Release").Range(0.001, 5).Default(0.3).Unit("s").Build(),
		param.New(paramModDecay, "Mod Decay").Range(0.001, 4).Default(0.8).Unit("s").Build(),
		percent(paramModSustain, "Mod Sustain", 40).Build(),
		percent(paramVolume, "Volume", 50).Smooth(0.01).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.allocator.Reset() })
	return p
}

// patch is the sound shared by every voice for a block.
type patch struct {
	algorithm int
	ratio     [4]float64
	level     [4]float64 // 0..1
	feedback  float64
}

type fmProcessor struct {
	*frameworkplugin.BaseProcessor
	voices    [numVoices]*fmVoice
	allocator *voice.Allocator
	scratch   []float32
	patch     patch
	env       [6]float64
}

func (p *fmProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	vs := make([]voice.Voice, numVoices)
	for i := range p.voices {
		v := &fmVoice{amp: envelope.New(sampleRate), mod: envelope.New(sampleRate), patch: &p.patch}
		for op := range v.ops {
			v.ops[op] = oscillator.NewOperator(sampleRate)
		}
		p.voices[i] = v
		vs[i] = v
	}
	p.allocator = voice.NewAllocator(vs)
	p.scratch = make([]float32, maxBlockSize)
	p.env = [6]float64{-1}
	return nil
}

// MIDIControllerChannels delivers the sustain pedal and All Notes Off.
func (p *fmProcessor) MIDIControllerChannels() int { return 1 }

func (p *fmProcessor) GetTailSamples() int32 { return int32(5 * p.SampleRate()) }

func (p *fmProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) == 0 || n == 0 {
		return
	}
	ctx.Clear()
	p.patch.algorithm = int(ctx.ParamPlain(paramAlgorithm))
	for op := range 4 {
		p.patch.ratio[op] = ctx.ParamPlain(paramRatio1 + uint32(op))
		p.patch.level[op] = ctx.ParamPlain(paramLevel1+uint32(op)) / 100
	}
	p.patch.feedback = ctx.ParamPlain(paramFeedback) / 100 * 1.5
	env := [6]float64{
		ctx.ParamPlain(paramAttack), ctx.ParamPlain(paramDecay),
		ctx.ParamPlain(paramSustain) / 100, ctx.ParamPlain(paramRelease),
		ctx.ParamPlain(paramModDecay), ctx.ParamPlain(paramModSustain) / 100,
	}
	if env != p.env {
		p.env = env
		for _, v := range p.voices {
			v.amp.SetADSR(env[0], env[1], env[2], env[3])
			v.mod.SetADSR(env[0], env[4], env[5], env[3])
		}
	}

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

func (p *fmProcessor) render(out []float32) {
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

// fmVoice implements voice.Voice with four operators.
type fmVoice struct {
	ops      [4]*oscillator.Operator
	amp, mod *envelope.ADSR
	patch    *patch
	note     uint8
	velocity uint8
	gain     float64
	active   bool
	age      int64
}

func (v *fmVoice) IsActive() bool        { return v.active }
func (v *fmVoice) GetNote() uint8        { return v.note }
func (v *fmVoice) GetVelocity() uint8    { return v.velocity }
func (v *fmVoice) GetAmplitude() float64 { return v.gain }
func (v *fmVoice) GetAge() int64         { return v.age }

func (v *fmVoice) TriggerNote(note, velocity uint8) {
	v.note, v.velocity = note, velocity
	v.gain = float64(velocity) / 127
	v.active, v.age = true, 0
	for _, o := range v.ops {
		o.Reset()
	}
	v.amp.Trigger()
	v.mod.Trigger()
}

func (v *fmVoice) ChangeNote(note uint8) { v.note = note }

func (v *fmVoice) ReleaseNote() {
	v.amp.Release()
	v.mod.Release()
}

func (v *fmVoice) Stop() {
	v.active = false
	v.amp.Reset()
	v.mod.Reset()
}

// Process writes the voice to output, overwriting it.
func (v *fmVoice) Process(output []float32) {
	pt := v.patch
	f := midi.NoteToFrequency(v.note, 440)
	for op, o := range v.ops {
		o.SetRatio(pt.ratio[op])
	}
	v.ops[3].SetFeedback(pt.feedback)
	l := pt.level
	for i := range output {
		if !v.active {
			output[i] = 0
			continue
		}
		a := float64(v.amp.Next())
		m := float64(v.mod.Next()) * maxIndex
		var y float64
		switch pt.algorithm {
		case algStack:
			o4 := v.ops[3].Next(f, 0)
			o3 := v.ops[2].Next(f, o4*l[3]*m)
			o2 := v.ops[1].Next(f, o3*l[2]*m)
			y = v.ops[0].Next(f, o2*l[1]*m) * l[0]
		case algPairs:
			o4 := v.ops[3].Next(f, 0)
			o2 := v.ops[1].Next(f, 0)
			y = 0.5 * (v.ops[0].Next(f, o2*l[1]*m)*l[0] + v.ops[2].Next(f, o4*l[3]*m)*l[2])
		case algBranch:
			mod := v.ops[1].Next(f, 0)*l[1] + v.ops[2].Next(f, 0)*l[2] + v.ops[3].Next(f, 0)*l[3]
			y = v.ops[0].Next(f, mod*m) * l[0]
		default: // algOrgan
			for op, o := range v.ops {
				y += o.Next(f, 0) * l[op]
			}
			y *= 0.25
		}
		output[i] = float32(y * a * v.gain)
		v.age++
		if v.amp.GetStage() == envelope.StageIdle {
			v.active = false
		}
	}
}

func init() {
	plugin.Register(fmPlugin{})
}

func main() { standalone.Main() }
