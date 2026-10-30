// Command pitchshifter shifts pitch by up to two octaves either way, with
// two engines: Spectral (a phase vocoder: clean on sustained and tonal
// material) and Granular (crossfading delay-line grains: tighter on
// transients and speech, with Window setting the grain length). The plugin
// reports the phase vocoder's frame as its latency in both modes and
// delays the granular engine and the dry signal to match, so switching
// modes or mixing never shifts the timing.
//
// Build: scripts/build_vst3.sh examples/pitchshifter PitchShifter
package main

import (
	"github.com/th-release/vst3go/v2/pkg/dsp/pitch"
	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const (
	paramMode uint32 = iota
	paramSemitones
	paramFine
	paramWindow
	paramMix
)

const (
	modeSpectral = 0
	modeGranular = 1
	maxWindow    = 0.08 // seconds; half of it stays within the latency
)

type pitchShifterPlugin struct{}

func (pitchShifterPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.pitchshifter",
		Name:     "Pitch Shifter",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxPitchShift,
	}
}

func (pitchShifterPlugin) CreateProcessor() plugin.Processor {
	p := &pitchShifterProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.Choice(paramMode, "Mode", []param.ChoiceOption{
			{Value: modeSpectral, Name: "Spectral"},
			{Value: modeGranular, Name: "Granular"},
		}).Build(),
		param.New(paramSemitones, "Semitones").Range(-24, 24).Default(0).Steps(48).Unit("st").Build(),
		param.New(paramFine, "Fine").Range(-100, 100).Default(0).Unit("ct").Build(),
		param.TimeParameter(paramWindow, "Window", 10, maxWindow*1000, 50).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type pitchShifterProcessor struct {
	*frameworkplugin.BaseProcessor
	spectral [2]*pitch.SpectralShifter
	granular [2]*pitch.Shifter
	mode     int

	latency int
	align   [2][]float32 // granular output delayed to the latency
	dry     [2][]float32 // input delayed by the latency
	pos     int          // shared write position of both delays
	wet     []float32
	mix     []float32
}

func (p *pitchShifterProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	// About 43 ms of frame at any rate, which also covers the granular
	// taps (up to maxWindow/2 back).
	size := 2048
	for sampleRate > 50000*float64(size)/2048 {
		size *= 2
	}
	for ch := range 2 {
		s, err := pitch.NewSpectralShifter(size)
		if err != nil {
			return err
		}
		p.spectral[ch] = s
		p.granular[ch] = pitch.NewShifter(sampleRate, maxWindow)
	}
	p.latency = p.spectral[0].Latency()
	for ch := range 2 {
		p.align[ch] = make([]float32, p.latency)
		p.dry[ch] = make([]float32, p.latency)
	}
	p.wet = make([]float32, maxBlockSize)
	p.mix = make([]float32, maxBlockSize)
	p.mode = -1
	return nil
}

func (p *pitchShifterProcessor) reset() {
	for ch := range 2 {
		p.spectral[ch].Reset()
		p.granular[ch].Reset()
		clear(p.align[ch])
		clear(p.dry[ch])
	}
	p.pos = 0
}

func (p *pitchShifterProcessor) GetLatencySamples() int32 { return int32(p.latency) }

func (p *pitchShifterProcessor) GetTailSamples() int32 { return int32(p.latency) }

func (p *pitchShifterProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	n := ctx.NumSamples()
	mode := int(ctx.ParamPlain(paramMode))
	if mode != p.mode {
		// The engine taking over starts from a clean state.
		p.reset()
		p.mode = mode
	}
	semis := ctx.ParamPlain(paramSemitones) + ctx.ParamPlain(paramFine)/100
	window := ctx.ParamPlain(paramWindow) / 1000
	// The granular taps sit on average 2 + window/2 samples back; the
	// rest of the latency is made up by the alignment delay.
	grainDelay := 2 + int(window*p.SampleRate()/2)
	mix := ctx.Smoothed(paramMix)
	for i := range n {
		p.mix[i] = float32(mix.Next() / 100)
	}

	wet := p.wet[:n]
	pos := p.pos
	for ch := range 2 {
		in, out := ctx.Input[ch][:n], ctx.Output[ch][:n]
		if mode == modeGranular {
			g := p.granular[ch]
			g.SetSemitones(semis)
			g.SetWindow(window)
			g.Process(in, wet)
		} else {
			s := p.spectral[ch]
			s.SetSemitones(semis)
			s.Process(in, wet)
		}
		// Delay the dry input (and the granular output) to the latency;
		// the input is read before the output is written, as the host
		// may process in place.
		align, dry := p.align[ch], p.dry[ch]
		size := len(dry)
		pos = p.pos
		for i := range n {
			d := dry[pos]
			dry[pos] = in[i]
			w := wet[i]
			if mode == modeGranular {
				align[pos] = w
				w = align[(pos+grainDelay)%size]
			}
			pos = (pos + 1) % size
			m := p.mix[i]
			out[i] = d*(1-m) + w*m
		}
	}
	p.pos = pos
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(pitchShifterPlugin{})
}

func main() { standalone.Main() }
