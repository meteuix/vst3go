// Command auto_params shows automatic parameter IDs (param.AutoRegistry):
// a channel strip whose standard controls, compressor and three EQ bands
// are registered by name with IDs assigned in order. The processor
// resolves every ID once when it is created and reads values by ID on the
// audio thread, so names are never looked up while processing.
//
// Build: scripts/build_vst3.sh examples/auto_params AutoParams
package main

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/v2/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/v2/pkg/dsp/filter"
	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/standalone"
)

const (
	numBands    = 3
	numChannels = 2
)

// EQ band types, in the order of the "Band N Type" choices.
const (
	bandBell = iota
	bandLowShelf
	bandHighShelf
	bandLowPass
	bandHighPass
	bandNotch
)

type autoParamsPlugin struct{}

func (autoParamsPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.autoparams",
		Name:     "AutoParams",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFx,
	}
}

func (autoParamsPlugin) CreateProcessor() plugin.Processor {
	p, err := newAutoParamsProcessor()
	if err != nil {
		panic(err) // the parameter set is fixed; this is a programming error
	}
	return p
}

// bandIDs are the parameter IDs of one EQ band.
type bandIDs struct{ enable, freq, gain, q, kind uint32 }

// bandState is the EQ band setting the filter was last designed for.
type bandState struct {
	enabled            bool
	freq, gain, q      float64
	kind               int
	designedSampleRate float64
}

type autoParamsProcessor struct {
	params *param.AutoRegistry
	buses  *bus.Configuration

	// Parameter IDs, resolved from their names once.
	bypass, mix, inputGain, outputGain uint32
	threshold, ratio, attack, release  uint32
	knee, autoGain, warmth             uint32
	bands                              [numBands]bandIDs

	sampleRate float64
	compressor *dynamics.Compressor
	eq         [numBands]*filter.Biquad
	eqState    [numBands]bandState
}

func newAutoParamsProcessor() (*autoParamsProcessor, error) {
	reg := param.NewAutoRegistry()
	if err := reg.RegisterStandardControls(); err != nil {
		return nil, err
	}
	if err := reg.RegisterCompressorControls(); err != nil {
		return nil, err
	}
	for band := 1; band <= numBands; band++ {
		if err := reg.RegisterEQBand(band); err != nil {
			return nil, err
		}
	}
	// Individual parameters get the next free IDs too.
	if err := reg.Register(param.DepthParameter(0, "Warmth").Default(0).Build()); err != nil {
		return nil, err
	}

	p := &autoParamsProcessor{params: reg, buses: bus.NewEffectStereo()}
	ids := map[string]*uint32{
		"Bypass": &p.bypass, "Mix": &p.mix, "Input Gain": &p.inputGain, "Output Gain": &p.outputGain,
		"Threshold": &p.threshold, "Ratio": &p.ratio, "Attack": &p.attack, "Release": &p.release,
		"Knee": &p.knee, "Auto Gain": &p.autoGain, "Warmth": &p.warmth,
	}
	for i := range p.bands {
		prefix := fmt.Sprintf("Band %d ", i+1)
		ids[prefix+"Enable"] = &p.bands[i].enable
		ids[prefix+"Frequency"] = &p.bands[i].freq
		ids[prefix+"Gain"] = &p.bands[i].gain
		ids[prefix+"Q"] = &p.bands[i].q
		ids[prefix+"Type"] = &p.bands[i].kind
	}
	for name, dst := range ids {
		id, ok := reg.GetID(name)
		if !ok {
			return nil, fmt.Errorf("auto_params: no parameter %q", name)
		}
		*dst = id
	}
	for i := range p.eq {
		p.eq[i] = filter.NewBiquad(numChannels)
	}
	return p, nil
}

func (p *autoParamsProcessor) GetParameters() *param.Registry { return p.params.Registry }
func (p *autoParamsProcessor) GetBuses() *bus.Configuration   { return p.buses }
func (p *autoParamsProcessor) GetLatencySamples() int32       { return 0 }
func (p *autoParamsProcessor) GetTailSamples() int32          { return 0 }

func (p *autoParamsProcessor) Initialize(sampleRate float64, _ int32) error {
	p.sampleRate = sampleRate
	p.compressor = dynamics.NewCompressor(sampleRate)
	p.eqState = [numBands]bandState{}
	return nil
}

