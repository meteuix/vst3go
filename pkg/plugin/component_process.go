package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
// #include <stdlib.h>
import "C"

import (
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/midi"
	"github.com/th-release/vst3go/pkg/vst3"
)

const (
	eventTypeNoteOn          = C.Steinberg_Vst_Event_EventTypes_kNoteOnEvent
	eventTypeNoteOff         = C.Steinberg_Vst_Event_EventTypes_kNoteOffEvent
	eventTypeData            = C.Steinberg_Vst_Event_EventTypes_kDataEvent
	eventTypePolyPressure    = C.Steinberg_Vst_Event_EventTypes_kPolyPressureEvent
	eventTypeNoteExpression  = C.Steinberg_Vst_Event_EventTypes_kNoteExpressionValueEvent
	eventTypeNoteExprInt     = C.Steinberg_Vst_Event_EventTypes_kNoteExpressionIntValueEvent
	eventTypeNoteExprText    = C.Steinberg_Vst_Event_EventTypes_kNoteExpressionTextEvent
	eventTypeChord           = C.Steinberg_Vst_Event_EventTypes_kChordEvent
	eventTypeScale           = C.Steinberg_Vst_Event_EventTypes_kScaleEvent
	eventTypeLegacyMIDICCOut = C.Steinberg_Vst_Event_EventTypes_kLegacyMIDICCOutEvent

	ctrlAfterTouch    = C.Steinberg_Vst_ControllerNumbers_kAfterTouch
	ctrlPitchBend     = C.Steinberg_Vst_ControllerNumbers_kPitchBend
	ctrlProgramChange = C.Steinberg_Vst_ControllerNumbers_kCtrlProgramChange

	dataTypeMidiSysEx = C.Steinberg_Vst_DataEvent_DataTypes_kMidiSysEx

	sampleSize32 = C.Steinberg_Vst_SymbolicSampleSizes_kSample32
	sampleSize64 = C.Steinberg_Vst_SymbolicSampleSizes_kSample64
)

// processScratch is C memory reused for out-parameters on the audio thread.
type processScratch struct {
	c *C.VST3GoScratch
}

func newProcessScratch() processScratch {
	return processScratch{c: (*C.VST3GoScratch)(C.calloc(1, C.size_t(unsafe.Sizeof(C.VST3GoScratch{}))))}
}

func (s processScratch) free() {
	if s.c != nil {
		C.free(unsafe.Pointer(s.c))
	}
}

// Process runs one host process call. It is the only method called on the
// audio thread.
func (c *componentImpl) Process(data *C.struct_Steinberg_Vst_ProcessData, scratch processScratch) bool {
	if data.numSamples < 0 {
		return false
	}
	double := data.symbolicSampleSize == sampleSize64
	if (double && !c.double) || (!double && data.symbolicSampleSize != sampleSize32) {
		return false
	}

	ctx := c.processCtx
	numSamples := int(data.numSamples)
	if double {
		ctx.BeginBlock64(numSamples)
	} else {
		ctx.BeginBlock(numSamples)
	}
	ctx.SampleRate = c.sampleRate
	ctx.ProcessMode = process.ProcessMode(data.processMode)

	updateTransport(ctx.Transport, data.processContext)
	if numSamples > 0 {
		if double {
			mapAudioBuses64(ctx, data, numSamples)
		} else {
			mapAudioBuses(ctx, data, numSamples)
		}
	}
	c.collectInputEvents(ctx, unsafe.Pointer(data.inputEvents), scratch)
	c.collectParameterChanges(ctx, unsafe.Pointer(data.inputParameterChanges), scratch)

	ctx.Dispatch(c.processor.ProcessAudio)

	writeOutputSilenceFlags(ctx, data)
	c.writeOutputParameters(ctx, unsafe.Pointer(data.outputParameterChanges))
	writeOutputEvents(ctx, unsafe.Pointer(data.outputEvents), scratch)
	return true
}

