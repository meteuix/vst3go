package plugin

// #cgo CFLAGS: -I../../include
// #include "clap_bridge.h"
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"bytes"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/midi"
)

// The CLAP front end: the same binary that exports GetPluginFactory exports
// clap_entry (clap_bridge.c). A CLAP instance runs the componentImpl of a
// single-component VST3 instance; the runtime contract (lifecycle,
// sample-accurate automation, MIDI mapping, programs, smoothing, state,
// presets) is the same.

// clapInstance is one CLAP plugin instance. C holds only its ID.
type clapInstance struct {
	id     uintptr
	plugin *C.VST3GoClapPlugin
	c      *componentImpl
	params []*param.Parameter // host-visible parameters (no read-only meters)

	active         atomic.Bool
	latencyPending atomic.Bool // latency changed while active
	rescanPending  atomic.Bool // the audio thread changed the program
	notes          clapNotes   // audio thread
	seenProgram    int32       // audio thread: last program applied by process
	edits          *mpscQueue[clapEdit]
	editorPending  atomic.Bool // host automation changed values the editor shows
	hostChanged    bool        // audio thread: the block had host parameter changes
	// changed holds the values MIDI controllers changed through the MIDI
	// mapping in this block (audio thread), reported to the host after
	// processing.
	changed      [clapChangedSize]paramValue
	changedCount int

	editorSession
}

var (
	clapInstances instances[clapInstance]

	clapDescriptorsOnce sync.Once
	clapDescriptors     []*C.clap_plugin_descriptor_t
)

const clapEditQueueSize = 4096

// clapChangedSize bounds the MIDI-mapped parameter changes reported per
// block; later ones in a full block still reach the processor.
const clapChangedSize = 128

// clap.h defines these as static const variables, which cgo cannot link.
const (
	clapCoreEventSpaceID = 0          // CLAP_CORE_EVENT_SPACE_ID
	clapInvalidID        = 0xFFFFFFFF // CLAP_INVALID_ID
)

func getClap(id C.uintptr_t) *clapInstance {
	return clapInstances.get(uintptr(id))
}

// clapPlugins is the registered plugin list; CLAP has no distributed mode,
// so every plugin is one class.
func clapDescriptorList() []*C.clap_plugin_descriptor_t {
	clapDescriptorsOnce.Do(func() {
		for _, p := range registeredPlugins {
			info := p.GetInfo()
			desc := (*C.clap_plugin_descriptor_t)(C.calloc(1, C.size_t(unsafe.Sizeof(C.clap_plugin_descriptor_t{}))))
			desc.clap_version = C.clap_version_t{major: C.CLAP_VERSION_MAJOR, minor: C.CLAP_VERSION_MINOR, revision: C.CLAP_VERSION_REVISION}
			desc.id = C.CString(info.ID)
			desc.name = C.CString(info.Name)
			vendor := info.Vendor
			if vendor == "" {
				vendor = globalFactoryInfo.Vendor
			}
			desc.vendor = C.CString(vendor)
			desc.url = C.CString(globalFactoryInfo.URL)
			desc.manual_url = C.CString("")
			desc.support_url = C.CString("")
			desc.version = C.CString(info.Version)
			desc.description = C.CString("")
			features := clapFeatures(p, info)
			list := (**C.char)(C.calloc(C.size_t(len(features)+1), C.size_t(unsafe.Sizeof((*C.char)(nil)))))
			entries := unsafe.Slice(list, len(features)+1)
			for i, f := range features {
				entries[i] = C.CString(f)
			}
			desc.features = list
			clapDescriptors = append(clapDescriptors, desc)
		}
	})
	return clapDescriptors
}

//export GoClapPluginCount
func GoClapPluginCount() C.uint32_t {
	return C.uint32_t(len(clapDescriptorList()))
}

//export GoClapDescriptor
func GoClapDescriptor(index C.uint32_t) *C.clap_plugin_descriptor_t {
	list := clapDescriptorList()
	if int(index) >= len(list) {
		return nil
	}
	return list[index]
}

//export GoClapCreate
func GoClapCreate(index C.uint32_t, plugin *C.VST3GoClapPlugin) (id C.uintptr_t) {
	defer func() {
		if recover() != nil {
			id = 0
		}
	}()
	if int(index) >= len(registeredPlugins) {
		return 0
	}
	p := registeredPlugins[index]
	component, err := newComponent(p.CreateProcessor(), p.GetInfo())
	if err != nil {
		return 0
	}
	inst := &clapInstance{plugin: plugin, c: component, edits: newMPSCQueue[clapEdit](clapEditQueueSize)}
	for _, p := range component.params.All() {
		// Output parameters (meters) change on their own while processing,
		// which CLAP hosts treat as unrequested value changes; they stay
		// available to the editor and VST3 hosts.
		if p.Flags&param.IsReadOnly == 0 {
			inst.params = append(inst.params, p)
		}
	}
	if component.programs != nil {
		inst.seenProgram = component.programs.audioProgram.Load()
	}
	component.wrapper = inst
	component.ara = newARAInstance(int(index))
	inst.id = nextInstanceID()
	clapInstances.add(inst.id, inst)
	return C.uintptr_t(inst.id)
}

//export GoClapInit
func GoClapInit(id C.uintptr_t) (ok C.bool) {
	defer recoverClap(&ok)
	inst := getClap(id)
	if inst == nil || inst.c.Initialize() != nil {
		return false
	}
	if aware, ok := inst.c.processor.(HostAwareProcessor); ok {
		aware.SetHost(clapHost{inst})
	}
	inst.updateTrackInfo()
	return true
}

