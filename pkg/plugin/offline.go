package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/midi"
)

// OfflineSetup configures an Offline instance.
type OfflineSetup struct {
	SampleRate float64 // default 48000
	// MaxBlockSize is the largest block passed to ProcessAudio; Process
	// splits longer buffers. Default 512.
	MaxBlockSize int
	// DoublePrecision processes in 64-bit (Process64); the processor must
	// implement DoublePrecisionProcessor.
	DoublePrecision bool
	// Mode is the process mode reported in ctx.ProcessMode.
	Mode process.ProcessMode
}

// Offline runs a plugin without a host: in unit tests, offline renderers
// and batch tools. It goes through the same runtime as a VST3 host
// (lifecycle, sample-accurate automation, MIDI controller mapping, programs,
// smoothing, state), on the calling goroutine, with every bus active.
//
// Offline is not safe for concurrent use.
type Offline struct {
	c         *componentImpl
	maxBlock  int
	double    bool
	mode      process.ProcessMode
	pending   []offlineChange
	events    []midi.Event
	outEvents []midi.Event
	outParams []process.ParameterChange
	restarts  RestartFlags
	messages  []OfflineMessage
	editorOut []OfflineEditorMessage

	// Transport is reported to the processor in every block. While
	// IsPlaying is set, Process advances ProjectTimeSamples and, with a
	// valid tempo, ProjectTimeMusic.
	Transport process.TransportInfo
}

type offlineChange struct {
	id     uint32
	value  float64
	offset int
}

// OfflineMessage is a message the processor sent with Host.SendMessage.
type OfflineMessage struct {
	ID   string
	Data []byte
}

// OfflineEditorMessage is a message the processor sent to the editor with
// Host.PostEditorMessage.
type OfflineEditorMessage struct {
	Name string
	Data json.RawMessage
}

var errOfflineBlock = errors.New("offline: channel buffers have different lengths")

// NewOffline creates, initializes and activates a processor of p.
func NewOffline(p Plugin, setup OfflineSetup) (*Offline, error) {
	if setup.SampleRate == 0 {
		setup.SampleRate = defaultSampleRate
	}
	if setup.MaxBlockSize == 0 {
		setup.MaxBlockSize = defaultMaxBlockSize
	}
	if setup.SampleRate < 0 || setup.MaxBlockSize < 0 {
		return nil, errInvalidArgument
	}
	c, err := newComponent(p.CreateProcessor(), p.GetInfo())
	if err != nil {
		return nil, err
	}
	o := &Offline{c: c, maxBlock: setup.MaxBlockSize, double: setup.DoublePrecision, mode: setup.Mode}
	if aware, ok := c.processor.(HostAwareProcessor); ok {
		aware.SetHost(offlineHost{o})
	}
	if err := c.Initialize(); err != nil {
		return nil, err
	}
	if err := c.SetupProcessing(setup.SampleRate, int32(setup.MaxBlockSize), setup.Mode, setup.DoublePrecision); err != nil {
		return nil, fmt.Errorf("offline setup: %w", err)
	}
	for _, media := range []bus.MediaType{bus.MediaTypeAudio, bus.MediaTypeEvent} {
		for _, dir := range []bus.Direction{bus.DirectionInput, bus.DirectionOutput} {
			for i := range c.GetBusCount(int32(media), int32(dir)) {
				_ = c.ActivateBus(int32(media), int32(dir), i, true)
			}
		}
	}
	if err := c.SetActive(true); err != nil {
		return nil, fmt.Errorf("offline activate: %w", err)
	}
	c.SetProcessing(true)
	return o, nil
}

// Close deactivates the processor.
func (o *Offline) Close() error {
	o.c.SetProcessing(false)
	return o.c.SetActive(false)
}

// Processor returns the processor instance.
func (o *Offline) Processor() Processor { return o.c.processor }

// SetParameter sets a parameter like an editor or host edit between blocks.
// Selecting a program (ProgramParamID) loads its values.
func (o *Offline) SetParameter(id uint32, normalized float64) error {
	return o.c.EditParameter(id, normalized)
}

