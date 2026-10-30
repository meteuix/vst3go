// Command tuner is a chromatic tuner: a YIN pitch detector on the sum of
// the input channels reports the frequency, the nearest note and the
// deviation in cents (against an adjustable A4 reference) through
// read-only parameters a host or editor can display. Audio passes through
// unchanged.
//
// Build: scripts/build_vst3.sh examples/tuner Tuner
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/pitch"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramReference uint32 = iota
	paramFrequency
	paramNote
	paramCents
	paramClarity
)

type tunerPlugin struct{}

func (tunerPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.tuner",
		Name:     "Tuner",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxAnalyzer,
	}
}

func (tunerPlugin) CreateProcessor() plugin.Processor {
	p := &tunerProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramReference, "A4").Range(415, 466).Default(440).Unit("Hz").Build(),
		param.New(paramFrequency, "Frequency").Range(0, 2000).Default(0).Unit("Hz").ReadOnly().Build(),
		param.New(paramNote, "Note").Range(0, 127).Default(69).Steps(127).ReadOnly().Build(),
		param.New(paramCents, "Cents").Range(-50, 50).Default(0).Unit("ct").ReadOnly().Build(),
		param.New(paramClarity, "Clarity").Range(0, 100).Default(0).Unit("%").ReadOnly().Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.detector.Reset() })
	return p
}

type tunerProcessor struct {
	*frameworkplugin.BaseProcessor
	detector *pitch.Detector
	mono     []float32
}

func (p *tunerProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	d, err := pitch.NewDetector(sampleRate, 30, 2000, 256)
	if err != nil {
		return err
	}
	p.detector = d
	p.mono = make([]float32, maxBlockSize)
	return nil
}

func (p *tunerProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	mono := p.mono[:n]
	clear(mono)
	for _, in := range ctx.Input {
		for i, x := range in[:n] {
			mono[i] += x
		}
	}
	ctx.PassThrough()
	p.detector.Process(mono)

	freq, clarity := p.detector.Pitch()
	params := p.Parameters()
	ctx.SetOutputParameter(paramFrequency, params.Get(paramFrequency).Normalize(freq))
	ctx.SetOutputParameter(paramClarity, params.Get(paramClarity).Normalize(clarity*100))
	if freq <= 0 {
		return
	}
	note, cents := nearestNote(freq, ctx.ParamPlain(paramReference))
	ctx.SetOutputParameter(paramNote, params.Get(paramNote).Normalize(float64(note)))
	ctx.SetOutputParameter(paramCents, params.Get(paramCents).Normalize(cents))
}

// nearestNote returns the MIDI note closest to freq and the deviation from
// it in cents, with A4 (note 69) at reference Hz.
func nearestNote(freq, reference float64) (int, float64) {
	semis := 69 + 12*math.Log2(freq/reference)
	note := int(math.Round(semis))
	return max(0, min(127, note)), (semis - float64(note)) * 100
}

func init() {
	plugin.Register(tunerPlugin{})
}

func main() { standalone.Main() }
