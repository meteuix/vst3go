package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/bus"
)

// Optional edit controller interfaces. Each is exposed by the C object only
// when the processor implements the matching Go interface (capabilities).

const (
	noParentUnitID  = -1         // kNoParentUnitId
	noProgramListID = -1         // kNoProgramListId
	noParamID       = 0xFFFFFFFF // kNoParamId
	invalidTypeID   = 0xFFFFFFFF // kInvalidTypeID (note expressions)
)

// IMidiMapping2

type midi1Assignment struct {
	busIndex   int32
	channel    int16
	controller int16
	paramID    uint32
}

// midi1Assignments enumerates every MIDI 1.0 controller assignment of the
// event input buses.
func (c *componentImpl) midi1Assignments() []midi1Assignment {
	var out []midi1Assignment
	buses := c.buses.GetBusCount(bus.MediaTypeEvent, bus.DirectionInput)
	for b := int32(0); b < buses; b++ {
		for channel := int16(0); channel < 16; channel++ {
			// IMidiMapping covers CC 0-127, aftertouch and pitch bend
			// (kCountCtrlNumber); program changes reach the program
			// selector through the host (kIsProgramChange).
			for controller := int16(0); controller <= MIDIControllerPitchBend; controller++ {
				if id, ok := c.MIDIControllerAssignment(b, channel, controller); ok {
					out = append(out, midi1Assignment{busIndex: b, channel: channel, controller: controller, paramID: id})
				}
			}
		}
	}
	return out
}

func (c *componentImpl) midi2Assignments() []MIDI2Assignment {
	if mapper, ok := c.processor.(MIDI2MappingProcessor); ok {
		return mapper.MIDI2ControllerParameters()
	}
	return nil
}

func isInputDirection(direction C.int32_t) bool {
	return direction == C.Steinberg_Vst_BusDirections_kInput
}

//export GoMidiMapping1Count
func GoMidiMapping1Count(componentID C.uintptr_t, direction C.int32_t) (count C.uint32_t) {
	defer func() { _ = recover() }()
	wrapper := getComponent(componentID)
	if wrapper == nil || !isInputDirection(direction) {
		return 0
	}
	return C.uint32_t(len(wrapper.component.midi1Assignments()))
}

//export GoMidiMapping1Fill
func GoMidiMapping1Fill(componentID C.uintptr_t, direction C.int32_t, list *C.struct_Steinberg_Vst_Midi1ControllerParamIDAssignmentList) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || !isInputDirection(direction) {
		return resultFalse
	}
	assignments := wrapper.component.midi1Assignments()
	if list.count < C.Steinberg_uint32(len(assignments)) || (len(assignments) > 0 && list._map == nil) {
		return resultInvalidArgument
	}
	if len(assignments) == 0 {
		return resultOK
	}
	dst := unsafe.Slice(list._map, len(assignments))
	for i, a := range assignments {
		dst[i].pId = C.Steinberg_Vst_ParamID(a.paramID)
		dst[i].busIndex = C.Steinberg_Vst_BusIndex(a.busIndex)
		dst[i].channel = C.Steinberg_Vst_MidiChannel(a.channel)
		dst[i].controller = C.Steinberg_Vst_CtrlNumber(a.controller)
	}
	return resultOK
}

//export GoMidiMapping2Count
func GoMidiMapping2Count(componentID C.uintptr_t, direction C.int32_t) (count C.uint32_t) {
	defer func() { _ = recover() }()
	wrapper := getComponent(componentID)
	if wrapper == nil || !isInputDirection(direction) {
		return 0
	}
	return C.uint32_t(len(wrapper.component.midi2Assignments()))
}

//export GoMidiMapping2Fill
func GoMidiMapping2Fill(componentID C.uintptr_t, direction C.int32_t, list *C.struct_Steinberg_Vst_Midi2ControllerParamIDAssignmentList) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || !isInputDirection(direction) {
		return resultFalse
	}
	assignments := wrapper.component.midi2Assignments()
	if list.count < C.Steinberg_uint32(len(assignments)) || (len(assignments) > 0 && list._map == nil) {
		return resultInvalidArgument
	}
	if len(assignments) == 0 {
		return resultOK
	}
	dst := unsafe.Slice(list._map, len(assignments))
	for i, a := range assignments {
		dst[i].pId = C.Steinberg_Vst_ParamID(a.ParamID)
		dst[i].busIndex = C.Steinberg_Vst_BusIndex(a.BusIndex)
		dst[i].channel = C.Steinberg_Vst_MidiChannel(a.Channel)
		dst[i].controller = midi2ControllerToC(a.Controller)
	}
	return resultOK
}

