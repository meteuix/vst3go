// Command saturator is a tanh saturator run at up to 16 times the host
// sample rate, so the harmonics it adds above the host Nyquist frequency
// are filtered out instead of folding back as inharmonic aliases. The
// Oversampling factor sets the latency, so it takes effect when processing
// (re)starts and the new latency is announced then. Mix blends the dry
// signal delayed by the same latency.
//
// Build: scripts/build_vst3.sh examples/saturator Saturator
package main

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/dsp/filter"
	"github.com/th-release/vst3go/pkg/dsp/resample"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramDrive uint32 = iota
	paramTone
	paramOversampling
	paramMix
	paramOutput
	paramOutputPeak
)

var factors = [...]int{1, 2, 4, 8, 16}

type saturatorPlugin struct{}

func (saturatorPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.saturator",
		Name:     "Saturator",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDistortion,
	}
}

func (saturatorPlugin) CreateProcessor() plugin.Processor {
	p := &saturatorProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	options := make([]param.ChoiceOption, len(factors))
	for i, f := range factors {
		options[i] = param.ChoiceOption{Value: float64(i), Name: fmt.Sprintf("%dx", f)}
	}
	options[0].Name = "Off"
	p.Parameters().Add(
		param.New(paramDrive, "Drive").Range(0, 36).Default(12).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.FrequencyParameter(paramTone, "Tone", 1000, 20000, 20000).Build(),
		param.Choice(paramOversampling, "Oversampling", options).Default(2).Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
		param.New(paramOutput, "Output").Range(-24, 12).Default(-6).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.OutputLevelMeter(paramOutputPeak, "Output Peak").Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

type saturatorProcessor struct {
	*frameworkplugin.BaseProcessor
	maxBlock int
	over     [2]*resample.Oversampler
	tone     *filter.Biquad
	toneFreq float64

	// Dry signal delayed by the oversampling latency.
	dry    [2][]float32
	dryPos int
	// Per-sample gains for the block, shared by both channels.
	drive, mix, output []float64
	reported           int32
	host               atomic.Pointer[hostRef]
}

type hostRef struct{ plugin.Host }

func (p *saturatorProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

func (p *saturatorProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.maxBlock = int(maxBlockSize)
	p.tone = filter.NewBiquad(2)
	p.toneFreq = 0
	p.drive = make([]float64, p.maxBlock)
	p.mix = make([]float64, p.maxBlock)
	p.output = make([]float64, p.maxBlock)
	return p.configure()
}

// configure builds the oversamplers for the Oversampling parameter.
func (p *saturatorProcessor) configure() error {
	choice := int(p.Parameters().Get(paramOversampling).GetPlainValue())
	factor := factors[max(0, min(len(factors)-1, choice))]
	if p.over[0] != nil && p.over[0].Factor() == factor {
		return nil
	}
	for ch := range p.over {
		o, err := resample.NewOversampler(factor, p.maxBlock)
		if err != nil {
			return err
		}
		p.over[ch] = o
		p.dry[ch] = make([]float32, o.Latency())
	}
	p.dryPos = 0
	return nil
}

func (p *saturatorProcessor) reset() {
	for ch := range p.over {
		p.over[ch].Reset()
		clear(p.dry[ch])
	}
	p.tone.Reset()
	p.dryPos = 0
}

// SetActive applies Oversampling when processing starts and announces the
// latency if it changed.
func (p *saturatorProcessor) SetActive(active bool) error {
	if active {
		if err := p.configure(); err != nil {
			return err
		}
		if latency := p.GetLatencySamples(); latency != p.reported {
			p.reported = latency
			if host := p.host.Load(); host != nil {
				_ = host.RestartComponent(plugin.RestartLatencyChanged)
			}
		}
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *saturatorProcessor) GetLatencySamples() int32 { return int32(p.over[0].Latency()) }

func (p *saturatorProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	n := ctx.NumSamples()
	if f := ctx.ParamPlain(paramTone); f != p.toneFreq {
		p.toneFreq = f
		p.tone.SetLowpass(p.SampleRate(), f, math.Sqrt2/2)
	}
	drive, mix, output := ctx.Smoothed(paramDrive), ctx.Smoothed(paramMix), ctx.Smoothed(paramOutput)
	for i := range n {
		p.drive[i] = math.Pow(10, drive.Next()/20)
		p.mix[i] = mix.Next() / 100
		p.output[i] = math.Pow(10, output.Next()/20)
	}
	factor := p.over[0].Factor()
	wet := ctx.WorkBuffer()
	for ch := range 2 {
		in, out := ctx.Input[ch][:n], ctx.Output[ch][:n]
		high := p.over[ch].Up(in)
		for i := range high {
			high[i] = float32(math.Tanh(p.drive[i/factor] * float64(high[i])))
		}
		p.over[ch].Down(high, wet)
		p.tone.Process(wet, ch)
		// Delay the dry input by the latency; read each input sample
		// before writing its output, as the host may process in place.
		dry := p.dry[ch]
		pos := p.dryPos
		for i := range n {
			d := in[i]
			if len(dry) > 0 {
				d, dry[pos] = dry[pos], in[i]
				pos = (pos + 1) % len(dry)
			}
			m := p.mix[i]
			out[i] = float32((float64(d)*(1-m) + float64(wet[i])*m) * p.output[i])
		}
		if ch == 1 {
			p.dryPos = pos
		}
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}

	peak := 0.0
	for _, out := range ctx.Output[:2] {
		for _, x := range out[:n] {
			peak = math.Max(peak, math.Abs(float64(x)))
		}
	}
	meter := p.Parameters().Get(paramOutputPeak)
	ctx.SetOutputParameter(paramOutputPeak, meter.Normalize(20*math.Log10(math.Max(peak, 1e-3))))
}

func init() {
	plugin.Register(saturatorPlugin{})
}

func main() { standalone.Main() }
