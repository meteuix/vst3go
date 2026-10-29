// Command convreverb is a stereo convolution reverb with a generated
// impulse response: decorrelated noise for each channel with a
// two-band exponential decay (Decay is the low-band RT60, Damping shortens
// the high band), after a pre-delay. A background goroutine rebuilds the
// response when the parameters change; the audio thread picks the new
// convolvers up through an atomic pointer and crossfades to them, so
// neither the build nor the swap blocks or allocates on the audio thread.
// The convolution itself has no latency.
//
// Build: scripts/build_vst3.sh examples/convreverb ConvReverb
package main

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/th-release/vst3go/pkg/dsp/convolution"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramDecay uint32 = iota
	paramDamping
	paramPreDelay
	paramWidth
	paramMix
)

const (
	blockSize   = 128
	fadeSamples = 4096
	maxSeconds  = 12.0
)

type convReverbPlugin struct{}

func (convReverbPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.convreverb",
		Name:     "Conv Reverb",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxReverb,
	}
}

func (convReverbPlugin) CreateProcessor() plugin.Processor {
	p := &convReverbProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramDecay, "Decay").Range(0.2, 10).Default(2).Unit("s").Build(),
		param.New(paramDamping, "Damping").Range(0, 100).Default(50).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.TimeParameter(paramPreDelay, "Pre-Delay", 0, 200, 10).Build(),
		param.New(paramWidth, "Width").Range(0, 100).Default(100).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.MixParameter(paramMix, "Mix").Default(30).Smooth(0.02).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(p.reset)
	return p
}

// settings are the parameters an impulse response was built from.
type settings struct {
	decay, damping, preDelay, width float64
}

// reverb is one built response: immutable settings and its convolvers.
type reverb struct {
	settings settings
	ir       [2][]float32
	conv     [2]*convolution.Convolver
}

type convReverbProcessor struct {
	*frameworkplugin.BaseProcessor

	built atomic.Pointer[reverb] // latest response from the builder

	// Audio thread state.
	current, previous *reverb
	fade              int // samples into the crossfade from previous
	wet, old, dry     [2][]float32

	// Builder goroutine, running while active.
	mu       sync.Mutex
	stop     chan struct{}
	finished chan struct{}
}

func (p *convReverbProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	for ch := range 2 {
		p.wet[ch] = make([]float32, maxBlockSize)
		p.old[ch] = make([]float32, maxBlockSize)
		p.dry[ch] = make([]float32, maxBlockSize)
	}
	r, err := build(p.requested(), sampleRate)
	if err != nil {
		return err
	}
	p.built.Store(r)
	p.current, p.previous = r, nil
	return nil
}

func (p *convReverbProcessor) reset() {
	if r := p.built.Load(); r != nil {
		for _, c := range r.conv {
			c.Reset()
		}
		p.current, p.previous = r, nil
	}
}

func (p *convReverbProcessor) requested() settings {
	params := p.Parameters()
	return settings{
		decay:    params.Get(paramDecay).GetPlainValue(),
		damping:  params.Get(paramDamping).GetPlainValue() / 100,
		preDelay: params.Get(paramPreDelay).GetPlainValue() / 1000,
		width:    params.Get(paramWidth).GetPlainValue() / 100,
	}
}

// SetActive runs the builder while processing.
func (p *convReverbProcessor) SetActive(active bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if active && p.stop == nil {
		p.stop = make(chan struct{})
		p.finished = make(chan struct{})
		go p.rebuild(p.stop, p.finished, p.SampleRate())
	} else if !active && p.stop != nil {
		close(p.stop)
		<-p.finished
		p.stop, p.finished = nil, nil
	}
	return p.BaseProcessor.SetActive(active)
}

// rebuild polls the parameters and builds a new response when they
// change.
func (p *convReverbProcessor) rebuild(stop <-chan struct{}, finished chan<- struct{}, sampleRate float64) {
	defer close(finished)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			want := p.requested()
			if p.built.Load().settings == want {
				continue
			}
			if r, err := build(want, sampleRate); err == nil {
				p.built.Store(r)
			}
		}
	}
}

// GetTailSamples covers the longest response.
func (p *convReverbProcessor) GetTailSamples() int32 {
	return int32((maxSeconds + 0.2) * p.SampleRate())
}

func (p *convReverbProcessor) ProcessAudio(ctx *process.Context) {
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	n := ctx.NumSamples()
	if r := p.built.Load(); r != p.current && p.previous == nil {
		// The new convolvers start from silence; fade the old ones out.
		p.previous, p.current, p.fade = p.current, r, 0
	}
	for ch := range 2 {
		copy(p.dry[ch][:n], ctx.Input[ch][:n])
		p.current.conv[ch].Process(p.dry[ch][:n], p.wet[ch][:n])
		if p.previous != nil {
			p.previous.conv[ch].Process(p.dry[ch][:n], p.old[ch][:n])
		}
	}
	mix := ctx.Smoothed(paramMix)
	for i := range n {
		m := float32(mix.Next() / 100)
		for ch := range 2 {
			w := p.wet[ch][i]
			if p.previous != nil {
				g := float32(min(1, float64(p.fade+i)/fadeSamples))
				w = g*w + (1-g)*p.old[ch][i]
			}
			ctx.Output[ch][i] = p.dry[ch][i]*(1-m) + w*m
		}
	}
	if p.previous != nil {
		p.fade += n
		if p.fade >= fadeSamples {
			p.previous = nil
		}
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

// build generates a stereo impulse response and its convolvers.
func build(s settings, sampleRate float64) (*reverb, error) {
	length := int(math.Min(maxSeconds, 1.5*s.decay) * sampleRate)
	pre := int(s.preDelay * sampleRate)
	highDecay := s.decay * (1 - 0.85*s.damping)
	// One-pole crossover at 2 kHz splitting the noise into two bands.
	a := math.Exp(-2 * math.Pi * 2000 / sampleRate)
	rng := uint64(0x2545F4914F6CDD1D)
	noise := func() float64 {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return float64(rng>>11)/(1<<52) - 1
	}
	var raw [2][]float64
	for ch := range raw {
		raw[ch] = make([]float64, length)
		low := 0.0
		for i := range raw[ch] {
			t := float64(i) / sampleRate
			x := noise()
			low = (1-a)*x + a*low
			high := x - low
			// RT60: 60 dB down (factor 1000) after the decay time.
			raw[ch][i] = low*math.Pow(1000, -t/s.decay) + high*math.Pow(1000, -t/highDecay)
		}
	}
	// Width blends the two decorrelated channels; normalize each channel
	// to unit energy so the wet level does not depend on Decay.
	r := &reverb{settings: s}
	mid := (1 - s.width) / 2
	for ch := range 2 {
		ir := make([]float32, pre+length)
		energy := 0.0
		for i := range length {
			v := (1-mid)*raw[ch][i] + mid*raw[1-ch][i]
			ir[pre+i] = float32(v)
			energy += v * v
		}
		scale := float32(1 / math.Sqrt(energy))
		for i := range ir {
			ir[i] *= scale
		}
		c, err := convolution.New(ir, blockSize)
		if err != nil {
			return nil, err
		}
		r.ir[ch], r.conv[ch] = ir, c
	}
	return r, nil
}

func init() {
	plugin.Register(convReverbPlugin{})
}

func main() { standalone.Main() }