func midi2ControllerToC(controller MIDI2Controller) C.struct_Steinberg_Vst_Midi2Controller {
	byte1, byte2 := midi2ControllerBytes(controller)
	return C.struct_Steinberg_Vst_Midi2Controller{byte1: C.Steinberg_uint8(byte1), byte2: C.Steinberg_uint8(byte2)}
}

// midi2ControllerBytes packs a controller like the SDK bit fields:
// byte1 = bank (7 bits) | registered << 7, byte2 = index (7 bits).
func midi2ControllerBytes(controller MIDI2Controller) (byte1, byte2 uint8) {
	byte1 = controller.Bank & 0x7F
	if controller.Registered {
		byte1 |= 0x80
	}
	return byte1, controller.Index & 0x7F
}

func midi2ControllerFromBytes(byte1, byte2 uint8) MIDI2Controller {
	return MIDI2Controller{Bank: byte1 & 0x7F, Registered: byte1&0x80 != 0, Index: byte2 & 0x7F}
}

// IMidiLearn, IMidiLearn2

func learnResult(learned bool) C.Steinberg_tresult {
	if learned {
		return resultOK
	}
	return resultFalse
}

//export GoMidiLearn1
func GoMidiLearn1(componentID C.uintptr_t, busIndex C.int32_t, channel, controller C.int16_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	learner, ok := wrapper.component.processor.(MIDILearnProcessor)
	if !ok {
		return resultFalse
	}
	return learnResult(learner.LearnMIDIController(int32(busIndex), int16(channel), int16(controller)))
}

//export GoMidiLearn2
func GoMidiLearn2(componentID C.uintptr_t, busIndex C.int32_t, channel, byte1, byte2 C.uint8_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	learner, ok := wrapper.component.processor.(MIDI2LearnProcessor)
	if !ok {
		return resultFalse
	}
	return learnResult(learner.LearnMIDI2Controller(int32(busIndex), uint8(channel), midi2ControllerFromBytes(uint8(byte1), uint8(byte2))))
}

// IUnitInfo

// units returns the root unit followed by the processor's units.
func (c *componentImpl) units() []Unit {
	root := Unit{ID: rootUnitID, ParentID: noParentUnitID, Name: "Root"}
	var units []Unit
	if processor, ok := c.processor.(UnitProcessor); ok {
		for _, unit := range processor.Units() {
			if unit.ID == rootUnitID {
				root.Name = unit.Name
				continue
			}
			units = append(units, unit)
		}
	}
	return append([]Unit{root}, units...)
}

//export GoUnitCount
func GoUnitCount(componentID C.uintptr_t) (count C.int32_t) {
	defer func() { _ = recover() }()
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.int32_t(len(wrapper.component.units()))
}

//export GoUnitInfo
func GoUnitInfo(componentID C.uintptr_t, index C.int32_t, info *C.struct_Steinberg_Vst_UnitInfo) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	units := wrapper.component.units()
	if index < 0 || int(index) >= len(units) {
		return resultInvalidArgument
	}
	unit := units[index]
	info.id = C.Steinberg_Vst_UnitID(unit.ID)
	info.parentUnitId = C.Steinberg_Vst_UnitID(unit.ParentID)
	copyStringToTChar(unit.Name, &info.name[0], len(info.name))
	info.programListId = noProgramListID
	if unit.ID == rootUnitID && wrapper.component.programs != nil {
		info.programListId = C.Steinberg_Vst_ProgramListID(programListID)
	}
	return resultOK
}

//export GoProgramListCount
func GoProgramListCount(componentID C.uintptr_t) C.int32_t {
	wrapper := getComponent(componentID)
	if wrapper == nil || wrapper.component.programs == nil {
		return 0
	}
	return 1
}

//export GoProgramListInfo
func GoProgramListInfo(componentID C.uintptr_t, index C.int32_t, info *C.struct_Steinberg_Vst_ProgramListInfo) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil || wrapper.component.programs == nil || index != 0 {
		return resultInvalidArgument
	}
	programs := wrapper.component.programs
	info.id = C.Steinberg_Vst_ProgramListID(programListID)
	copyStringToTChar(programs.list.Name, &info.name[0], len(info.name))
	info.programCount = C.Steinberg_int32(programs.count())
	return resultOK
}