func recoverClap(ok *C.bool) {
	if recover() != nil {
		*ok = false
	}
}

//export GoClapDestroy
func GoClapDestroy(id C.uintptr_t) {
	inst := clapInstances.remove(uintptr(id))
	if inst == nil {
		return
	}
	if inst.active.Load() {
		_ = inst.c.SetActive(false)
	}
	if aware, ok := inst.c.processor.(HostAwareProcessor); ok {
		aware.SetHost(nil)
	}
	inst.c.ara.release()
}

//export GoClapActivate
func GoClapActivate(id C.uintptr_t, sampleRate C.double, minFrames, maxFrames C.uint32_t) (ok C.bool) {
	defer recoverClap(&ok)
	inst := getClap(id)
	c := inst.c
	if maxFrames == 0 || maxFrames > math.MaxInt32 {
		return false
	}
	if c.SetupProcessing(float64(sampleRate), int32(maxFrames), inst.processMode(), c.double) != nil {
		return false
	}
	// CLAP has no bus activation: every port is active.
	for _, media := range []bus.MediaType{bus.MediaTypeAudio, bus.MediaTypeEvent} {
		for _, dir := range []bus.Direction{bus.DirectionInput, bus.DirectionOutput} {
			for i := range c.GetBusCount(int32(media), int32(dir)) {
				_ = c.ActivateBus(int32(media), int32(dir), i, true)
			}
		}
	}
	if c.SetActive(true) != nil {
		return false
	}
	inst.notes.reset()
	inst.active.Store(true)
	if inst.latencyPending.Swap(false) {
		C.vst3go_clap_latency_changed(inst.plugin)
	}
	return true
}

//export GoClapRealtimeConfig
func GoClapRealtimeConfig(id C.uintptr_t, share *C.double, channels *C.uint32_t) {
	s, n := getClap(id).c.realtimeConfig()
	*share, *channels = C.double(s), C.uint32_t(n)
}

//export GoClapDeactivate
func GoClapDeactivate(id C.uintptr_t) {
	inst := getClap(id)
	inst.active.Store(false)
	_ = inst.c.SetActive(false)
}

//export GoClapReset
func GoClapReset(id C.uintptr_t) {
	// Audio thread: restart the processor without reallocating the context.
	inst := getClap(id)
	c := inst.c
	c.processCtx.ResetParameterChanges()
	c.processCtx.ClearAllEvents()
	inst.notes.reset()
	_ = c.processor.SetActive(false)
	_ = c.processor.SetActive(true)
}

//export GoClapOnMainThread
func GoClapOnMainThread(id C.uintptr_t) {
	inst := getClap(id)
	if inst == nil {
		return
	}
	if inst.rescanPending.Swap(false) {
		inst.restart(RestartParamValuesChanged)
		inst.editorPending.Store(true)
	}
	if inst.editorPending.Swap(false) {
		inst.refreshEditorParameters()
	}
}

// Processing

const (
	clapProcessError    = C.CLAP_PROCESS_ERROR
	clapProcessContinue = C.CLAP_PROCESS_CONTINUE
)

//export GoClapProcess
func GoClapProcess(id C.uintptr_t, proc *C.clap_process_t) C.clap_process_status {
	inst := getClap(id)
	if inst == nil || proc == nil {
		return clapProcessError
	}
	c := inst.c
	ctx := c.processCtx
	n := int(proc.frames_count)
	if n > int(c.maxBlockSize) {
		return clapProcessError
	}
	inputs := clapBuffers(proc.audio_inputs, proc.audio_inputs_count)
	outputs := clapBuffers(proc.audio_outputs, proc.audio_outputs_count)
	double := c.double && uses64(inputs, outputs)
	if double {
		ctx.BeginBlock64(n)
	} else {
		ctx.BeginBlock(n)
	}
	ctx.SampleRate = c.sampleRate
	ctx.ProcessMode = inst.processMode()
	clapTransport(ctx.Transport, proc.transport, int64(proc.steady_time), c.sampleRate)
	if n > 0 {
		mapClapBuffers(ctx, inputs, outputs, n, double)
	}

	out := proc.out_events
	inst.drainEdits(out)
	inst.hostChanged = false
	inst.changedCount = 0
	inst.collectEvents(ctx, proc.in_events, true)

	ctx.Dispatch(c.processor.ProcessAudio)
	if inst.hostChanged && inst.open.Load() && !inst.editorPending.Swap(true) {
		// Show host automation in the editor, from the main thread.
		C.vst3go_clap_request_callback(inst.plugin)
	}

	for b := range outputs {
		outputs[b].constant_mask = C.uint64_t(ctx.OutputSilenceFlags(b))
	}
	c.publishOutputParameters(ctx)
	// Values the plugin changed itself go to the host as parameter events
	// (at frame 0, before the block's other output events, which keeps the
	// list sorted): MIDI-mapped controllers, then a newly selected program.
	for _, v := range inst.changed[:inst.changedCount] {
		inst.pushValue(out, v)
	}
	if c.programs != nil {
		// A program change (host automation or MIDI) changed the values of
		// the program's parameters, which the host must re-read.
		if index := c.programs.audioProgram.Load(); index != inst.seenProgram {
			inst.seenProgram = index
			c.programs.uiProgram.Store(index)
			inst.pushValue(out, paramValue{id: ProgramParamID, normalized: c.programs.param.Normalize(float64(index))})
			for _, v := range c.programs.valuesOf(index) {
				inst.pushValue(out, v)
			}
			inst.rescanPending.Store(true)
			C.vst3go_clap_request_callback(inst.plugin)
		}
	}
	inst.writeEvents(ctx, out)
	return clapProcessContinue
}

