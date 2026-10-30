package dsp

import (
	"github.com/th-release/vst3go/v2/pkg/dsp/dynamics"
	"github.com/th-release/vst3go/v2/pkg/dsp/utility"
)

// The adapters wrap pkg/dsp processors as chain stages. Each implements
// Processor (one channel) and StereoProcessor (two channels); use a stage
// in one kind of chain only, because it keeps one set of state.

// CompressorAdapter adapts a compressor. In a stereo chain the channels
// are linked: both get the gain the louder one needs.
type CompressorAdapter struct {
	comp *dynamics.Compressor
}

// NewCompressorAdapter wraps c.
func NewCompressorAdapter(c *dynamics.Compressor) *CompressorAdapter {
	return &CompressorAdapter{comp: c}
}

func (a *CompressorAdapter) Process(buffer []float32) {
	a.comp.ProcessBuffer(buffer, buffer)
}

func (a *CompressorAdapter) ProcessStereo(left, right []float32) {
	a.comp.ProcessStereo(left, right, left, right)
}

func (a *CompressorAdapter) Reset() {
	a.comp.Reset()
}

// GateAdapter adapts a gate. In a stereo chain the channels are linked.
type GateAdapter struct {
	gate *dynamics.Gate
}

// NewGateAdapter wraps g.
func NewGateAdapter(g *dynamics.Gate) *GateAdapter {
	return &GateAdapter{gate: g}
}

func (a *GateAdapter) Process(buffer []float32) {
	a.gate.ProcessBuffer(buffer, buffer)
}

func (a *GateAdapter) ProcessStereo(left, right []float32) {
	a.gate.ProcessStereo(left, right, left, right)
}

func (a *GateAdapter) Reset() {
	a.gate.Reset()
}

// DCBlockerAdapter removes DC offset, with one filter per channel.
type DCBlockerAdapter struct {
	left, right *utility.SimpleDCBlocker
}

// NewDCBlockerAdapter creates a DC blocker for up to two channels.
func NewDCBlockerAdapter(sampleRate float64) *DCBlockerAdapter {
	return &DCBlockerAdapter{
		left:  utility.NewSimpleDCBlocker(sampleRate),
		right: utility.NewSimpleDCBlocker(sampleRate),
	}
}

func (a *DCBlockerAdapter) Process(buffer []float32) {
	a.left.ProcessBuffer(buffer)
}

func (a *DCBlockerAdapter) ProcessStereo(left, right []float32) {
	a.left.ProcessBuffer(left)
	a.right.ProcessBuffer(right)
}

func (a *DCBlockerAdapter) Reset() {
	a.left.Reset()
	a.right.Reset()
}

// NoiseAdapter adds noise at a level set by SetMix (linear amplitude). In a
// stereo chain each channel gets its own noise.
type NoiseAdapter struct {
	noise *utility.NoiseGenerator
	mix   float32
}

// NewNoiseAdapter creates a noise stage of the given type and level.
func NewNoiseAdapter(noiseType utility.NoiseType, mix float32) *NoiseAdapter {
	return &NoiseAdapter{
		noise: utility.NewNoiseGenerator(noiseType),
		mix:   mix,
	}
}

func (a *NoiseAdapter) Process(buffer []float32) {
	a.noise.GenerateAdd(buffer, a.mix)
}

func (a *NoiseAdapter) ProcessStereo(left, right []float32) {
	a.noise.GenerateAdd(left, a.mix)
	a.noise.GenerateAdd(right, a.mix)
}

func (a *NoiseAdapter) Reset() {
	a.noise.Reset()
}

// SetMix sets the noise level.
func (a *NoiseAdapter) SetMix(mix float32) {
	a.mix = mix
}

// CreateSimpleChain creates a chain that removes DC offset.
func CreateSimpleChain(sampleRate float64) (*Chain, error) {
	return NewBuilder("Simple Chain").
		WithProcessor(NewDCBlockerAdapter(sampleRate)).
		Build()
}

// CreateDynamicsChain creates a gate (-40 dB, 60 dB range) followed by a
// 4:1 compressor at -20 dB.
func CreateDynamicsChain(sampleRate float64) (*Chain, error) {
	gate := dynamics.NewGate(sampleRate)
	gate.SetThreshold(-40)
	gate.SetRange(-60)

	comp := dynamics.NewCompressor(sampleRate)
	comp.SetThreshold(-20)
	comp.SetRatio(4)
	comp.SetAttack(0.010)
	comp.SetRelease(0.100)

	return NewBuilder("Dynamics Chain").
		WithProcessor(NewGateAdapter(gate)).
		WithProcessor(NewCompressorAdapter(comp)).
		Build()
}