func (w *componentWrapper) program(listID, index C.int32_t) *Program {
	programs := w.programList(listID)
	if programs == nil || index < 0 || int(index) >= len(programs.list.Programs) {
		return nil
	}
	return &programs.list.Programs[index]
}

//export GoProgramName
func GoProgramName(componentID C.uintptr_t, listID, index C.int32_t, name *C.Steinberg_Vst_TChar) C.Steinberg_tresult {
	program := getComponent(componentID).program(listID, index)
	if program == nil {
		return resultInvalidArgument
	}
	copyStringToTChar(program.Name, name, string128)
	return resultOK
}

//export GoProgramHasPitchNames
func GoProgramHasPitchNames(componentID C.uintptr_t, listID, index C.int32_t) C.Steinberg_tresult {
	program := getComponent(componentID).program(listID, index)
	if program == nil || len(program.PitchNames) == 0 {
		return resultFalse
	}
	return resultOK
}

//export GoProgramPitchName
func GoProgramPitchName(componentID C.uintptr_t, listID, index C.int32_t, pitch C.int16_t, name *C.Steinberg_Vst_TChar) C.Steinberg_tresult {
	program := getComponent(componentID).program(listID, index)
	if program == nil {
		return resultInvalidArgument
	}
	pitchName, ok := program.PitchNames[int16(pitch)]
	if !ok {
		return resultFalse
	}
	copyStringToTChar(pitchName, name, string128)
	return resultOK
}

// INoteExpressionController

func (c *componentImpl) noteExpressions(busIndex C.int32_t, channel C.int16_t) []NoteExpressionInfo {
	if processor, ok := c.processor.(NoteExpressionProcessor); ok {
		return processor.NoteExpressions(int32(busIndex), int16(channel))
	}
	return nil
}

func (c *componentImpl) noteExpression(busIndex C.int32_t, channel C.int16_t, typeID C.uint32_t) *NoteExpressionInfo {
	infos := c.noteExpressions(busIndex, channel)
	for i := range infos {
		if infos[i].TypeID == uint32(typeID) {
			return &infos[i]
		}
	}
	return nil
}

//export GoNoteExpressionCount
func GoNoteExpressionCount(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t) (count C.int32_t) {
	defer func() { _ = recover() }()
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.int32_t(len(wrapper.component.noteExpressions(busIndex, channel)))
}

//export GoNoteExpressionInfo
func GoNoteExpressionInfo(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, index C.int32_t, out *C.struct_Steinberg_Vst_NoteExpressionTypeInfo) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	infos := wrapper.component.noteExpressions(busIndex, channel)
	if index < 0 || int(index) >= len(infos) {
		return resultInvalidArgument
	}
	info := infos[index]
	maximum := info.Max
	if maximum == 0 {
		maximum = 1
	}
	*out = C.struct_Steinberg_Vst_NoteExpressionTypeInfo{}
	out.typeId = C.Steinberg_Vst_NoteExpressionTypeID(info.TypeID)
	copyStringToTChar(info.Title, &out.title[0], len(out.title))
	copyStringToTChar(info.ShortTitle, &out.shortTitle[0], len(out.shortTitle))
	copyStringToTChar(info.Units, &out.units[0], len(out.units))
	out.unitId = C.Steinberg_int32(info.UnitID)
	out.valueDesc.defaultValue = C.Steinberg_Vst_NoteExpressionValue(info.Default)
	out.valueDesc.minimum = C.Steinberg_Vst_NoteExpressionValue(info.Min)
	out.valueDesc.maximum = C.Steinberg_Vst_NoteExpressionValue(maximum)
	out.valueDesc.stepCount = C.Steinberg_int32(info.StepCount)
	out.associatedParameterId = noParamID
	if info.Flags&NoteExpressionAssociated != 0 {
		out.associatedParameterId = C.Steinberg_Vst_ParamID(info.AssociatedParamID)
	}
	out.flags = C.Steinberg_int32(info.Flags)
	return resultOK
}

//export GoNoteExpressionString
func GoNoteExpressionString(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, typeID C.uint32_t, value C.double, out *C.Steinberg_Vst_TChar) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	info := wrapper.component.noteExpression(busIndex, channel, typeID)
	if info == nil {
		return resultInvalidArgument
	}
	normalized := clampUnit(float64(value))
	text := fmt.Sprintf("%.2f", normalized)
	if info.Format != nil {
		text = info.Format(normalized)
	}
	copyStringToTChar(text, out, string128)
	return resultOK
}