// SetParameterPlain is SetParameter with a plain value.
func (o *Offline) SetParameterPlain(id uint32, plain float64) error {
	p, ok := o.c.params.GetOK(id)
	if !ok {
		return errInvalidArgument
	}
	return o.SetParameter(id, p.Normalize(plain))
}

// Parameter returns a parameter's normalized and plain value.
func (o *Offline) Parameter(id uint32) (normalized, plain float64, ok bool) {
	p, ok := o.c.params.GetOK(id)
	if !ok {
		return 0, 0, false
	}
	return p.GetNormalized(), p.GetPlain(), true
}

// Automate queues a sample-accurate parameter change for the next Process
// call. offset counts samples from the start of that call.
func (o *Offline) Automate(id uint32, offset int, normalized float64) error {
	if offset < 0 || (!o.c.params.Has(id) && !o.c.midiCC.owns(id)) {
		return errInvalidArgument
	}
	o.pending = append(o.pending, offlineChange{id, normalized, offset})
	return nil
}

// SendEvent queues an input event for the next Process call; event.Offset
// counts samples from the start of that call.
func (o *Offline) SendEvent(event midi.Event) {
	o.events = append(o.events, event)
}

// SendController queues a MIDI controller value (CC 0-127,
// MIDIControllerAfterTouch, MIDIControllerPitchBend) on the first event bus,
// the way a host delivers it: through the controller's MIDI mapping. value
// is normalized (pitch bend 0.5 is center).
func (o *Offline) SendController(channel int16, controller int16, offset int, value float64) error {
	id, ok := o.c.MIDIControllerAssignment(0, channel, controller)
	if !ok {
		return fmt.Errorf("offline: controller %d on channel %d is not mapped: %w", controller, channel, errInvalidArgument)
	}
	return o.Automate(id, offset, value)
}

// Process renders one buffer through the main audio buses. The buffer may
// be longer than MaxBlockSize; it is processed in blocks, with queued
// events and automation delivered at their offsets.
func (o *Offline) Process(in, out [][]float32) error {
	var ins, outs [][][]float32
	if in != nil {
		ins = [][][]float32{in}
	}
	if out != nil {
		outs = [][][]float32{out}
	}
	return o.ProcessBuses(ins, outs)
}

// ProcessBuses is Process with the channels of every audio bus:
// ins[bus][channel][sample]. Like a host, Offline passes silent input and
// scratch output buffers for buses and channels the caller leaves out.
func (o *Offline) ProcessBuses(ins, outs [][][]float32) error {
	if o.double {
		return fmt.Errorf("offline: set up for 64-bit, use Process64: %w", errInvalidArgument)
	}
	n, err := bufferLength(ins, outs)
	if err != nil {
		return err
	}
	return render(o, ins, outs, n, (*process.Context).BeginBlock, (*process.Context).AddInputChannel, (*process.Context).AddOutputChannel)
}

// Process64 is Process in 64-bit; it needs OfflineSetup.DoublePrecision.
func (o *Offline) Process64(in, out [][]float64) error {
	if !o.double {
		return fmt.Errorf("offline: not set up for 64-bit: %w", errInvalidArgument)
	}
	var ins, outs [][][]float64
	if in != nil {
		ins = [][][]float64{in}
	}
	if out != nil {
		outs = [][][]float64{out}
	}
	n, err := bufferLength(ins, outs)
	if err != nil {
		return err
	}
	return render(o, ins, outs, n, (*process.Context).BeginBlock64, (*process.Context).AddInputChannel64, (*process.Context).AddOutputChannel64)
}

// ProcessEvents runs n samples with silent input, for instruments, MIDI
// effects and analyzers; output audio is discarded.
func (o *Offline) ProcessEvents(n int) error {
	if n < 0 {
		return errInvalidArgument
	}
	if o.double {
		return render[float64](o, nil, nil, n, (*process.Context).BeginBlock64, (*process.Context).AddInputChannel64, (*process.Context).AddOutputChannel64)
	}
	return render[float32](o, nil, nil, n, (*process.Context).BeginBlock, (*process.Context).AddInputChannel, (*process.Context).AddOutputChannel)
}