func updateTransport(transport *process.TransportInfo, ctx *C.struct_Steinberg_Vst_ProcessContext) {
	*transport = process.TransportInfo{}
	if ctx == nil {
		return
	}

	state := ctx.state
	transport.IsPlaying = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kPlaying != 0
	transport.IsRecording = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kRecording != 0
	transport.IsCycling = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kCycleActive != 0

	transport.HasTempo = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kTempoValid != 0
	if transport.HasTempo {
		transport.Tempo = float64(ctx.tempo)
	}
	transport.HasTimeSignature = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kTimeSigValid != 0
	if transport.HasTimeSignature {
		transport.TimeSigNumerator = int32(ctx.timeSigNumerator)
		transport.TimeSigDenominator = int32(ctx.timeSigDenominator)
	}
	transport.HasMusicalTime = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kProjectTimeMusicValid != 0
	if transport.HasMusicalTime {
		transport.ProjectTimeMusic = float64(ctx.projectTimeMusic)
	}
	transport.HasBarPosition = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kBarPositionValid != 0
	if transport.HasBarPosition {
		transport.BarPositionMusic = float64(ctx.barPositionMusic)
	}
	transport.HasCycle = state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kCycleValid != 0
	if transport.HasCycle {
		transport.CycleStartMusic = float64(ctx.cycleStartMusic)
		transport.CycleEndMusic = float64(ctx.cycleEndMusic)
	}
	transport.ProjectTimeSamples = int64(ctx.projectTimeSamples)
	if state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kContTimeValid != 0 {
		transport.ContinuousTimeSamples = int64(ctx.continousTimeSamples)
	}
	if state&C.Steinberg_Vst_ProcessContext_StatesAndFlags_kClockValid != 0 {
		transport.SamplesToNextClock = int32(ctx.samplesToNextClock)
	}
}

func audioBuses(buses *C.struct_Steinberg_Vst_AudioBusBuffers, count C.Steinberg_int32) []C.struct_Steinberg_Vst_AudioBusBuffers {
	if buses == nil || count <= 0 {
		return nil
	}
	return unsafe.Slice(buses, int(count))
}

func mapAudioBuses(ctx *process.Context, data *C.struct_Steinberg_Vst_ProcessData, numSamples int) {
	inputs := audioBuses(data.inputs, data.numInputs)
	for b := range inputs {
		ctx.SetInputSilenceFlags(b, uint64(inputs[b].silenceFlags))
		forEachChannel(&inputs[b], numSamples, func(samples []float32) { ctx.AddInputChannel(b, samples) })
	}
	outputs := audioBuses(data.outputs, data.numOutputs)
	for b := range outputs {
		forEachChannel(&outputs[b], numSamples, func(samples []float32) { ctx.AddOutputChannel(b, samples) })
	}
}

func mapAudioBuses64(ctx *process.Context, data *C.struct_Steinberg_Vst_ProcessData, numSamples int) {
	inputs := audioBuses(data.inputs, data.numInputs)
	for b := range inputs {
		ctx.SetInputSilenceFlags(b, uint64(inputs[b].silenceFlags))
		forEachChannel64(&inputs[b], numSamples, func(samples []float64) { ctx.AddInputChannel64(b, samples) })
	}
	outputs := audioBuses(data.outputs, data.numOutputs)
	for b := range outputs {
		forEachChannel64(&outputs[b], numSamples, func(samples []float64) { ctx.AddOutputChannel64(b, samples) })
	}
}

// forEachChannel visits the 32-bit channel buffers of a bus. A bus whose
// host buffers are missing or incomplete contributes no channels.
func forEachChannel(audioBus *C.struct_Steinberg_Vst_AudioBusBuffers, numSamples int, visit func([]float32)) {
	if audioBus.numChannels <= 0 {
		return
	}
	visitChannels(unsafe.Pointer(C.vst3go_bus_channels32(audioBus)), int(audioBus.numChannels), func(ptr unsafe.Pointer) {
		visit(unsafe.Slice((*float32)(ptr), numSamples))
	})
}