//export GoNoteExpressionValue
func GoNoteExpressionValue(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, typeID C.uint32_t, in *C.Steinberg_Vst_TChar, out *C.double) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	info := wrapper.component.noteExpression(busIndex, channel, typeID)
	if info == nil {
		return resultInvalidArgument
	}
	text := strings.TrimSpace(stringFromTChar(in, string128))
	var value float64
	var err error
	if info.Parse != nil {
		value, err = info.Parse(text)
	} else {
		value, err = strconv.ParseFloat(text, 64)
	}
	if err != nil {
		return resultFalse
	}
	*out = C.double(clampUnit(value))
	return resultOK
}

// INoteExpressionPhysicalUIMapping

//export GoPhysicalUIMapping
func GoPhysicalUIMapping(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, list *C.struct_Steinberg_Vst_PhysicalUIMapList) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	mapper, ok := wrapper.component.processor.(PhysicalUIMappingProcessor)
	if !ok {
		return resultFalse
	}
	mappings := mapper.PhysicalUIMappings(int32(busIndex), int16(channel))
	if list.count == 0 || list._map == nil {
		return resultOK
	}
	// The host pre-fills the physical UI types it asks for; fill in the
	// note expression each one drives.
	entries := unsafe.Slice(list._map, int(list.count))
	for i := range entries {
		entries[i].noteExpressionTypeID = invalidTypeID
		for _, m := range mappings {
			if uint32(entries[i].physicalUITypeID) == uint32(m.PhysicalUI) {
				entries[i].noteExpressionTypeID = C.Steinberg_Vst_NoteExpressionTypeID(m.TypeID)
				break
			}
		}
	}
	return resultOK
}

// IKeyswitchController

func (c *componentImpl) keyswitches(busIndex C.int32_t, channel C.int16_t) []KeyswitchInfo {
	if processor, ok := c.processor.(KeyswitchProcessor); ok {
		return processor.Keyswitches(int32(busIndex), int16(channel))
	}
	return nil
}

//export GoKeyswitchCount
func GoKeyswitchCount(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t) (count C.int32_t) {
	defer func() { _ = recover() }()
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.int32_t(len(wrapper.component.keyswitches(busIndex, channel)))
}

//export GoKeyswitchInfo
func GoKeyswitchInfo(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, index C.int32_t, out *C.struct_Steinberg_Vst_KeyswitchInfo) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	infos := wrapper.component.keyswitches(busIndex, channel)
	if index < 0 || int(index) >= len(infos) {
		return resultInvalidArgument
	}
	info := infos[index]
	*out = C.struct_Steinberg_Vst_KeyswitchInfo{}
	out.typeId = C.Steinberg_Vst_KeyswitchTypeID(info.Type)
	copyStringToTChar(info.Title, &out.title[0], len(out.title))
	copyStringToTChar(info.ShortTitle, &out.shortTitle[0], len(out.shortTitle))
	out.keyswitchMin = C.Steinberg_int32(info.Min)
	out.keyswitchMax = C.Steinberg_int32(info.Max)
	out.keyRemapped = C.Steinberg_int32(info.KeyRemapped)
	out.unitId = C.Steinberg_int32(info.UnitID)
	out.flags = C.Steinberg_int32(info.Flags)
	return resultOK
}

// IInfoListener

//export GoSetChannelInfo
func GoSetChannelInfo(componentID C.uintptr_t, in *C.VST3GoChannelInfo) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	listener, ok := wrapper.component.processor.(ChannelInfoProcessor)
	if !ok {
		return resultFalse
	}
	listener.SetChannelInfo(ChannelInfo{
		Name:              stringFromTChar(&in.name[0], len(in.name)),
		UID:               stringFromTChar(&in.uid[0], len(in.uid)),
		Color:             uint32(in.color),
		HasColor:          in.hasColor != 0,
		Index:             int64(in.index),
		HasIndex:          in.hasIndex != 0,
		IndexNamespace:    stringFromTChar(&in.indexNamespace[0], len(in.indexNamespace)),
		PluginLocation:    int64(in.pluginLocation),
		HasPluginLocation: in.hasPluginLocation != 0,
	})
	return resultOK
}
