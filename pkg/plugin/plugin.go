// Package plugin provides the runtime-facing VST3 plugin interfaces.
//
// This package is the contract consumed by the VST3 wrapper/runtime layer:
// callers implement Plugin, which produces a Processor. A Processor can opt
// into extra host features by implementing the optional interfaces below
// (StatefulProcessor, BusArrangementProcessor, MIDIControllerProcessor,
// MIDIMappingProcessor, HostAwareProcessor).
package plugin

import (
	"io"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
)

// Plugin is the top-level runtime interface implemented by a plugin entrypoint.
//
// GetInfo returns the metadata shared with the host, while CreateProcessor
// constructs a fresh Processor instance for actual audio/event handling.
type Plugin interface {
	// GetInfo returns plugin metadata
	GetInfo() plugin.Info

	// CreateProcessor creates a new instance of the audio processor
	CreateProcessor() Processor
}

// Processor is the core runtime contract consumed by the VST3 wrapper.
//
// A Processor owns:
//   - initialization and activation lifecycle
//   - audio/event processing via process.Context
//   - parameter and bus configuration
//   - latency/tail reporting
//
// The wrapper/runtime creates a Processor from Plugin.CreateProcessor and
// delegates all host-facing lifecycle and processing calls through it.
type Processor interface {
	// Initialize is called when the plugin is created and again from
	// setupProcessing with the host sample rate and maximum block size.
	Initialize(sampleRate float64, maxBlockSize int32) error

	// ProcessAudio processes audio on the real-time thread. It must not
	// allocate, lock or block.
	ProcessAudio(ctx *process.Context)

	// GetParameters returns the parameter registry
	GetParameters() *param.Registry

	// GetBuses returns the bus configuration
	GetBuses() *bus.Configuration

	// SetActive is called when processing starts/stops
	SetActive(active bool) error

	// GetLatencySamples returns the plugin's latency in samples. Call
	// Host.RestartComponent(RestartLatencyChanged) after it changes.
	GetLatencySamples() int32

	// GetTailSamples returns the tail length in samples. Return TailInfinite
	// for processors that never decay to silence (e.g. oscillators).
	GetTailSamples() int32
}

// TailInfinite is the GetTailSamples value for a never-ending tail.
const TailInfinite int32 = -1

// StatefulProcessor extends Processor with custom state save/load hooks.
//
// Implement this only when parameter state is insufficient to restore runtime
// behavior, for example when additional processor-owned state must be persisted.
type StatefulProcessor interface {
	Processor

	// SaveCustomState saves additional state beyond parameters
	// This is called after all parameters have been saved
	SaveCustomState(w io.Writer) error

	// LoadCustomState loads additional state beyond parameters
	// This is called after all parameters have been loaded
	LoadCustomState(r io.Reader) error
}

// BusArrangementProcessor lets a processor accept speaker arrangements
// proposed by the host, for example an effect that runs in mono or stereo.
//
// Without it the runtime only accepts the arrangements of the processor's
// bus configuration. When AcceptBusArrangements returns true the runtime
// updates the bus configuration (and channel counts) before setupProcessing.
type BusArrangementProcessor interface {
	AcceptBusArrangements(inputs, outputs []bus.SpeakerArrangement) bool
}

// MIDIControllerProcessor receives MIDI controllers (CC 0-127), channel
// pressure and pitch bend as midi.ControlChangeEvent, midi.ChannelPressureEvent
// and midi.PitchBendEvent input events.
//
// VST3 hosts do not send these as events; they are mapped to hidden
// parameters through IMidiMapping. The runtime creates and hides those
// parameters (IDs from MIDIControllerParamIDBase upward) and converts their
// changes back to sample-accurate events. The processor needs an event input
// bus.
type MIDIControllerProcessor interface {
	// MIDIControllerChannels returns how many MIDI channels (1-16) of the
	// first event input bus deliver controller events.
	MIDIControllerChannels() int
}

// MIDIMappingProcessor maps MIDI controllers directly to plugin parameters,
// for example mod wheel to a vibrato parameter. Mappings returned here take
// precedence over MIDIControllerProcessor events.
type MIDIMappingProcessor interface {
	MIDIControllerParameter(busIndex int32, channel int16, controller int16) (paramID uint32, ok bool)
}

// MIDI controller numbers beyond the 128 CCs, as used by IMidiMapping.
const (
	MIDIControllerAfterTouch int16 = 128 // channel pressure
	MIDIControllerPitchBend  int16 = 129
)

// HostAwareProcessor receives a Host handle once the host connects its
// component handler, and nil when the host disconnects it.
type HostAwareProcessor interface {
	SetHost(host Host)
}

// RestartFlags tell the host what to re-read after a plugin-side change.
type RestartFlags int32

// Restart flags (Steinberg::Vst::RestartFlags).
const (
	RestartReloadComponent        RestartFlags = 1 << 0
	RestartIOChanged              RestartFlags = 1 << 1
	RestartParamValuesChanged     RestartFlags = 1 << 2
	RestartLatencyChanged         RestartFlags = 1 << 3
	RestartParamTitlesChanged     RestartFlags = 1 << 4
	RestartMidiCCAssignmentChange RestartFlags = 1 << 5
	RestartNoteExpressionChanged  RestartFlags = 1 << 6
	RestartIOTitlesChanged        RestartFlags = 1 << 7
)

// Host is the plugin-to-host channel. Its methods must be called from the
// UI/main thread, never from ProcessAudio.
type Host interface {
	// RestartComponent asks the host to re-query state, e.g. latency.
	RestartComponent(flags RestartFlags) error
	// EditParameter changes a parameter from the plugin side (a preset
	// load, a MIDI-learn result, ...) and records it as a host edit
	// (beginEdit/performEdit/endEdit), so automation and undo see it.
	EditParameter(id uint32, normalized float64) error
}
