// Package plugin provides the runtime-facing VST3 plugin interfaces.
//
// This package is the contract consumed by the VST3 wrapper/runtime layer:
// callers implement Plugin, which produces a Processor. A Processor can opt
// into extra host features by implementing the optional interfaces below.
// docs/plugin-capabilities.md lists them by plugin type.
package plugin

import (
	"io"

	"github.com/meteuix/vst3go/v2/pkg/ara"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
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

// DistributedPlugin runs the audio processor and the edit controller as two
// separate VST3 objects that only talk through the host, which lets hosts
// run them in different processes or on different machines.
//
// The factory then exposes two classes. Each side gets its own Processor
// from CreateProcessor: the processor side runs Initialize and ProcessAudio,
// the controller side only serves parameters, the editor and the controller
// interfaces (it never calls Initialize or ProcessAudio). Parameter values
// and state reach the controller through the host; anything else goes
// through Host.SendMessage and MessageReceiver.
type DistributedPlugin interface {
	Plugin
	Distributed() bool
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

// DeadlineProcessor bounds how long the host's audio thread waits for a
// block; a processor that does not implement it gets
// plugin.DefaultProcessDeadline. Its blocks run on a dedicated real-time
// thread; the host's thread
// waits at most ProcessDeadline of each block's duration, and a block that
// takes longer outputs silence and finishes in the background (blocks
// arriving meanwhile output silence too, their events and parameter
// changes carried into the next block). A stall in Go, such as a garbage
// collection pause or the scheduler, then costs the plugin a block of audio
// instead of making the host miss its own deadline. Offline rendering
// always runs on the host's thread. Applies to VST3, CLAP, AU and LV2.
type DeadlineProcessor interface {
	// ProcessDeadline returns the share of a block's duration, from 0 to
	// 1, that the host waits; 0 processes on the host's thread, with no
	// bound. It is read when the plugin is activated.
	ProcessDeadline() float64
}

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

// StateParameterProcessor optionally limits automatic parameter persistence.
// Return a registry containing the persistent parameter objects from
// GetParameters. The default remains the full registry for existing plugins.
// This lets metering plugins omit transient readings and reset triggers.
type StateParameterProcessor interface {
	StateParameters() *param.Registry
}

// DoublePrecisionProcessor lets hosts process in 64-bit floating point.
// When SupportsDoublePrecision returns true, ProcessAudio must handle both
// ctx.Input/ctx.Output (32-bit) and ctx.Input64/ctx.Output64 (64-bit, when
// ctx.Is64Bit() is true).
type DoublePrecisionProcessor interface {
	SupportsDoublePrecision() bool
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
// pressure and pitch bend as midi.EventTypeControlChange,
// EventTypeChannelPressure and EventTypePitchBend input events.
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
// precedence over MIDIControllerProcessor events. After the mapping changes
// (for example after MIDI learn), call
// Host.RestartComponent(RestartMidiCCAssignmentChange).
type MIDIMappingProcessor interface {
	MIDIControllerParameter(busIndex int32, channel int16, controller int16) (paramID uint32, ok bool)
}

// MIDI controller numbers beyond the 128 CCs, as used by IMidiMapping.
const (
	MIDIControllerAfterTouch int16 = 128 // channel pressure
	MIDIControllerPitchBend  int16 = 129
)

// MIDI2Controller identifies a MIDI 2.0 registered or assignable controller
// (RPN/NRPN): a 7-bit bank and a 7-bit index.
type MIDI2Controller struct {
	Bank       uint8
	Index      uint8
	Registered bool // registered (RPN) rather than assignable (NRPN)
}

// MIDI2Assignment maps a MIDI 2.0 controller to a parameter.
type MIDI2Assignment struct {
	BusIndex   int32
	Channel    uint8
	Controller MIDI2Controller
	ParamID    uint32
}

// MIDI2MappingProcessor maps MIDI 2.0 controllers to parameters
// (IMidiMapping2). MIDI 1.0 assignments are taken from MIDIMappingProcessor
// and MIDIControllerProcessor.
type MIDI2MappingProcessor interface {
	MIDI2ControllerParameters() []MIDI2Assignment
}

// MIDILearnProcessor receives the controllers the user moves while the host
// is in MIDI learn mode (IMidiLearn), on the UI thread. Return true when the
// controller was learned; then update the MIDIMappingProcessor mapping and
// call Host.RestartComponent(RestartMidiCCAssignmentChange).
type MIDILearnProcessor interface {
	LearnMIDIController(busIndex int32, channel int16, controller int16) bool
}

// MIDI2LearnProcessor is MIDILearnProcessor for MIDI 2.0 controllers.
type MIDI2LearnProcessor interface {
	LearnMIDI2Controller(busIndex int32, channel uint8, controller MIDI2Controller) bool
}

// NoteExpressionFlags describe a note expression type.
type NoteExpressionFlags int32

const (
	NoteExpressionBipolar    NoteExpressionFlags = 1 << 0 // centered default (e.g. tuning, pan)
	NoteExpressionOneShot    NoteExpressionFlags = 1 << 1 // only one value per note
	NoteExpressionAbsolute   NoteExpressionFlags = 1 << 2 // absolute rather than relative to the note start
	NoteExpressionAssociated NoteExpressionFlags = 1 << 3 // AssociatedParamID is valid
)

// NoteExpressionInfo describes one note expression type a processor accepts.
// Values are normalized (0-1).
type NoteExpressionInfo struct {
	TypeID            uint32 // midi.NoteExpressionVolume ... or >= midi.NoteExpressionCustomStart
	Title             string
	ShortTitle        string
	Units             string
	UnitID            int32
	Default           float64
	Min, Max          float64 // zero Max means 1
	StepCount         int32
	Flags             NoteExpressionFlags
	AssociatedParamID uint32
	// Format and Parse convert between normalized values and display text.
	// Nil uses two decimals.
	Format func(normalized float64) string
	Parse  func(text string) (float64, error)
}

// NoteExpressionProcessor advertises per-note expressions
// (INoteExpressionController), which hosts such as Cubase require before
// they send midi.EventTypeNoteExpression events. It needs an event input bus.
type NoteExpressionProcessor interface {
	NoteExpressions(busIndex int32, channel int16) []NoteExpressionInfo
}

// PhysicalUI identifies a physical control dimension of an MPE/expressive
// controller.
type PhysicalUI uint32

const (
	PhysicalUIXMovement PhysicalUI = 0 // horizontal finger movement
	PhysicalUIYMovement PhysicalUI = 1 // vertical finger movement
	PhysicalUIPressure  PhysicalUI = 2
)

// PhysicalUIMapping maps a physical control to a note expression type.
type PhysicalUIMapping struct {
	PhysicalUI PhysicalUI
	TypeID     uint32
}

// PhysicalUIMappingProcessor tells MPE-aware hosts which note expression
// each physical dimension drives (INoteExpressionPhysicalUIMapping).
type PhysicalUIMappingProcessor interface {
	PhysicalUIMappings(busIndex int32, channel int16) []PhysicalUIMapping
}

// KeyswitchType is the behavior of a keyswitch.
type KeyswitchType uint32

const (
	KeyswitchNoteOn    KeyswitchType = 0 // articulation selected by a note-on
	KeyswitchOnTheFly  KeyswitchType = 1 // changes sounding notes
	KeyswitchOnRelease KeyswitchType = 2 // applied on note release
	KeyswitchKeyRange  KeyswitchType = 3 // the key range plays the articulation
)

// KeyswitchInfo describes one articulation keyswitch.
type KeyswitchInfo struct {
	Type       KeyswitchType
	Title      string
	ShortTitle string
	Min, Max   int32 // key range of the keyswitch
	// KeyRemapped is the note played when the keyswitch is triggered from a
	// host expression map (-1 for none).
	KeyRemapped int32
	UnitID      int32
	Flags       int32
}

// KeyswitchProcessor publishes articulation keyswitches
// (IKeyswitchController) so hosts can build expression maps.
type KeyswitchProcessor interface {
	Keyswitches(busIndex int32, channel int16) []KeyswitchInfo
}

// Program is one factory preset.
type Program struct {
	Name string
	// Values maps parameter IDs to plain values. Parameters that are not
	// listed are set to their defaults when the program is selected.
	Values map[uint32]float64
	// PitchNames names MIDI notes, for example the pads of a drum kit.
	PitchNames map[int16]string
}

// ProgramList is a named list of factory presets.
type ProgramList struct {
	Name     string
	Programs []Program
}

// ProgramListProcessor publishes factory presets in the host's program menu
// (IUnitInfo). The runtime adds a "Program" list parameter
// (ProgramParamID) whose changes, from the host, automation or MIDI program
// change, load the program's parameter values. Hosts can also store
// program data in the list slots (IProgramListData).
type ProgramListProcessor interface {
	ProgramList() ProgramList
}

// ProgramParamID is the parameter ID of the runtime-owned program selector.
// Plugin parameter IDs must not use it.
const ProgramParamID uint32 = 0x7EFFFFFF

// Unit groups parameters in host UIs (IUnitInfo). Parameters refer to a unit
// through param.Parameter.UnitID; unit 0 is the root.
type Unit struct {
	ID       int32
	ParentID int32
	Name     string
}

// UnitProcessor declares parameter groups.
type UnitProcessor interface {
	Units() []Unit
}

// ChannelInfo describes the mixer channel hosting the plugin.
type ChannelInfo struct {
	Name     string
	UID      string
	Color    uint32 // ARGB
	HasColor bool
	Index    int64 // channel number in IndexNamespace
	HasIndex bool
	// IndexNamespace names the numbering ("Input", "Channel", "Output").
	IndexNamespace string
	// PluginLocation is ChannelPluginPreFader, ...PostFader or ...Panner.
	PluginLocation    int64
	HasPluginLocation bool
}

// Plugin routing positions reported in ChannelInfo.PluginLocation.
const (
	ChannelPluginPreFader  int64 = 0
	ChannelPluginPostFader int64 = 1
	ChannelPluginPanner    int64 = 2
)

// ChannelInfoProcessor receives the host channel name, color and position
// (IInfoListener) on the UI thread, for example to tint the editor.
type ChannelInfoProcessor interface {
	SetChannelInfo(info ChannelInfo)
}

// MessageReceiver receives messages sent with Host.SendMessage by the other
// side of a DistributedPlugin, on a host (non-audio) thread. data is only
// valid during the call.
type MessageReceiver interface {
	ReceiveMessage(id string, data []byte)
}

// HostAwareProcessor receives a Host handle once the host connects its
// component handler, and nil when the host disconnects it. On the processor
// side of a DistributedPlugin it receives the handle at initialization.
type HostAwareProcessor interface {
	SetHost(host Host)
}

// ARAPlugin is a Plugin that supports ARA (Audio Random Access): in an
// ARA host it edits and renders the audio of the host's clips (playback
// regions) instead of the live signal. The runtime exposes an ARA factory
// for it in VST3 (the ARA main factory class and IPlugInEntryPoint), CLAP
// (the ARA factory and plugin extension) and AU (the ARA properties and
// the "ARA" component tag). See pkg/ara.
type ARAPlugin interface {
	Plugin
	// ARA returns the plugin's ARA configuration. It is read once, when
	// the host first asks for the ARA factory.
	ARA() ara.Config
}

// ARAProcessor is the processor of an ARAPlugin. When the host binds the
// instance to an ARA document, on the main thread before activating it,
// BindARA receives the binding; ProcessAudio then renders
// b.Playback() at sample position ctx.Transport.ProjectTimeSamples +
// ctx.BlockOffset(). An instance the host does not bind processes its
// input as a normal effect.
type ARAProcessor interface {
	BindARA(b *ara.Binding)
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
	RestartPrefetchableSupport    RestartFlags = 1 << 8
	RestartRoutingInfoChanged     RestartFlags = 1 << 9
	RestartKeyswitchChanged       RestartFlags = 1 << 10
)

// Host is the plugin-to-host channel. Its methods must be called from the
// UI/main thread, never from ProcessAudio.
//
// On the processor side of a DistributedPlugin, RestartComponent and
// EditParameter are forwarded to the controller, which performs them.
type Host interface {
	// RestartComponent asks the host to re-query state, e.g. latency.
	RestartComponent(flags RestartFlags) error
	// EditParameter changes a parameter from the plugin side (a preset
	// load, a MIDI-learn result, a custom control) and records it as a host
	// edit, so automation and undo see it. Outside a gesture it is a
	// complete edit; between BeginEdit and EndEdit it is one gesture step.
	EditParameter(id uint32, normalized float64) error
	// BeginEdit and EndEdit bracket a continuous edit such as a knob drag,
	// so the host records it as one automation pass and one undo step.
	BeginEdit(id uint32) error
	EndEdit(id uint32) error
	// SavePreset writes the plugin state to a .vstpreset file, which hosts
	// list in their preset browsers when it is in the user preset folder
	// (preset.UserDir).
	SavePreset(path string) error
	// LoadPreset restores a .vstpreset file saved for this plugin and
	// updates the host, the editor and the other side of a distributed
	// plugin.
	LoadPreset(path string) error
	// SendMessage delivers data to the MessageReceiver of the other side of
	// a DistributedPlugin. Single-component plugins have no other side and
	// get an error.
	SendMessage(id string, data []byte) error
	// PostEditorMessage sends data, encoded as JSON, to the open editor
	// page, where vst3go.on(name, ...) receives it; without an open editor
	// it is dropped. Call it from the main thread (for example from
	// EditorMessageProcessor.EditorMessage), not from ProcessAudio. On the
	// processor side of a DistributedPlugin it goes through the connection
	// to the controller side's editor.
	PostEditorMessage(name string, data any) error
}