func (p *autoParamsProcessor) SetActive(active bool) error {
	if !active && p.compressor != nil {
		p.compressor.Reset()
		for _, f := range p.eq {
			f.Reset()
		}
	}
	return nil
}

func dbToGain(db float64) float32 {
	if db <= -80 {
		return 0
	}
	return float32(math.Pow(10, db/20))
}

// updateBand redesigns an EQ band's filter when its settings changed.
func (p *autoParamsProcessor) updateBand(ctx *process.Context, i int) {
	ids := p.bands[i]
	s := bandState{
		enabled:            ctx.ParamPlain(ids.enable) >= 0.5,
		freq:               min(ctx.ParamPlain(ids.freq), p.sampleRate*0.45),
		gain:               ctx.ParamPlain(ids.gain),
		q:                  ctx.ParamPlain(ids.q),
		kind:               int(math.Round(ctx.ParamPlain(ids.kind))),
		designedSampleRate: p.sampleRate,
	}
	if s == p.eqState[i] {
		return
	}
	p.eqState[i] = s
	f := p.eq[i]
	switch s.kind {
	case bandLowShelf:
		f.SetLowShelf(p.sampleRate, s.freq, s.q, s.gain)
	case bandHighShelf:
		f.SetHighShelf(p.sampleRate, s.freq, s.q, s.gain)
	case bandLowPass:
		f.SetLowpass(p.sampleRate, s.freq, s.q)
	case bandHighPass:
		f.SetHighpass(p.sampleRate, s.freq, s.q)
	case bandNotch:
		f.SetNotch(p.sampleRate, s.freq, s.q)
	default:
		f.SetPeakingEQ(p.sampleRate, s.freq, s.q, s.gain)
	}
}

func (p *autoParamsProcessor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(p.bypass) >= 0.5 {
		ctx.PassThrough()
		return
	}
	channels := min(ctx.NumInputChannels(), ctx.NumOutputChannels(), numChannels)
	for ch := channels; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
	if channels == 0 {
		return
	}

	// Keep the dry signal: hosts may process in place.
	dry := [numChannels][]float32{ctx.WorkBuffer(), ctx.TempBuffer()}
	inGain := dbToGain(ctx.ParamPlain(p.inputGain))
	for ch := 0; ch < channels; ch++ {
		copy(dry[ch], ctx.Input[ch])
		for i, x := range dry[ch] {
			ctx.Output[ch][i] = x * inGain
		}
	}

	// Compressor, linked across the channels.
	threshold := ctx.ParamPlain(p.threshold)
	ratio := ctx.ParamPlain(p.ratio)
	p.compressor.SetThreshold(threshold)
	p.compressor.SetRatio(ratio)
	p.compressor.SetAttack(ctx.ParamPlain(p.attack) / 1000)
	p.compressor.SetRelease(ctx.ParamPlain(p.release) / 1000)
	p.compressor.SetKnee(dynamics.KneeSoft, ctx.ParamPlain(p.knee))
	makeup := 0.0
	if ctx.ParamPlain(p.autoGain) >= 0.5 {
		// Give back half the reduction a 0 dBFS signal gets.
		makeup = -threshold * (1 - 1/ratio) / 2
	}
	p.compressor.SetMakeupGain(makeup)
	if channels == 2 {
		p.compressor.ProcessStereo(ctx.Output[0], ctx.Output[1], ctx.Output[0], ctx.Output[1])
	} else {
		p.compressor.ProcessBuffer(ctx.Output[0], ctx.Output[0])
	}

	// EQ bands.
	for i := range p.eq {
		p.updateBand(ctx, i)
		if !p.eqState[i].enabled {
			continue
		}
		for ch := 0; ch < channels; ch++ {
			p.eq[i].Process(ctx.Output[ch], ch)
		}
	}

	// Warmth: blend in tanh saturation, then the dry/wet mix and the
	// output gain.
	warmth := float32(ctx.ParamPlain(p.warmth) / 100)
	wet := float32(ctx.ParamPlain(p.mix) / 100)
	outGain := dbToGain(ctx.ParamPlain(p.outputGain))
	for ch := 0; ch < channels; ch++ {
		out := ctx.Output[ch]
		for i, x := range out {
			x = (1-warmth)*x + warmth*float32(math.Tanh(float64(x)))
			out[i] = ((1-wet)*dry[ch][i] + wet*x) * outGain
		}
	}
}

func init() {
	plugin.Register(autoParamsPlugin{})
}

func main() { standalone.Main() }