// forEachChannel64 is forEachChannel for 64-bit buffers.
func forEachChannel64(audioBus *C.struct_Steinberg_Vst_AudioBusBuffers, numSamples int, visit func([]float64)) {
	if audioBus.numChannels <= 0 {
		return
	}
	visitChannels(unsafe.Pointer(C.vst3go_bus_channels64(audioBus)), int(audioBus.numChannels), func(ptr unsafe.Pointer) {
		visit(unsafe.Slice((*float64)(ptr), numSamples))
	})
}

func visitChannels(channelPtrs unsafe.Pointer, numChannels int, visit func(unsafe.Pointer)) {
	if channelPtrs == nil {
		return
	}
	ptrs := unsafe.Slice((*unsafe.Pointer)(channelPtrs), numChannels)
	for _, ptr := range ptrs {
		if ptr == nil {
			return
		}
	}
	for _, ptr := range ptrs {
		visit(ptr)
	}
}

func writeOutputSilenceFlags(ctx *process.Context, data *C.struct_Steinberg_Vst_ProcessData) {
	outputs := audioBuses(data.outputs, data.numOutputs)
	for b := range outputs {
		outputs[b].silenceFlags = C.Steinberg_uint64(ctx.OutputSilenceFlags(b))
	}
}

func (c *componentImpl) collectInputEvents(ctx *process.Context, list unsafe.Pointer, scratch processScratch) {
	if list == nil {
		return
	}
	count := C.vst3go_events_count(list)
	ev := &scratch.c.event
	for i := C.int32_t(0); i < count; i++ {
		if C.vst3go_events_get(list, i, ev) != C.Steinberg_tresult(vst3.ResultOK) {
			continue
		}
		if event, ok := convertInputEvent(ctx, ev); ok {
			ctx.AddInputEvent(event)
		}
	}
}

func convertInputEvent(ctx *process.Context, ev *C.VST3GoEvent) (midi.Event, bool) {
	event := midi.Event{
		Channel: uint8(ev.channel & 0x0F),
		Offset:  int32(ev.sampleOffset),
		NoteID:  int32(ev.noteId),
	}
	switch ev._type {
	case eventTypeNoteOn:
		event.Type = midi.EventTypeNoteOn
		event.Note = uint8(ev.pitch & 0x7F)
		event.Velocity = float32(ev.value)
		event.Tuning = float32(ev.tuning)
		event.Length = int32(ev.length)
	case eventTypeNoteOff:
		event.Type = midi.EventTypeNoteOff
		event.Note = uint8(ev.pitch & 0x7F)
		event.Velocity = float32(ev.value)
		event.Tuning = float32(ev.tuning)
	case eventTypePolyPressure:
		event.Type = midi.EventTypePolyPressure
		event.Note = uint8(ev.pitch & 0x7F)
		event.Value = float64(ev.value)
	case eventTypeNoteExpression:
		event.Type = midi.EventTypeNoteExpression
		event.ExpressionType = uint32(ev.expressionType)
		event.Value = float64(ev.expressionValue)
	case eventTypeNoteExprInt:
		event.Type = midi.EventTypeNoteExpressionInt
		event.ExpressionType = uint32(ev.expressionType)
		event.IntValue = uint64(ev.expressionIntValue)
	case eventTypeNoteExprText:
		event.Type = midi.EventTypeNoteExpressionText
		event.ExpressionType = uint32(ev.expressionType)
		event.Data = eventText(ctx, ev)
	case eventTypeChord:
		event.Type = midi.EventTypeChord
		event.Channel = 0
		event.Root = int16(ev.root)
		event.Bass = int16(ev.bassNote)
		event.Mask = uint16(ev.mask)
		event.Data = eventText(ctx, ev)
	case eventTypeScale:
		event.Type = midi.EventTypeScale
		event.Channel = 0
		event.Root = int16(ev.root)
		event.Bass = -1
		event.Mask = uint16(ev.mask)
		event.Data = eventText(ctx, ev)
	case eventTypeData:
		if ev.dataType != dataTypeMidiSysEx || ev.data == nil || ev.dataSize == 0 {
			return event, false
		}
		event.Type = midi.EventTypeSysEx
		event.Channel = 0
		event.NoteID = midi.NoteIDNone
		event.Data = ctx.EventDataBuffer(int(ev.dataSize))
		copy(event.Data, unsafe.Slice((*byte)(unsafe.Pointer(ev.data)), int(ev.dataSize)))
	default:
		return event, false
	}
	return event, true
}