// render processes n samples of the given buses (completed to the bus
// layout) in blocks of at most maxBlock.
func render[T float32 | float64](o *Offline, ins, outs [][][]T, n int,
	begin func(*process.Context, int), addIn, addOut func(*process.Context, int, []T)) error {
	ins = completeBuses(ins, o.c.audioChannelCounts(bus.DirectionInput), n)
	outs = completeBuses(outs, o.c.audioChannelCounts(bus.DirectionOutput), n)
	return o.render(n, func(ctx *process.Context, start, end int) {
		begin(ctx, end-start)
		for b, channels := range ins {
			for _, ch := range channels {
				addIn(ctx, b, ch[start:end])
			}
		}
		for b, channels := range outs {
			for _, ch := range channels {
				addOut(ctx, b, ch[start:end])
			}
		}
	})
}

// completeBuses returns buses with at least channels[b] channels of n
// samples per bus, adding zeroed buffers for the missing ones.
func completeBuses[T float32 | float64](buses [][][]T, channels []int, n int) [][][]T {
	out := make([][][]T, max(len(buses), len(channels)))
	for b := range out {
		if b < len(buses) {
			out[b] = buses[b]
		}
		if b < len(channels) {
			for len(out[b]) < channels[b] {
				out[b] = append(out[b], make([]T, n))
			}
		}
	}
	return out
}

func bufferLength[T float32 | float64](ins, outs [][][]T) (int, error) {
	n := -1
	for _, buses := range [][][][]T{ins, outs} {
		for _, channels := range buses {
			for _, ch := range channels {
				if n >= 0 && len(ch) != n {
					return 0, errOfflineBlock
				}
				n = len(ch)
			}
		}
	}
	return max(n, 0), nil
}

// render processes n samples in blocks of at most maxBlock. begin starts a
// block on the context and maps the audio of [start, end).
func (o *Offline) render(n int, begin func(ctx *process.Context, start, end int)) error {
	defer func() {
		o.pending = o.pending[:0]
		o.events = o.events[:0]
	}()
	limit := max(n, 1)
	for _, e := range o.events {
		if e.Offset < 0 || int(e.Offset) >= limit {
			return fmt.Errorf("offline: event offset %d outside the %d-sample buffer: %w", e.Offset, n, errInvalidArgument)
		}
	}
	for _, p := range o.pending {
		if p.offset >= limit {
			return fmt.Errorf("offline: automation offset %d outside the %d-sample buffer: %w", p.offset, n, errInvalidArgument)
		}
	}
	o.outEvents = o.outEvents[:0]
	o.outParams = o.outParams[:0]
	for start := 0; start < n || (n == 0 && start == 0); {
		end := min(start+o.maxBlock, n)
		o.processBlock(start, end, begin)
		if end == start {
			break
		}
		start = end
	}
	return nil
}

func (o *Offline) processBlock(start, end int, begin func(ctx *process.Context, start, end int)) {
	c := o.c
	ctx := c.processCtx
	begin(ctx, start, end)
	ctx.SampleRate = c.sampleRate
	ctx.ProcessMode = o.mode
	*ctx.Transport = o.Transport

	inBlock := func(offset int) bool { return offset >= start && (offset < end || end == start) }
	for _, e := range o.events {
		if offset := int(e.Offset); inBlock(offset) {
			ctx.AddInputEvent(e.At(int32(offset - start)))
		}
	}
	for _, p := range o.pending {
		if inBlock(p.offset) {
			c.queueParameterChange(ctx, p.id, p.value, p.offset-start)
		}
	}

	ctx.Dispatch(c.processor.ProcessAudio)
	c.publishOutputParameters(ctx)

	for _, e := range ctx.GetOutputEvents() {
		e.Offset += int32(start)
		e.Data = bytes.Clone(e.Data)
		o.outEvents = append(o.outEvents, e)
	}
	for _, p := range ctx.OutputParameterChanges() {
		p.SampleOffset += start
		o.outParams = append(o.outParams, p)
	}
	o.advanceTransport(end - start)
}

