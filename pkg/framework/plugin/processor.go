// Package plugin provides framework-side helper types that sit below the
// runtime-facing pkg/plugin interfaces.
//
// In practice:
//   - pkg/plugin defines the runtime contract consumed by the VST3 wrapper
//   - pkg/framework/plugin provides reusable building blocks that help satisfy
//     that contract without owning the wrapper itself
package plugin

import (
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/framework/process"
)

// BaseProcessor is a convenience helper for implementations of pkg/plugin.Processor.
//
// It supplies default storage for parameters, bus configuration, sample rate,
// and a few lifecycle hooks. It is optional: callers can implement the runtime
// Processor interface directly when they need full control.
type BaseProcessor struct {
	params     *param.Registry
	buses      *bus.Configuration
	sampleRate float64

	// Optional callbacks for customization
	onInitialize func(sampleRate float64, maxBlockSize int32) error
	onSetActive  func(active bool) error
	onReset      func()

	deadline float64
}

// NewBaseProcessor creates a BaseProcessor with the given bus configuration.
func NewBaseProcessor(buses *bus.Configuration) *BaseProcessor {
	if buses == nil {
		buses = bus.Stereo() // Default to stereo
	}

	return &BaseProcessor{
		params:   param.NewRegistry(),
		buses:    buses,
		deadline: DefaultProcessDeadline,
	}
}

// Initialize implements the Processor interface
func (b *BaseProcessor) Initialize(sampleRate float64, maxBlockSize int32) error {
	b.sampleRate = sampleRate

	if b.onInitialize != nil {
		return b.onInitialize(sampleRate, maxBlockSize)
	}

	return nil
}

// GetParameters implements the Processor interface
func (b *BaseProcessor) GetParameters() *param.Registry {
	return b.params
}

// GetBuses implements the Processor interface
func (b *BaseProcessor) GetBuses() *bus.Configuration {
	return b.buses
}

// SetActive implements the Processor interface
func (b *BaseProcessor) SetActive(active bool) error {
	if !active && b.onReset != nil {
		b.onReset()
	}

	if b.onSetActive != nil {
		return b.onSetActive(active)
	}

	return nil
}

// GetLatencySamples implements the Processor interface - default no latency
func (b *BaseProcessor) GetLatencySamples() int32 {
	return 0
}

// GetTailSamples implements the Processor interface - default no tail
func (b *BaseProcessor) GetTailSamples() int32 {
	return 0
}

// SampleRate returns the current sample rate
func (b *BaseProcessor) SampleRate() float64 {
	return b.sampleRate
}

// Parameters returns the parameter registry for adding parameters
func (b *BaseProcessor) Parameters() *param.Registry {
	return b.params
}

// DefaultProcessDeadline is the share of each block's duration the host's
// audio thread waits for a block (see BaseProcessor.SetProcessDeadline).
const DefaultProcessDeadline = 0.5

// SetProcessDeadline lets the host's audio thread wait at most share (0 to
// 1) of each block's duration for the block, which runs on a dedicated
// real-time thread; a later block outputs silence instead of stalling the
// host. It is DefaultProcessDeadline unless set; 0 processes on the host's
// audio thread. It takes effect when the plugin is activated (see
// plugin.DeadlineProcessor).
func (b *BaseProcessor) SetProcessDeadline(share float64) {
	b.deadline = share
}

// ProcessDeadline implements plugin.DeadlineProcessor.
func (b *BaseProcessor) ProcessDeadline() float64 {
	return b.deadline
}

// OnInitialize sets a callback for initialization
func (b *BaseProcessor) OnInitialize(fn func(sampleRate float64, maxBlockSize int32) error) {
	b.onInitialize = fn
}

// OnSetActive sets a callback for activation/deactivation
func (b *BaseProcessor) OnSetActive(fn func(active bool) error) {
	b.onSetActive = fn
}

// OnReset sets a callback for when the processor should reset its state
func (b *BaseProcessor) OnReset(fn func()) {
	b.onReset = fn
}

// ProcessorWithBase marks types that use BaseProcessor and provide ProcessAudio.
//
// It is a documentation-oriented helper rather than a required runtime layer.
type ProcessorWithBase interface {
	ProcessAudio(ctx *process.Context)
}

// SimpleProcessor is a thin convenience wrapper for very small processors.
//
// It still satisfies the runtime Processor contract through BaseProcessor, but
// delegates audio handling to a single function.
type SimpleProcessor struct {
	*BaseProcessor
	processFunc func(ctx *process.Context)
}

// NewSimpleProcessor creates a processor with just a process function
func NewSimpleProcessor(buses *bus.Configuration, processFunc func(ctx *process.Context)) *SimpleProcessor {
	return &SimpleProcessor{
		BaseProcessor: NewBaseProcessor(buses),
		processFunc:   processFunc,
	}
}

// ProcessAudio implements the audio processing
func (s *SimpleProcessor) ProcessAudio(ctx *process.Context) {
	if s.processFunc != nil {
		s.processFunc(ctx)
	}
}
