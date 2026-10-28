package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
// #include <stdlib.h>
import "C"

import (
	"math"
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
	eventTypeLegacyMIDICCOut = C.Steinberg_Vst_Event_EventTypes_kLegacyMIDICCOutEvent

	ctrlAfterTouch    = C.Steinberg_Vst_ControllerNumbers_kAfterTouch
	ctrlPitchBend     = C.Steinberg_Vst_ControllerNumbers_kPitchBend
	ctrlProgramChange = C.Steinberg_Vst_ControllerNumbers_kCtrlProgramChange

	dataTypeMidiSysEx = C.Steinberg_Vst_DataEvent_DataTypes_kMidiSysEx

	sampleSize32 = C.Steinberg_Vst_SymbolicSampleSizes_kSample32
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
	if data.symbolicSampleSize != sampleSize32 || data.numSamples < 0 {
		return false
	}

	ctx := c.processCtx
	numSamples := int(data.numSamples)
	ctx.BeginBlock(numSamples)
	ctx.SampleRate = c.sampleRate
	ctx.ProcessMode = process.ProcessMode(data.processMode)

	updateTransport(ctx.Transport, data.processContext)
	if numSamples > 0 {
		mapAudioBuses(ctx, data, numSamples)
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

// forEachChannel visits the 32-bit channel buffers of a bus. A bus whose
// host buffers are missing or incomplete contributes no channels.
func forEachChannel(audioBus *C.struct_Steinberg_Vst_AudioBusBuffers, numSamples int, visit func([]float32)) {
	if audioBus.numChannels <= 0 {
		return
	}
	channelPtrs := C.vst3go_bus_channels32(audioBus)
	if channelPtrs == nil {
		return
	}
	ptrs := unsafe.Slice(channelPtrs, int(audioBus.numChannels))
	for _, ptr := range ptrs {
		if ptr == nil {
			return
		}
	}
	for _, ptr := range ptrs {
		visit(unsafe.Slice((*float32)(unsafe.Pointer(ptr)), numSamples))
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
		if event := convertInputEvent(ctx, ev); event != nil {
			ctx.AddInputEvent(event)
		}
	}
}

func convertInputEvent(ctx *process.Context, ev *C.VST3GoEvent) midi.Event {
	base := midi.BaseEvent{EventChannel: uint8(ev.channel), Offset: int32(ev.sampleOffset)}
	switch ev._type {
	case eventTypeNoteOn:
		return midi.NoteOnEvent{
			BaseEvent:    base,
			NoteNumber:   uint8(ev.pitch),
			Velocity:     unitTo7Bit(float64(ev.value)),
			VelocityNorm: float32(ev.value),
			Tuning:       float32(ev.tuning),
			Length:       int32(ev.length),
			NoteID:       int32(ev.noteId),
		}
	case eventTypeNoteOff:
		return midi.NoteOffEvent{
			BaseEvent:    base,
			NoteNumber:   uint8(ev.pitch),
			Velocity:     unitTo7Bit(float64(ev.value)),
			VelocityNorm: float32(ev.value),
			Tuning:       float32(ev.tuning),
			NoteID:       int32(ev.noteId),
		}
	case eventTypePolyPressure:
		return midi.PolyPressureEvent{
			BaseEvent:    base,
			NoteNumber:   uint8(ev.pitch),
			Pressure:     unitTo7Bit(float64(ev.value)),
			PressureNorm: float32(ev.value),
			NoteID:       int32(ev.noteId),
		}
	case eventTypeNoteExpression:
		return midi.NoteExpressionEvent{
			BaseEvent: midi.BaseEvent{Offset: int32(ev.sampleOffset)},
			TypeID:    uint32(ev.expressionType),
			NoteID:    int32(ev.noteId),
			Value:     float64(ev.expressionValue),
		}
	case eventTypeData:
		if ev.dataType != dataTypeMidiSysEx || ev.data == nil || ev.dataSize == 0 {
			return nil
		}
		buf := ctx.EventDataBuffer(int(ev.dataSize))
		copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(ev.data)), int(ev.dataSize)))
		return midi.SysExEvent{BaseEvent: midi.BaseEvent{Offset: int32(ev.sampleOffset)}, Data: buf}
	}
	return nil
}

func unitTo7Bit(v float64) uint8 {
	return uint8(math.Round(clampUnit(v) * 127))
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

func writeOutputEvents(ctx *process.Context, list unsafe.Pointer, scratch processScratch) {
	if list == nil {
		return
	}
	ev := &scratch.c.event
	for _, event := range ctx.GetOutputEvents() {
		if fillOutputEvent(ev, event) {
			C.vst3go_events_add(list, ev)
		}
	}
}

func fillOutputEvent(ev *C.VST3GoEvent, event midi.Event) bool {
	*ev = C.VST3GoEvent{}
	ev.sampleOffset = C.int32_t(event.SampleOffset())
	ev.channel = C.int32_t(event.Channel())
	ev.noteId = -1

	switch e := event.(type) {
	case midi.NoteOnEvent:
		ev._type = eventTypeNoteOn
		ev.pitch = C.int32_t(e.NoteNumber)
		ev.value = C.float(velocityOut(e.VelocityNorm, e.Velocity))
		ev.tuning = C.float(e.Tuning)
		ev.length = C.int32_t(e.Length)
		ev.noteId = C.int32_t(e.NoteID)
	case midi.NoteOffEvent:
		ev._type = eventTypeNoteOff
		ev.pitch = C.int32_t(e.NoteNumber)
		ev.value = C.float(velocityOut(e.VelocityNorm, e.Velocity))
		ev.tuning = C.float(e.Tuning)
		ev.noteId = C.int32_t(e.NoteID)
	case midi.PolyPressureEvent:
		ev._type = eventTypePolyPressure
		ev.pitch = C.int32_t(e.NoteNumber)
		ev.value = C.float(velocityOut(e.PressureNorm, e.Pressure))
		ev.noteId = C.int32_t(e.NoteID)
	case midi.NoteExpressionEvent:
		ev._type = eventTypeNoteExpression
		ev.expressionType = C.uint32_t(e.TypeID)
		ev.expressionValue = C.double(e.Value)
		ev.noteId = C.int32_t(e.NoteID)
	case midi.ControlChangeEvent:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = C.int32_t(e.Controller)
		ev.controlValue = C.int32_t(e.Value & 0x7F)
	case midi.ChannelPressureEvent:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlAfterTouch
		ev.controlValue = C.int32_t(e.Pressure & 0x7F)
	case midi.PitchBendEvent:
		value := int(e.Value) + 8192
		if value < 0 {
			value = 0
		} else if value > 16383 {
			value = 16383
		}
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlPitchBend
		ev.controlValue = C.int32_t(value & 0x7F)
		ev.controlValue2 = C.int32_t(value >> 7)
	case midi.ProgramChangeEvent:
		ev._type = eventTypeLegacyMIDICCOut
		ev.controlNumber = ctrlProgramChange
		ev.controlValue = C.int32_t(e.Program & 0x7F)
	default:
		return false
	}
	return true
}

func velocityOut(norm float32, value uint8) float32 {
	if norm > 0 {
		return norm
	}
	return float32(value) / 127
}
