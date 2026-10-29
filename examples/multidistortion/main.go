// Command multidistortion switches between four distortion engines from
// pkg/dsp/distortion: a waveshaper with seven curves, a tube stage, tape
// saturation and a bitcrusher. Drive pushes whichever engine is selected;
// each engine has its own page of parameters, and Mix and Output apply to
// all of them.
//
// Build: scripts/build_vst3.sh examples/multidistortion MultiDistortion
package main

import (
	"fmt"
	"math"

	"github.com/th-release/vst3go/pkg/dsp/distortion"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

const (
	paramType uint32 = iota
	paramDrive
	paramMix
	paramOutput

	paramWaveCurve
	paramWaveAsymmetry

	paramTubeWarmth
	paramTubeHarmonics
	paramTubeBias
	paramTubeHysteresis

	paramTapeSaturation
	paramTapeCompression
	paramTapeFlutter
	paramTapeWarmth

	paramBitDepth
	paramSampleReduction
	paramDither
	paramAntiAlias
)

// Engines selected by Type.
const (
	engineWaveshaper = iota
	engineTube
	engineTape
	engineBitcrusher
)

type multiDistortionPlugin struct{}

func (multiDistortionPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.multidistortion",
		Name:     "Multi-Distortion",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDistortion,
	}
}

func (multiDistortionPlugin) CreateProcessor() plugin.Processor {
	p := &multiDistortionProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	params := p.Parameters()
	params.Add(
		param.Choice(paramType, "Type", []param.ChoiceOption{
			{Value: engineWaveshaper, Name: "Waveshaper", Aliases: []string{"wave", "shaper"}},
			{Value: engineTube, Name: "Tube", Aliases: []string{"valve", "triode"}},
			{Value: engineTape, Name: "Tape", Aliases: []string{"analog", "vintage"}},
			{Value: engineBitcrusher, Name: "Bitcrusher", Aliases: []string{"digital", "lofi", "crush"}},
		}).Build(),
		param.New(paramDrive, "Drive").Range(0, 100).Default(50).Unit("%").Build(),
		param.MixParameter(paramMix, "Mix").Smooth(0.02).Build(),
		param.New(paramOutput, "Output").Range(-24, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),

		param.Choice(paramWaveCurve, "Curve", []param.ChoiceOption{
			{Value: float64(distortion.CurveHardClip), Name: "Hard Clip", Aliases: []string{"hard", "clip"}},
			{Value: float64(distortion.CurveSoftClip), Name: "Soft Clip", Aliases: []string{"soft", "tanh"}},
			{Value: float64(distortion.CurveSaturate), Name: "Saturate", Aliases: []string{"sat"}},
			{Value: float64(distortion.CurveFoldback), Name: "Foldback", Aliases: []string{"fold", "wrap"}},
			{Value: float64(distortion.CurveAsymmetric), Name: "Asymmetric", Aliases: []string{"asym"}},
			{Value: float64(distortion.CurveSine), Name: "Sine", Aliases: []string{"sin"}},
			{Value: float64(distortion.CurveExponential), Name: "Exponential", Aliases: []string{"exp"}},
		}).Default(float64(distortion.CurveSoftClip)).Build(),
		param.New(paramWaveAsymmetry, "Asymmetry").Range(-100, 100).Default(0).Unit("%").Build(),

		param.New(paramTubeWarmth, "Tube Warmth").Range(0, 100).Default(50).Unit("%").Build(),
		param.New(paramTubeHarmonics, "Harmonics").Range(0, 100).Default(50).Unit("%").Build(),
		param.New(paramTubeBias, "Bias").Range(-100, 100).Default(0).Unit("%").Build(),
		param.New(paramTubeHysteresis, "Hysteresis").Range(0, 100).Default(10).Unit("%").Build(),

		param.New(paramTapeSaturation, "Saturation").Range(0, 100).Default(50).Unit("%").Build(),
		param.New(paramTapeCompression, "Compression").Range(0, 100).Default(30).Unit("%").Build(),
		param.New(paramTapeFlutter, "Flutter").Range(0, 100).Default(0).Unit("%").Build(),
		param.New(paramTapeWarmth, "Tape Warmth").Range(0, 100).Default(50).Unit("%").Build(),

		param.New(paramBitDepth, "Bit Depth").Range(1, 24).Default(8).Steps(23).Unit("bits").Build(),
		param.New(paramSampleReduction, "Downsample").Range(1, 64).Default(1).
			Formatter(func(v float64) string {
				if v <= 1 {
					return "Off"
				}
				return fmt.Sprintf("1/%.1f", v)
			}, nil).Build(),
		param.Choice(paramDither, "Dither", []param.ChoiceOption{
			{Value: float64(distortion.DitherNone), Name: "None", Aliases: []string{"off"}},
			{Value: float64(distortion.DitherWhite), Name: "White", Aliases: []string{"white noise"}},
			{Value: float64(distortion.DitherTriangular), Name: "Triangular", Aliases: []string{"tri", "tpdf"}},
		}).Build(),
		param.Choice(paramAntiAlias, "Anti-Alias", []param.ChoiceOption{
			{Value: 0, Name: "Off"},
			{Value: 1, Name: "On"},
		}).Default(1).Build(),
	)
	p.OnInitialize(p.prepare)
	p.OnReset(func() {
		p.tube.Reset()
		p.tape.Reset()
		p.crusher.Reset()
	})
	return p
}