func clapBuffers(buffers *C.clap_audio_buffer_t, count C.uint32_t) []C.clap_audio_buffer_t {
	if buffers == nil || count == 0 {
		return nil
	}
	return unsafe.Slice(buffers, int(count))
}

// uses64 reports whether the host passes 64-bit buffers.
func uses64(inputs, outputs []C.clap_audio_buffer_t) bool {
	for _, list := range [][]C.clap_audio_buffer_t{outputs, inputs} {
		for i := range list {
			if list[i].channel_count > 0 {
				return list[i].data32 == nil && list[i].data64 != nil
			}
		}
	}
	return false
}

func mapClapBuffers(ctx *process.Context, inputs, outputs []C.clap_audio_buffer_t, n int, double bool) {
	for b := range inputs {
		var silence uint64
		count := int(inputs[b].channel_count)
		if double {
			for ch, ptr := range clapChannels(unsafe.Pointer(inputs[b].data64), count) {
				samples := unsafe.Slice((*float64)(ptr), n)
				if inputs[b].constant_mask&(1<<ch) != 0 && samples[0] == 0 {
					silence |= 1 << ch
				}
				ctx.AddInputChannel64(b, samples)
			}
		} else {
			for ch, ptr := range clapChannels(unsafe.Pointer(inputs[b].data32), count) {
				samples := unsafe.Slice((*float32)(ptr), n)
				if inputs[b].constant_mask&(1<<ch) != 0 && samples[0] == 0 {
					silence |= 1 << ch
				}
				ctx.AddInputChannel(b, samples)
			}
		}
		ctx.SetInputSilenceFlags(b, silence)
	}
	for b := range outputs {
		count := int(outputs[b].channel_count)
		if double {
			for _, ptr := range clapChannels(unsafe.Pointer(outputs[b].data64), count) {
				ctx.AddOutputChannel64(b, unsafe.Slice((*float64)(ptr), n))
			}
		} else {
			for _, ptr := range clapChannels(unsafe.Pointer(outputs[b].data32), count) {
				ctx.AddOutputChannel(b, unsafe.Slice((*float32)(ptr), n))
			}
		}
	}
}

// clapChannels returns the channel pointers of a buffer, or none when the
// host left any of them out.
func clapChannels(channels unsafe.Pointer, count int) []unsafe.Pointer {
	if channels == nil || count <= 0 {
		return nil
	}
	ptrs := unsafe.Slice((*unsafe.Pointer)(channels), count)
	for _, ptr := range ptrs {
		if ptr == nil {
			return nil
		}
	}
	return ptrs
}

func clapTransport(t *process.TransportInfo, tr *C.clap_event_transport_t, steadyTime int64, sampleRate float64) {
	*t = process.TransportInfo{}
	if steadyTime >= 0 {
		t.ContinuousTimeSamples = steadyTime
	}
	if tr == nil {
		return
	}
	flags := uint32(tr.flags)
	t.IsPlaying = flags&C.CLAP_TRANSPORT_IS_PLAYING != 0
	t.IsRecording = flags&C.CLAP_TRANSPORT_IS_RECORDING != 0
	t.IsCycling = flags&C.CLAP_TRANSPORT_IS_LOOP_ACTIVE != 0
	if flags&C.CLAP_TRANSPORT_HAS_TEMPO != 0 {
		t.HasTempo = true
		t.Tempo = float64(tr.tempo)
	}
	if flags&C.CLAP_TRANSPORT_HAS_TIME_SIGNATURE != 0 {
		t.HasTimeSignature = true
		t.TimeSigNumerator = int32(tr.tsig_num)
		t.TimeSigDenominator = int32(tr.tsig_denom)
	}
	const fixed = float64(1 << 31)
	if flags&C.CLAP_TRANSPORT_HAS_BEATS_TIMELINE != 0 {
		t.HasMusicalTime = true
		t.ProjectTimeMusic = float64(tr.song_pos_beats) / fixed
		t.HasBarPosition = true
		t.BarPositionMusic = float64(tr.bar_start) / fixed
		t.HasCycle = true
		t.CycleStartMusic = float64(tr.loop_start_beats) / fixed
		t.CycleEndMusic = float64(tr.loop_end_beats) / fixed
	}
	if flags&C.CLAP_TRANSPORT_HAS_SECONDS_TIMELINE != 0 {
		t.ProjectTimeSamples = int64(math.Round(float64(tr.song_pos_seconds) / fixed * sampleRate))
	}
}