func (o *Offline) advanceTransport(n int) {
	if !o.Transport.IsPlaying || n == 0 {
		return
	}
	o.Transport.ProjectTimeSamples += int64(n)
	if o.Transport.HasTempo && o.Transport.HasMusicalTime {
		o.Transport.ProjectTimeMusic += float64(n) / o.c.sampleRate * o.Transport.Tempo / 60
	}
}

// OutputEvents returns the events the processor sent during the last
// Process call, with offsets from the start of that call.
func (o *Offline) OutputEvents() []midi.Event { return slices.Clone(o.outEvents) }

// OutputParameters returns the output parameter changes (meters) of the
// last Process call.
func (o *Offline) OutputParameters() []process.ParameterChange { return slices.Clone(o.outParams) }

// State returns the component state, as a host would save it.
func (o *Offline) State() ([]byte, error) { return o.c.GetState() }

// SetState restores a component state.
func (o *Offline) SetState(state []byte) error { return o.c.SetState(state) }

// Latency returns the reported latency in samples.
func (o *Offline) Latency() int { return int(o.c.GetLatencySamples()) }

// SampleRate returns the processing sample rate.
func (o *Offline) SampleRate() float64 { return o.c.sampleRate }

// TakeRestarts returns and clears the restart flags requested through
// Host.RestartComponent.
func (o *Offline) TakeRestarts() RestartFlags {
	flags := o.restarts
	o.restarts = 0
	return flags
}

// EditorMessage delivers a message like vst3go.send(name, data) from the
// editor page to an EditorMessageProcessor.
func (o *Offline) EditorMessage(name string, data any) error {
	receiver, ok := o.c.processor.(EditorMessageProcessor)
	if !ok || name == "" {
		return errNotSupported
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	receiver.EditorMessage(name, raw)
	return nil
}

// TakeEditorMessages returns and clears the messages sent to the editor
// through Host.PostEditorMessage.
func (o *Offline) TakeEditorMessages() []OfflineEditorMessage {
	out := o.editorOut
	o.editorOut = nil
	return out
}

// TakeMessages returns and clears the messages sent through
// Host.SendMessage.
func (o *Offline) TakeMessages() []OfflineMessage {
	messages := o.messages
	o.messages = nil
	return messages
}

// offlineHost is the Host handed to processors run by Offline.
type offlineHost struct{ o *Offline }

func (h offlineHost) RestartComponent(flags RestartFlags) error {
	h.o.restarts |= flags
	return nil
}

func (h offlineHost) EditParameter(id uint32, normalized float64) error {
	return h.o.c.EditParameter(id, normalized)
}

func (h offlineHost) BeginEdit(id uint32) error { return h.o.c.BeginEdit(id) }

func (h offlineHost) EndEdit(id uint32) error { return h.o.c.EndEdit(id) }

func (h offlineHost) SendMessage(id string, data []byte) error {
	h.o.messages = append(h.o.messages, OfflineMessage{ID: id, Data: bytes.Clone(data)})
	return nil
}

func (h offlineHost) PostEditorMessage(name string, data any) error {
	msg, err := customEditorMessage(name, data)
	if err != nil {
		return err
	}
	var decoded editorCustomMessage
	if err := json.Unmarshal(msg, &decoded); err != nil {
		return err
	}
	h.o.editorOut = append(h.o.editorOut, OfflineEditorMessage{Name: decoded.Name, Data: decoded.Data})
	return nil
}

func (h offlineHost) SavePreset(path string) error { return h.o.c.savePreset(path) }

func (h offlineHost) LoadPreset(path string) error {
	state, err := h.o.c.readPreset(path)
	if err != nil {
		return err
	}
	if err := h.o.c.SetState(state); err != nil {
		return err
	}
	h.o.restarts |= RestartParamValuesChanged
	return nil
}