// eventText converts the UTF-16 text of an event to UTF-8 block memory.
func eventText(ctx *process.Context, ev *C.VST3GoEvent) []byte {
	if ev.text == nil || ev.textLen == 0 {
		return nil
	}
	text := unsafe.Slice((*uint16)(unsafe.Pointer(ev.text)), int(ev.textLen))
	// Hosts may count the terminating zero.
	for len(text) > 0 && text[len(text)-1] == 0 {
		text = text[:len(text)-1]
	}
	return ctx.AppendEventText(text)
}

func (c *componentImpl) collectParameterChanges(ctx *process.Context, changes unsafe.Pointer, scratch processScratch) {
	if changes == nil {
		return
	}
	count := C.vst3go_paramchanges_count(changes)
	for i := C.int32_t(0); i < count; i++ {
		queue := C.vst3go_paramchanges_queue(changes, i)
		if queue == nil {
			continue
		}
		id := uint32(C.vst3go_paramqueue_id(queue))
		isController := c.midiCC.owns(id)
		points := C.vst3go_paramqueue_count(queue)
		for j := C.int32_t(0); j < points; j++ {
			if C.vst3go_paramqueue_point(queue, j, &scratch.c.sampleOffset, &scratch.c.value) != C.Steinberg_tresult(vst3.ResultOK) {
				continue
			}
			offset := int(scratch.c.sampleOffset)
			value := float64(scratch.c.value)
			if isController {
				c.midiCC.set(id, value)
				ctx.AddInputEvent(c.midiCC.event(id, value, int32(offset)))
				continue
			}
			ctx.AddParameterChange(id, value, offset)
			if id == ProgramParamID && c.programs != nil {
				c.applyProgram(ctx, c.programs.index(value), offset)
			}
		}
	}
}

func (c *componentImpl) writeOutputParameters(ctx *process.Context, changes unsafe.Pointer) {
	for _, change := range ctx.OutputParameterChanges() {
		if p, ok := c.params.GetOK(change.ParamID); ok {
			p.SetNormalized(change.Value)
		}
		if changes != nil {
			C.vst3go_paramchanges_add_point(changes, C.uint32_t(change.ParamID), C.int32_t(change.SampleOffset), C.double(change.Value))
		}
	}
}

// applyProgram queues the parameter values of a newly selected program at
// the offset of the selection change.
func (c *componentImpl) applyProgram(ctx *process.Context, index int32, offset int) {
	if c.programs.audioProgram.Swap(index) == index {
		return
	}
	for _, v := range c.programs.valuesOf(index) {
		ctx.AddParameterChange(v.id, v.normalized, offset)
	}
}

func writeOutputEvents(ctx *process.Context, list unsafe.Pointer, scratch processScratch) {
	if list == nil {
		return
	}
	ev := &scratch.c.event
	// Payloads stay in scratch memory until the next block; the host reads
	// the output list after process returns.
	scratch.c.dataUsed = 0
	scratch.c.textUsed = 0
	events := ctx.GetOutputEvents()
	for i := range events {
		if fillOutputEvent(ev, &events[i], scratch) {
			C.vst3go_events_add(list, ev)
		}
	}
}