// collectEvents routes host events. In process they become sample-accurate
// changes and input events; in flush (processing false) parameter values
// are applied directly.
func (inst *clapInstance) collectEvents(ctx *process.Context, in *C.clap_input_events_t, processing bool) {
	count := C.vst3go_clap_events_size(in)
	for i := C.uint32_t(0); i < count; i++ {
		header := C.vst3go_clap_events_get(in, i)
		if header == nil || header.space_id != clapCoreEventSpaceID {
			continue
		}
		offset := int32(header.time)
		switch header._type {
		case C.CLAP_EVENT_PARAM_VALUE:
			ev := (*C.clap_event_param_value_t)(unsafe.Pointer(header))
			inst.paramValue(ctx, uint32(ev.param_id), float64(ev.value), int(offset), processing)
		case C.CLAP_EVENT_TRANSPORT:
			if processing && offset == 0 {
				clapTransport(ctx.Transport, (*C.clap_event_transport_t)(unsafe.Pointer(header)), ctx.Transport.ContinuousTimeSamples, inst.c.sampleRate)
			}
		}
		if !processing {
			continue
		}
		switch header._type {
		case C.CLAP_EVENT_NOTE_ON, C.CLAP_EVENT_NOTE_OFF, C.CLAP_EVENT_NOTE_CHOKE:
			inst.noteEvent(ctx, (*C.clap_event_note_t)(unsafe.Pointer(header)), offset)
		case C.CLAP_EVENT_NOTE_EXPRESSION:
			inst.noteExpression(ctx, (*C.clap_event_note_expression_t)(unsafe.Pointer(header)), offset)
		case C.CLAP_EVENT_MIDI:
			ev := (*C.clap_event_midi_t)(unsafe.Pointer(header))
			inst.routeMIDI(ctx, int32(ev.port_index), clapRawMIDI(&inst.notes, [3]byte{byte(ev.data[0]), byte(ev.data[1]), byte(ev.data[2])}, offset), offset)
		case C.CLAP_EVENT_MIDI2:
			ev := (*C.clap_event_midi2_t)(unsafe.Pointer(header))
			words := [4]uint32{uint32(ev.data[0]), uint32(ev.data[1]), uint32(ev.data[2]), uint32(ev.data[3])}
			inst.routeMIDI(ctx, int32(ev.port_index), clapMIDI2(&inst.notes, words, offset), offset)
		case C.CLAP_EVENT_MIDI_SYSEX:
			ev := (*C.clap_event_midi_sysex_t)(unsafe.Pointer(header))
			if ev.buffer == nil || ev.size == 0 {
				continue
			}
			data := ctx.EventDataBuffer(int(ev.size))
			copy(data, unsafe.Slice((*byte)(unsafe.Pointer(ev.buffer)), int(ev.size)))
			event := midi.SysEx(data).At(offset)
			event.NoteID = midi.NoteIDNone
			ctx.AddInputEvent(event)
		}
	}
}

func (inst *clapInstance) paramValue(ctx *process.Context, id uint32, plain float64, offset int, processing bool) {
	c := inst.c
	p, ok := inst.param(id)
	if !ok {
		return
	}
	normalized := clapNormalized(p, plain)
	inst.hostChanged = true
	if processing {
		c.queueParameterChange(ctx, id, normalized, offset)
		return
	}
	p.SetNormalized(normalized)
	if id == ProgramParamID && c.programs != nil {
		index := c.programs.index(normalized)
		if c.programs.audioProgram.Swap(index) != index {
			for _, v := range c.programs.valuesOf(index) {
				if target, ok := c.params.GetOK(v.id); ok {
					target.SetNormalized(v.normalized)
				}
			}
			c.programs.uiProgram.Store(index)
			inst.rescanPending.Store(true)
			C.vst3go_clap_request_callback(inst.plugin)
		}
	}
}

func (inst *clapInstance) noteEvent(ctx *process.Context, ev *C.clap_event_note_t, offset int32) {
	channel, key, noteID := int32(ev.channel), int32(ev.key), int32(ev.note_id)
	if ev.header._type == C.CLAP_EVENT_NOTE_ON {
		if channel < 0 || channel > 15 || key < 0 || key > 127 {
			return
		}
		event := midi.NoteOn(uint8(channel), uint8(key), float32(ev.velocity)).At(offset)
		event.NoteID = inst.notes.noteOn(channel, key, noteID)
		ctx.AddInputEvent(event)
		return
	}
	velocity := float32(ev.velocity)
	if ev.header._type == C.CLAP_EVENT_NOTE_CHOKE {
		velocity = 0
	}
	inst.notes.match(channel, key, noteID, func(ch, k, id int32) {
		inst.notes.noteOff(ch, k)
		event := midi.NoteOff(uint8(ch), uint8(k), velocity).At(offset)
		event.NoteID = id
		ctx.AddInputEvent(event)
	})
}

func (inst *clapInstance) noteExpression(ctx *process.Context, ev *C.clap_event_note_expression_t, offset int32) {
	typeID, value, pressure, ok := clapExpressionToVST3(int32(ev.expression_id), float64(ev.value))
	if !ok {
		return
	}
	inst.notes.match(int32(ev.channel), int32(ev.key), int32(ev.note_id), func(ch, k, id int32) {
		var event midi.Event
		if pressure {
			event = midi.PolyPressure(uint8(ch), uint8(k), value)
			event.NoteID = id
		} else {
			event = midi.NoteExpression(id, typeID, value)
			event.Channel, event.Note = uint8(ch), uint8(k)
		}
		ctx.AddInputEvent(event.At(offset))
	})
}

// routeMIDI routes a converted MIDI message: notes, pressure and note
// expressions become events, controllers go through the MIDI mapping like
// on VST3 hosts, and program changes select a program.
func (inst *clapInstance) routeMIDI(ctx *process.Context, port int32, msg clapMIDIMessage, offset int32) {
	c := inst.c
	switch {
	case msg.isEvent:
		ctx.AddInputEvent(msg.event)
	case msg.isCtrl:
		if id, ok := c.MIDIControllerAssignment(port, int16(msg.event.Channel), msg.controller); ok {
			c.queueParameterChange(ctx, id, msg.value, int(offset))
			if inst.changedCount < len(inst.changed) {
				inst.changed[inst.changedCount] = paramValue{id: id, normalized: msg.value}
				inst.changedCount++
			}
		}
	case msg.program >= 0 && c.programs != nil && msg.program < c.programs.count():
		c.queueParameterChange(ctx, ProgramParamID, c.programs.param.Normalize(float64(msg.program)), int(offset))
	}
}