type multiDistortionProcessor struct {
	*frameworkplugin.BaseProcessor
	shaper  *distortion.Waveshaper
	tube    *distortion.TubeSaturation
	tape    *distortion.TapeSaturation
	crusher *distortion.Bitcrusher

	// The engines work in float64.
	in, out [2][]float64
}

func (p *multiDistortionProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	p.shaper = distortion.NewWaveshaper()
	p.tube = distortion.NewTubeSaturation(sampleRate)
	p.tape = distortion.NewTapeSaturation(sampleRate)
	p.crusher = distortion.NewBitcrusher(sampleRate)
	for ch := range p.in {
		p.in[ch] = make([]float64, maxBlockSize)
		p.out[ch] = make([]float64, maxBlockSize)
	}
	return nil
}

func (p *multiDistortionProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) < 2 || len(ctx.Output) < 2 {
		ctx.PassThrough()
		return
	}
	drive := ctx.ParamPlain(paramDrive) / 100
	inL, inR := p.in[0][:n], p.in[1][:n]
	outL, outR := p.out[0][:n], p.out[1][:n]
	for i := range n {
		inL[i], inR[i] = float64(ctx.Input[0][i]), float64(ctx.Input[1][i])
	}

	// Each engine runs fully wet at unity output; Mix and Output follow.
	switch int(ctx.ParamPlain(paramType)) {
	case engineWaveshaper:
		s := p.shaper
		s.SetCurveType(distortion.CurveType(ctx.ParamPlain(paramWaveCurve)))
		s.SetDrive(1 + drive*19) // 1x to 20x (+26 dB)
		s.SetAsymmetry(ctx.ParamPlain(paramWaveAsymmetry) / 100)
		s.ProcessStereo(inL, inR, outL, outR)
	case engineTube:
		t := p.tube
		t.SetWarmth(ctx.ParamPlain(paramTubeWarmth) / 100)
		t.SetHarmonics(ctx.ParamPlain(paramTubeHarmonics) / 100)
		t.SetBias(ctx.ParamPlain(paramTubeBias) / 100)
		t.SetHysteresis(ctx.ParamPlain(paramTubeHysteresis) / 100)
		gain := 1 + drive*7 // up to +18 dB into the tube
		for i := range n {
			inL[i] *= gain
			inR[i] *= gain
		}
		t.ProcessStereo(inL, inR, outL, outR)
	case engineTape:
		t := p.tape
		t.SetSaturation(ctx.ParamPlain(paramTapeSaturation) / 100 * (0.5 + drive))
		t.SetCompression(ctx.ParamPlain(paramTapeCompression) / 100)
		t.SetFlutter(ctx.ParamPlain(paramTapeFlutter) / 100)
		t.SetWarmth(ctx.ParamPlain(paramTapeWarmth) / 100)
		t.ProcessStereo(inL, inR, outL, outR)
	case engineBitcrusher:
		c := p.crusher
		bits := ctx.ParamPlain(paramBitDepth)
		c.SetBitDepth(bits - (bits-1)*drive*0.8) // Drive takes bits away
		c.SetSampleRateReduction(ctx.ParamPlain(paramSampleReduction))
		c.SetDither(distortion.DitherType(ctx.ParamPlain(paramDither)))
		c.SetAntiAlias(ctx.ParamPlain(paramAntiAlias) >= 0.5)
		c.ProcessStereo(inL, inR, outL, outR)
	}

	mix, output := ctx.Smoothed(paramMix), ctx.Smoothed(paramOutput)
	for i := range n {
		wet := mix.Next() / 100
		g := math.Pow(10, output.Next()/20)
		dryL, dryR := float64(ctx.Input[0][i]), float64(ctx.Input[1][i])
		ctx.Output[0][i] = float32((dryL + (outL[i]-dryL)*wet) * g)
		ctx.Output[1][i] = float32((dryR + (outR[i]-dryR)*wet) * g)
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

func init() {
	plugin.Register(multiDistortionPlugin{})
}

func main() { standalone.Main() }