func fillOutputEvent(ev *C.VST3GoEvent, event *midi.Event, scratch processScratch) bool {
	*ev = C.VST3GoEvent{}
	ev.sampleOffset = C.int32_t(event.Offset)
	ev.channel = C.int32_t(event.Channel & 0x0F)
	ev.noteId = C.int32_t(event.NoteID)

	switch event.Type {
	case midi.EventTypeNoteOn:
		ev._type = eventTypeNoteOn
		ev.pitch = C.int32_t(event.Note & 0x7F)
		ev.value = C.float(event.Velocity)
		ev.tuning = C.float(event.Tuning)
		ev.length = C.int32_t(event.Length)
	case midi.EventTypeNoteOff:
		ev._type = eventTypeNoteOff
		ev.pitch = C.int32_t(event.Note & 0x7F)
		ev.value = C.float(event.Velocity)
		ev.tuning = C.float(event.Tuning)
	case midi.EventTypePolyPressure:
		ev._type = eventTypePolyPressure
		ev.pitch = C.int32_t(event.Note & 0x7F)
		ev.value = C.float(clampUnit(event.Value))
	case midi.EventTypeNoteExpression:
		ev._type = eventTypeNoteExpression
		ev.expressionType = C.uint32_t(event.ExpressionType)
		ev.expressionValue = C.double(clampUnit(event.Value))
	case midi.EventTypeNoteExpressionInt:
		ev._type = eventTypeNoteExprInt
		ev.expressionType = C.uint32_t(event.ExpressionType)
		ev.expressionIntValue = C.uint64_t(event.IntValue)
	case midi.EventTypeNoteExpressionText:
		ev._type = eventTypeNoteExprText
		ev.expressionType = C.uint32_t(event.ExpressionType)
		if !scratch.putText(ev, event.Data) {
			return false
		}
	case midi.EventTypeControlChange:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = C.int32_t(event.Controller & 0x7F)
		ev.controlValue = C.int32_t(event.Value7())
	case midi.EventTypeChannelPressure:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlAfterTouch
		ev.controlValue = C.int32_t(event.Value7())
	case midi.EventTypePitchBend:
		value := int(event.PitchBend14()) + 8192
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlPitchBend
		ev.controlValue = C.int32_t(value & 0x7F)
		ev.controlValue2 = C.int32_t(value >> 7)
	case midi.EventTypeProgramChange:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlProgramChange
		ev.controlValue = C.int32_t(event.Program & 0x7F)
	case midi.EventTypeSysEx:
		ev._type = eventTypeData
		ev.dataType = dataTypeMidiSysEx
		ev.noteId = 0
		if !scratch.putData(ev, event.Data) {
			return false
		}
	default:
		// Chord and scale events are host-to-plugin only.
		return false
	}
	return true
}

// putData copies an event payload into scratch memory; it fails when the
// block's payload capacity is exhausted.
func (s processScratch) putData(ev *C.VST3GoEvent, data []byte) bool {
	used := int(s.c.dataUsed)
	if len(data) == 0 || used+len(data) > len(s.c.data) {
		return false
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(&s.c.data[used])), len(data))
	copy(dst, data)
	ev.data = (*C.uint8_t)(unsafe.Pointer(&s.c.data[used]))
	ev.dataSize = C.uint32_t(len(data))
	s.c.dataUsed = C.uint32_t(used + len(data))
	return true
}

// putText stores UTF-8 text as zero-terminated UTF-16 in scratch memory.
func (s processScratch) putText(ev *C.VST3GoEvent, text []byte) bool {
	used := int(s.c.textUsed)
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(&s.c.text[0])), len(s.c.text))[used:]
	n := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		i += size
		if r >= 0x10000 {
			if n+2 >= len(dst) {
				return false
			}
			r1, r2 := utf16.EncodeRune(r)
			dst[n], dst[n+1] = uint16(r1), uint16(r2)
			n += 2
			continue
		}
		if n+1 >= len(dst) {
			return false
		}
		dst[n] = uint16(r)
		n++
	}
	if n >= len(dst) {
		return false
	}
	dst[n] = 0
	ev.text = (*C.Steinberg_char16)(unsafe.Pointer(&dst[0]))
	ev.textLen = C.uint32_t(n)
	s.c.textUsed = C.uint32_t(used + n + 1)
	return true
}