// drainEdits reports plugin-side edits (Host.EditParameter, the editor) to
// the host as gesture and value events.
func (inst *clapInstance) drainEdits(out *C.clap_output_events_t) {
	if out == nil {
		return
	}
	for {
		e, ok := inst.edits.pop()
		if !ok {
			return
		}
		switch e.kind {
		case editBegin:
			C.vst3go_clap_push_gesture(out, C.CLAP_EVENT_PARAM_GESTURE_BEGIN, 0, C.clap_id(e.id))
		case editEnd:
			C.vst3go_clap_push_gesture(out, C.CLAP_EVENT_PARAM_GESTURE_END, 0, C.clap_id(e.id))
		default:
			C.vst3go_clap_push_param(out, 0, C.clap_id(e.id), C.double(e.plain))
		}
	}
}

// pushValue reports a parameter value the plugin changed itself to the
// host. Hidden MIDI controller parameters and meters are not host-visible.
func (inst *clapInstance) pushValue(out *C.clap_output_events_t, v paramValue) {
	p, ok := inst.param(v.id)
	if out == nil || !ok || inst.c.midiCC.owns(v.id) {
		return
	}
	C.vst3go_clap_push_param(out, 0, C.clap_id(v.id), C.double(clapPlain(p, v.normalized)))
}

// writeEvents sends the block's output events.
func (inst *clapInstance) writeEvents(ctx *process.Context, out *C.clap_output_events_t) {
	if out == nil {
		return
	}
	events := ctx.GetOutputEvents()
	for i := range events {
		inst.pushEvent(out, &events[i])
	}
}

func (inst *clapInstance) pushEvent(out *C.clap_output_events_t, event *midi.Event) {
	time := C.uint32_t(max(event.Offset, 0))
	channel := C.int16_t(event.Channel & 0x0F)
	key := C.int16_t(event.Note & 0x7F)
	noteID := C.int32_t(event.NoteID)
	switch event.Type {
	case midi.EventTypeNoteOn:
		C.vst3go_clap_push_note(out, C.CLAP_EVENT_NOTE_ON, time, 0, channel, key, noteID, C.double(event.Velocity))
	case midi.EventTypeNoteOff:
		C.vst3go_clap_push_note(out, C.CLAP_EVENT_NOTE_OFF, time, 0, channel, key, noteID, C.double(event.Velocity))
	case midi.EventTypePolyPressure:
		C.vst3go_clap_push_expression(out, time, clapExprPressure, 0, channel, key, noteID, C.double(clampUnit(event.Value)))
	case midi.EventTypeNoteExpression:
		if id, value, ok := vst3ExpressionToCLAP(event.ExpressionType, event.Value); ok {
			C.vst3go_clap_push_expression(out, time, C.int32_t(id), 0, -1, -1, noteID, C.double(value))
		}
	case midi.EventTypeSysEx:
		if len(event.Data) > 0 {
			C.vst3go_clap_push_sysex(inst.plugin, out, time, 0, (*C.uint8_t)(unsafe.Pointer(&event.Data[0])), C.uint32_t(len(event.Data)))
		}
	default:
		if data, ok := clapMIDIBytes(event); ok {
			C.vst3go_clap_push_midi(out, time, 0, C.uint8_t(data[0]), C.uint8_t(data[1]), C.uint8_t(data[2]))
		}
	}
}

// frontend

func (inst *clapInstance) queueEdit(e clapEdit) {
	// A full queue means the host is not calling process or flush.
	inst.edits.push(e)
	C.vst3go_clap_request_flush(inst.plugin)
}

func (inst *clapInstance) notifyParamBeginEdit(id uint32) {
	if _, ok := inst.param(id); ok {
		inst.queueEdit(clapEdit{kind: editBegin, id: id})
	}
}

func (inst *clapInstance) notifyParamPerformEdit(id uint32, normalized float64) {
	p, ok := inst.param(id)
	if !ok {
		return
	}
	if id == ProgramParamID && inst.c.programs != nil {
		// CLAP hosts do not echo plugin edits back to process: the
		// program's values are loaded on this thread (selectProgram).
		inst.c.programs.audioProgram.Store(inst.c.programs.index(normalized))
	}
	inst.queueEdit(clapEdit{kind: editPerform, id: id, plain: clapPlain(p, normalized)})
}

func (inst *clapInstance) notifyParamEndEdit(id uint32) {
	if _, ok := inst.param(id); ok {
		inst.queueEdit(clapEdit{kind: editEnd, id: id})
	}
}

func (inst *clapInstance) refreshEditorParameters() {
	inst.refreshEditor(inst.c.params)
}

// GoClapGuiCreateView creates the platform editor view of an instance for
// the CLAP gui extension (clap_gui.c).
//
//export GoClapGuiCreateView
func GoClapGuiCreateView(id C.uintptr_t) unsafe.Pointer {
	inst := getClap(id)
	if inst == nil {
		return nil
	}
	view := C.VST3GoCreateEditorView(id)
	if view != nil {
		inst.setEditorView(view)
	}
	return view
}

