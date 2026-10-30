// Command surround pans a mono or stereo input onto a 5.1 speaker layout
// (L, R, C, LFE, Ls, Rs at the ITU angles 330, 30, 0, -, 250 and 110
// degrees). Each input channel is panned between the two speakers around
// its angle with a constant-power law; Width spreads the two inputs apart.
// Distance below 100% and Divergence blend the gains toward all five main
// speakers, Center Level moves the center speaker's share to a phantom
// center in L/R, and LFE Send feeds a 120 Hz low-passed mono sum to the
// LFE channel. Gains ramp across each block, so automation is smooth.
//
// Build: scripts/build_vst3.sh examples/surround Surround
package main

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/filter"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

const (
	paramAngle uint32 = iota
	paramDistance
	paramWidth
	paramDivergence
	paramCenterLevel
	paramLFE
)

// 5.1 channel order (VST3 speaker bit order).
const (
	chL = iota
	chR
	chC
	chLFE
	chLs
	chRs
	numChannels
)

// speakers are the main speakers clockwise from the front, with their
// angles in degrees.
var speakers = [...]struct {
	ch    int
	angle float64
}{{chC, 0}, {chR, 30}, {chRs, 110}, {chLs, 250}, {chL, 330}}

const lfeOff = -60.0 // LFE Send at its minimum is off

type surroundPlugin struct{}

func (surroundPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.surround",
		Name:     "Surround Panner 5.1",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxSpatial,
	}
}

func (surroundPlugin) CreateProcessor() plugin.Processor {
	p := &surroundProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewSurroundPanner())}
	p.Parameters().Add(
		param.New(paramAngle, "Angle").Range(0, 360).Default(0).Unit("°").Build(),
		param.New(paramDistance, "Distance").Range(0, 100).Default(100).Unit("%").Build(),
		param.New(paramWidth, "Width").Range(0, 200).Default(100).Unit("%").Build(),
		param.New(paramDivergence, "Divergence").Range(0, 100).Default(0).Unit("%").Build(),
		param.New(paramCenterLevel, "Center Level").Range(0, 100).Default(100).Unit("%").Build(),
		param.New(paramLFE, "LFE Send").Range(lfeOff, 0).Default(lfeOff).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Build(),
	)
	p.OnInitialize(func(sampleRate float64, _ int32) error {
		p.lfeFilter = filter.NewBiquad(1)
		p.lfeFilter.SetLowpass(sampleRate, 120, math.Sqrt2/2)
		p.started = false
		return nil
	})
	p.OnReset(func() {
		p.lfeFilter.Reset()
		p.started = false
	})
	return p
}

type surroundProcessor struct {
	*frameworkplugin.BaseProcessor
	lfeFilter *filter.Biquad
	gains     [2][numChannels]float64 // per input channel, at the end of the last block
	lfeGain   float64
	started   bool
}

// panGains returns the speaker gains for one source.
func panGains(angle, spread, centerLevel float64) [numChannels]float64 {
	var g [numChannels]float64
	angle = math.Mod(math.Mod(angle, 360)+360, 360)
	for i, a := range speakers {
		b := speakers[(i+1)%len(speakers)]
		end := b.angle
		if end <= a.angle {
			end += 360
		}
		pos := angle
		if pos < a.angle {
			pos += 360
		}
		if pos >= a.angle && pos < end {
			x := (pos - a.angle) / (end - a.angle) * math.Pi / 2
			g[a.ch], g[b.ch] = math.Cos(x), math.Sin(x)
			break
		}
	}
	// Spread blends the power toward all five main speakers.
	for _, s := range speakers {
		g[s.ch] = math.Sqrt((1-spread)*g[s.ch]*g[s.ch] + spread/float64(len(speakers)))
	}
	// The center's share beyond Center Level goes to a phantom center.
	phantom := g[chC] * math.Sqrt((1-centerLevel)/2)
	g[chC] *= math.Sqrt(centerLevel)
	g[chL] = math.Hypot(g[chL], phantom)
	g[chR] = math.Hypot(g[chR], phantom)
	return g
}

func (p *surroundProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) < numChannels || len(ctx.Input) == 0 {
		ctx.Clear()
		return
	}
	angle := ctx.ParamPlain(paramAngle)
	halfWidth := ctx.ParamPlain(paramWidth) / 100 * 30
	spread := 1 - ctx.ParamPlain(paramDistance)/100*(1-ctx.ParamPlain(paramDivergence)/100)
	center := ctx.ParamPlain(paramCenterLevel) / 100

	inputs := min(len(ctx.Input), 2)
	var target [2][numChannels]float64
	if inputs == 1 {
		target[0] = panGains(angle, spread, center)
	} else {
		target[0] = panGains(angle-halfWidth, spread, center)
		target[1] = panGains(angle+halfWidth, spread, center)
	}
	lfeTarget := 0.0
	if db := ctx.ParamPlain(paramLFE); db > lfeOff {
		lfeTarget = math.Pow(10, db/20)
	}
	if !p.started {
		p.gains, p.lfeGain, p.started = target, lfeTarget, true
	}

	// The LFE feed: the low-passed mono sum, built in the work buffer
	// before the outputs (which may alias the inputs) are written.
	lfe := ctx.WorkBuffer()[:n]
	for i := range lfe {
		sum := float32(0)
		for ch := range inputs {
			sum += ctx.Input[ch][i]
		}
		lfe[i] = sum / float32(inputs)
	}
	p.lfeFilter.Process(lfe, 0)

	// Inputs into registers first: hosts may process in place.
	for i := range n {
		t := float64(i+1) / float64(n)
		var in [2]float64
		for ch := range inputs {
			in[ch] = float64(ctx.Input[ch][i])
		}
		for out := range numChannels {
			if out == chLFE {
				g := p.lfeGain + (lfeTarget-p.lfeGain)*t
				ctx.Output[chLFE][i] = float32(float64(lfe[i]) * g)
				continue
			}
			y := 0.0
			for ch := range inputs {
				y += in[ch] * (p.gains[ch][out] + (target[ch][out]-p.gains[ch][out])*t)
			}
			ctx.Output[out][i] = float32(y)
		}
	}
	for ch := numChannels; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
	p.gains, p.lfeGain = target, lfeTarget
}

func init() {
	plugin.Register(surroundPlugin{})
}

func main() { standalone.Main() }