// restart maps VST3 restart flags to CLAP host notifications. Main thread.
func (inst *clapInstance) restart(flags RestartFlags) {
	p := inst.plugin
	active := inst.active.Load()
	if flags&RestartReloadComponent != 0 || (flags&RestartIOChanged != 0 && active) {
		C.vst3go_clap_request_restart(p)
	} else if flags&RestartIOChanged != 0 {
		C.vst3go_clap_audio_ports_rescan(p, C.CLAP_AUDIO_PORTS_RESCAN_LIST)
	}
	if flags&RestartIOTitlesChanged != 0 {
		C.vst3go_clap_audio_ports_rescan(p, C.CLAP_AUDIO_PORTS_RESCAN_NAMES)
	}
	if flags&RestartLatencyChanged != 0 {
		if active {
			inst.latencyPending.Store(true)
			C.vst3go_clap_request_restart(p)
		} else {
			C.vst3go_clap_latency_changed(p)
		}
	}
	if flags&RestartParamTitlesChanged != 0 {
		C.vst3go_clap_params_rescan(p, C.CLAP_PARAM_RESCAN_INFO|C.CLAP_PARAM_RESCAN_TEXT)
	}
	if flags&RestartParamValuesChanged != 0 {
		C.vst3go_clap_params_rescan(p, C.CLAP_PARAM_RESCAN_VALUES|C.CLAP_PARAM_RESCAN_TEXT)
		if inst.c.programs != nil {
			C.vst3go_clap_note_names_changed(p)
		}
	}
}

// clapHost is the Host handed to processors running as CLAP plugins.
type clapHost struct{ inst *clapInstance }

func (h clapHost) RestartComponent(flags RestartFlags) error {
	h.inst.restart(flags)
	return nil
}

func (h clapHost) EditParameter(id uint32, normalized float64) error {
	return h.inst.c.EditParameter(id, normalized)
}

func (h clapHost) BeginEdit(id uint32) error { return h.inst.c.BeginEdit(id) }

func (h clapHost) EndEdit(id uint32) error { return h.inst.c.EndEdit(id) }

func (h clapHost) SavePreset(path string) error { return h.inst.c.savePreset(path) }

func (h clapHost) LoadPreset(path string) error {
	state, err := h.inst.c.readPreset(path)
	if err != nil {
		return err
	}
	if err := h.inst.c.SetState(state); err != nil {
		return err
	}
	h.inst.restart(RestartParamValuesChanged)
	h.inst.refreshEditorParameters()
	C.vst3go_clap_state_mark_dirty(h.inst.plugin)
	return nil
}

func (h clapHost) SendMessage(string, []byte) error { return errNotSupported }

func (h clapHost) PostEditorMessage(name string, data any) error {
	msg, err := customEditorMessage(name, data)
	if err != nil {
		return err
	}
	h.inst.postRaw(msg)
	return nil
}

// Extensions

// param returns a host-visible parameter.
func (inst *clapInstance) param(id uint32) (*param.Parameter, bool) {
	p, ok := inst.c.params.GetOK(id)
	if !ok || p.Flags&param.IsReadOnly != 0 {
		return nil, false
	}
	return p, true
}

//export GoClapParamsCount
func GoClapParamsCount(id C.uintptr_t) C.uint32_t {
	return C.uint32_t(len(getClap(id).params))
}

//export GoClapParamsInfo
func GoClapParamsInfo(id C.uintptr_t, index C.uint32_t, info *C.clap_param_info_t) C.bool {
	params := getClap(id).params
	if int(index) >= len(params) {
		return false
	}
	p := params[index]
	*info = C.clap_param_info_t{}
	info.id = C.clap_id(p.ID)
	info.flags = C.clap_param_info_flags(clapParamFlags(p))
	copyCString(&info.name[0], len(info.name), p.Name)
	lo, hi := clapParamRange(p)
	info.min_value = C.double(lo)
	info.max_value = C.double(hi)
	info.default_value = C.double(clapPlain(p, p.DefaultValue))
	return true
}

//export GoClapParamsValue
func GoClapParamsValue(id C.uintptr_t, paramID C.clap_id, value *C.double) C.bool {
	p, ok := getClap(id).param(uint32(paramID))
	if !ok {
		return false
	}
	*value = C.double(clapPlain(p, p.GetNormalized()))
	return true
}

//export GoClapParamsValueToText
func GoClapParamsValueToText(id C.uintptr_t, paramID C.clap_id, value C.double, out *C.char, size C.uint32_t) C.bool {
	p, ok := getClap(id).param(uint32(paramID))
	if !ok {
		return false
	}
	copyCString(out, int(size), clapParamText(p, float64(value)))
	return true
}

//export GoClapParamsTextToValue
func GoClapParamsTextToValue(id C.uintptr_t, paramID C.clap_id, text *C.char, value *C.double) C.bool {
	p, ok := getClap(id).param(uint32(paramID))
	if !ok {
		return false
	}
	plain, err := clapParamValue(p, C.GoString(text))
	if err != nil {
		return false
	}
	*value = C.double(plain)
	return true
}

//export GoClapParamsFlush
func GoClapParamsFlush(id C.uintptr_t, in *C.clap_input_events_t, out *C.clap_output_events_t) {
	inst := getClap(id)
	inst.hostChanged = false
	if in != nil {
		inst.collectEvents(nil, in, false)
	}
	inst.drainEdits(out)
	if inst.hostChanged && inst.open.Load() && !inst.editorPending.Swap(true) {
		C.vst3go_clap_request_callback(inst.plugin)
	}
}

//export GoClapStateSave
func GoClapStateSave(id C.uintptr_t, stream *C.clap_ostream_t) (ok C.bool) {
	defer recoverClap(&ok)
	state, err := getClap(id).c.GetState()
	if err != nil {
		return false
	}
	// The stream may write fewer bytes per call.
	buffer := C.CBytes(state)
	defer C.free(buffer)
	for written := 0; written < len(state); {
		n := C.vst3go_clap_ostream_write(stream, unsafe.Add(buffer, written), C.uint64_t(len(state)-written))
		if n <= 0 {
			return false
		}
		written += int(n)
	}
	return true
}

//export GoClapStateLoad
func GoClapStateLoad(id C.uintptr_t, stream *C.clap_istream_t) (ok C.bool) {
	defer recoverClap(&ok)
	const chunk = 4096
	buffer := C.malloc(chunk)
	defer C.free(buffer)
	var state bytes.Buffer
	for {
		n := C.vst3go_clap_istream_read(stream, buffer, chunk)
		if n < 0 {
			return false
		}
		if n == 0 {
			break
		}
		state.Write(C.GoBytes(buffer, C.int(n)))
	}
	inst := getClap(id)
	if inst.c.SetState(state.Bytes()) != nil {
		return false
	}
	inst.restart(RestartParamValuesChanged)
	inst.refreshEditorParameters()
	return true
}

//export GoClapAudioPortsCount
func GoClapAudioPortsCount(id C.uintptr_t, isInput C.bool) C.uint32_t {
	return C.uint32_t(getClap(id).c.buses.GetBusCount(bus.MediaTypeAudio, clapDirection(isInput)))
}

func clapDirection(isInput C.bool) bus.Direction {
	if isInput {
		return bus.DirectionInput
	}
	return bus.DirectionOutput
}

// clapOutputPortID keeps output port IDs apart from input port IDs.
const clapOutputPortID = 1 << 16

var (
	clapPortMono     = C.CString("mono")
	clapPortStereo   = C.CString("stereo")
	clapPortSurround = C.CString("surround")
)

//export GoClapAudioPortsGet
func GoClapAudioPortsGet(id C.uintptr_t, index C.uint32_t, isInput C.bool, info *C.clap_audio_port_info_t) C.bool {
	c := getClap(id).c
	b := c.buses.GetBusInfo(bus.MediaTypeAudio, clapDirection(isInput), int32(index))
	if b == nil {
		return false
	}
	*info = C.clap_audio_port_info_t{}
	info.id = C.clap_id(index)
	if !isInput {
		info.id |= clapOutputPortID
	}
	copyCString(&info.name[0], len(info.name), b.Name)
	if index == 0 && b.BusType == bus.TypeMain {
		info.flags |= C.CLAP_AUDIO_PORT_IS_MAIN
	}
	if c.double {
		info.flags |= C.CLAP_AUDIO_PORT_SUPPORTS_64BITS
	}
	info.channel_count = C.uint32_t(b.ChannelCount)
	switch arrangement := b.SpeakerArrangement(); {
	case b.ChannelCount == 1:
		info.port_type = clapPortMono
	case arrangement == bus.ArrangementStereo:
		info.port_type = clapPortStereo
	case clapSurroundArrangement(arrangement):
		info.port_type = clapPortSurround
	}
	info.in_place_pair = clapInvalidID
	return true
}

// clapSurroundSpeakers is the number of CLAP surround channel IDs that are
// VST3 speaker bits: IDs 0 to 17 (front left to top back right) name the
// speaker of the bit with the same number, and both order the channels of
// a port by it.
const clapSurroundSpeakers = 18

// clapSurroundArrangement reports whether an arrangement of three or more
// speakers has only speakers with CLAP surround IDs.
func clapSurroundArrangement(a bus.SpeakerArrangement) bool {
	return a.ChannelCount() >= 3 && a>>clapSurroundSpeakers == 0
}

// clapRequestArrangement converts a configurable-audio-ports request to a
// speaker arrangement: mono and stereo ports, surround ports by their
// channel map (in speaker order), and ports without a type by their
// channel count.
func clapRequestArrangement(r *C.clap_audio_port_configuration_request_t) (bus.SpeakerArrangement, bool) {
	channels := int32(r.channel_count)
	if r.port_type == nil {
		a := bus.ArrangementForChannels(channels)
		return a, a.ChannelCount() == channels
	}
	switch C.GoString(r.port_type) {
	case "mono":
		return bus.ArrangementMono, channels == 1
	case "stereo":
		return bus.ArrangementStereo, channels == 2
	case "surround":
		if r.port_details == nil || channels < 1 || channels > clapSurroundSpeakers {
			return 0, false
		}
		var a bus.SpeakerArrangement
		last := -1
		for _, id := range unsafe.Slice((*uint8)(r.port_details), channels) {
			if int(id) <= last || id >= clapSurroundSpeakers {
				return 0, false
			}
			last = int(id)
			a |= 1 << id
		}
		return a, true
	}
	return 0, false
}

// GoClapConfigurePorts checks (apply false) or applies a configurable-audio-ports
// request; the plugin is deactivated.
//
//export GoClapConfigurePorts
func GoClapConfigurePorts(id C.uintptr_t, requests *C.clap_audio_port_configuration_request_t, count C.uint32_t, apply C.bool) C.bool {
	inst := getClap(id)
	c := inst.c
	if inst.active.Load() || (count > 0 && requests == nil) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	inputs, outputs := c.busArrangements(bus.DirectionInput), c.busArrangements(bus.DirectionOutput)
	for _, r := range unsafe.Slice(requests, count) {
		ports := outputs
		if r.is_input {
			ports = inputs
		}
		a, ok := clapRequestArrangement(&r)
		if !ok || int(r.port_index) >= len(ports) {
			return false
		}
		ports[r.port_index] = a
	}
	return C.bool(c.applyBusArrangements(inputs, outputs, bool(apply)))
}

// GoClapSurroundMaskSupported reports whether the main ports accept a
// surround channel mask.
//
//export GoClapSurroundMaskSupported
func GoClapSurroundMaskSupported(id C.uintptr_t, mask C.uint64_t) C.bool {
	a := bus.SpeakerArrangement(mask)
	if a == 0 || a>>clapSurroundSpeakers != 0 {
		return false
	}
	c := getClap(id).c
	c.mu.Lock()
	defer c.mu.Unlock()
	inputs, outputs := c.busArrangements(bus.DirectionInput), c.busArrangements(bus.DirectionOutput)
	for _, ports := range [][]bus.SpeakerArrangement{inputs, outputs} {
		if len(ports) > 0 {
			ports[0] = a
		}
	}
	return C.bool(c.applyBusArrangements(inputs, outputs, false))
}

// GoClapSurroundChannelMap writes the surround channel IDs of a port in
// channel order and returns their number.
//
//export GoClapSurroundChannelMap
func GoClapSurroundChannelMap(id C.uintptr_t, isInput C.bool, index C.uint32_t, channelMap *C.uint8_t, capacity C.uint32_t) C.uint32_t {
	b := getClap(id).c.buses.GetBusInfo(bus.MediaTypeAudio, clapDirection(isInput), int32(index))
	if b == nil || !clapSurroundArrangement(b.SpeakerArrangement()) {
		return 0
	}
	out := unsafe.Slice((*uint8)(channelMap), capacity)
	n := 0
	for speaker := range clapSurroundSpeakers {
		if b.SpeakerArrangement()&(1<<speaker) != 0 {
			if n < len(out) {
				out[n] = uint8(speaker)
			}
			n++
		}
	}
	return C.uint32_t(n)
}

//export GoClapNotePortsCount
func GoClapNotePortsCount(id C.uintptr_t, isInput C.bool) C.uint32_t {
	return C.uint32_t(getClap(id).c.buses.GetBusCount(bus.MediaTypeEvent, clapDirection(isInput)))
}

//export GoClapNotePortsGet
func GoClapNotePortsGet(id C.uintptr_t, index C.uint32_t, isInput C.bool, info *C.clap_note_port_info_t) C.bool {
	b := getClap(id).c.buses.GetBusInfo(bus.MediaTypeEvent, clapDirection(isInput), int32(index))
	if b == nil {
		return false
	}
	*info = C.clap_note_port_info_t{}
	info.id = C.clap_id(index)
	info.supported_dialects = C.CLAP_NOTE_DIALECT_CLAP | C.CLAP_NOTE_DIALECT_MIDI
	if isInput {
		info.supported_dialects |= C.CLAP_NOTE_DIALECT_MIDI2
	}
	info.preferred_dialect = C.CLAP_NOTE_DIALECT_CLAP
	copyCString(&info.name[0], len(info.name), b.Name)
	return true
}

//export GoClapLatency
func GoClapLatency(id C.uintptr_t) C.uint32_t {
	return C.uint32_t(getClap(id).c.GetLatencySamples())
}

//export GoClapTail
func GoClapTail(id C.uintptr_t) C.uint32_t {
	// TailInfinite (-1) is CLAP's UINT32_MAX "infinite tail" too.
	return C.uint32_t(getClap(id).c.GetTailSamples())
}

// processMode is the render mode the host set (clap_bridge.c keeps it).
func (inst *clapInstance) processMode() process.ProcessMode {
	if C.vst3go_clap_offline(inst.plugin) {
		return process.ProcessModeOffline
	}
	return process.ProcessModeRealtime
}

//export GoClapTrackInfoChanged
func GoClapTrackInfoChanged(id C.uintptr_t) {
	getClap(id).updateTrackInfo()
}

// updateTrackInfo passes the host track name and color to a
// ChannelInfoProcessor.
func (inst *clapInstance) updateTrackInfo() {
	listener, ok := inst.c.processor.(ChannelInfoProcessor)
	if !ok {
		return
	}
	var track C.clap_track_info_t
	if !C.vst3go_clap_track_info(inst.plugin, &track) {
		return
	}
	var info ChannelInfo
	if track.flags&C.CLAP_TRACK_INFO_HAS_TRACK_NAME != 0 {
		info.Name = C.GoString(&track.name[0])
	}
	if track.flags&C.CLAP_TRACK_INFO_HAS_TRACK_COLOR != 0 {
		color := track.color
		info.Color = uint32(color.alpha)<<24 | uint32(color.red)<<16 | uint32(color.green)<<8 | uint32(color.blue)
		info.HasColor = true
	}
	listener.SetChannelInfo(info)
}

// clapNoteName is one pitch name of the selected program.
type clapNoteName struct {
	key  int16
	name string
}

func (inst *clapInstance) noteNames() []clapNoteName {
	c := inst.c
	if c.programs == nil {
		return nil
	}
	index := c.programs.uiProgram.Load()
	if index < 0 || int(index) >= len(c.programs.list.Programs) {
		return nil
	}
	var names []clapNoteName
	for key, name := range c.programs.list.Programs[index].PitchNames {
		names = append(names, clapNoteName{key: key, name: name})
	}
	slices.SortFunc(names, func(a, b clapNoteName) int { return int(a.key) - int(b.key) })
	return names
}

//export GoClapNoteNameCount
func GoClapNoteNameCount(id C.uintptr_t) C.uint32_t {
	return C.uint32_t(len(getClap(id).noteNames()))
}

//export GoClapNoteNameGet
func GoClapNoteNameGet(id C.uintptr_t, index C.uint32_t, name *C.clap_note_name_t) C.bool {
	names := getClap(id).noteNames()
	if int(index) >= len(names) {
		return false
	}
	*name = C.clap_note_name_t{port: -1, key: C.int16_t(names[index].key), channel: -1}
	copyCString(&name.name[0], len(name.name), names[index].name)
	return true
}
