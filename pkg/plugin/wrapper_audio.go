package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
import "C"

import (
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/vst3"
)

// IAudioProcessor callbacks

func arrangements(ptr *C.Steinberg_Vst_SpeakerArrangement, count C.int32_t) []bus.SpeakerArrangement {
	if ptr == nil || count <= 0 {
		return []bus.SpeakerArrangement{}
	}
	src := unsafe.Slice(ptr, int(count))
	out := make([]bus.SpeakerArrangement, len(src))
	for i, arrangement := range src {
		out[i] = bus.SpeakerArrangement(arrangement)
	}
	return out
}

//export GoAudioSetBusArrangements
func GoAudioSetBusArrangements(componentID C.uintptr_t, inputs *C.Steinberg_Vst_SpeakerArrangement, numIns C.int32_t, outputs *C.Steinberg_Vst_SpeakerArrangement, numOuts C.int32_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || numIns < 0 || numOuts < 0 {
		return resultInvalidArgument
	}
	if wrapper.component.SetBusArrangements(arrangements(inputs, numIns), arrangements(outputs, numOuts)) {
		return resultOK
	}
	return resultFalse
}

//export GoAudioGetBusArrangement
func GoAudioGetBusArrangement(componentID C.uintptr_t, dir, index C.int32_t, arr *C.Steinberg_Vst_SpeakerArrangement) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil || arr == nil {
		return resultInvalidArgument
	}
	arrangement, ok := wrapper.component.GetBusArrangement(int32(dir), int32(index))
	if !ok {
		return resultInvalidArgument
	}
	*arr = C.Steinberg_Vst_SpeakerArrangement(arrangement)
	return resultOK
}

//export GoAudioCanProcessSampleSize
func GoAudioCanProcessSampleSize(componentID C.uintptr_t, symbolicSampleSize C.int32_t) C.Steinberg_tresult {
	switch symbolicSampleSize {
	case sampleSize32:
		return resultOK
	case sampleSize64:
		if wrapper := getComponent(componentID); wrapper != nil && wrapper.component.CanProcess64() {
			return resultOK
		}
	}
	return resultFalse
}

//export GoAudioGetLatencySamples
func GoAudioGetLatencySamples(componentID C.uintptr_t) C.uint32_t {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.uint32_t(wrapper.component.GetLatencySamples())
}

//export GoAudioSetupProcessing
func GoAudioSetupProcessing(componentID C.uintptr_t, setup *C.struct_Steinberg_Vst_ProcessSetup) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || setup == nil {
		return resultInvalidArgument
	}
	use64 := setup.symbolicSampleSize == sampleSize64
	if setup.symbolicSampleSize != sampleSize32 && (!use64 || !wrapper.component.CanProcess64()) {
		return resultFalse
	}
	err := wrapper.component.SetupProcessing(float64(setup.sampleRate), int32(setup.maxSamplesPerBlock), process.ProcessMode(setup.processMode), use64)
	return resultFromError(err)
}

//export GoAudioSetProcessing
func GoAudioSetProcessing(componentID C.uintptr_t, state C.int32_t) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	wrapper.component.SetProcessing(state != 0)
	return resultOK
}

//export GoAudioProcess
func GoAudioProcess(componentID C.uintptr_t, data *C.struct_Steinberg_Vst_ProcessData) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || data == nil {
		return resultInvalidArgument
	}
	if !wrapper.component.Process(data, wrapper.scratch) {
		return resultInvalidArgument
	}
	return resultOK
}

//export GoAudioGetTailSamples
func GoAudioGetTailSamples(componentID C.uintptr_t) C.uint32_t {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.uint32_t(wrapper.component.GetTailSamples())
}

// IProgramListData

func (w *componentWrapper) programList(listID C.int32_t) *programs {
	if w == nil || w.component.programs == nil || int32(listID) != programListID {
		return nil
	}
	return w.component.programs
}

//export GoProgramDataSupported
func GoProgramDataSupported(componentID C.uintptr_t, listID C.int32_t) C.Steinberg_tresult {
	if getComponent(componentID).programList(listID) == nil {
		return resultFalse
	}
	return resultOK
}

//export GoProgramDataGet
func GoProgramDataGet(componentID C.uintptr_t, listID, programIndex C.int32_t, state unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	programs := getComponent(componentID).programList(listID)
	stream := vst3.NewStreamWrapper(state)
	if programs == nil || stream == nil {
		return resultInvalidArgument
	}
	data, err := programs.encode(int32(programIndex))
	if err != nil {
		return resultFromError(err)
	}
	if _, err := stream.Write(data); err != nil {
		return resultFalse
	}
	return resultOK
}

//export GoProgramDataSet
func GoProgramDataSet(componentID C.uintptr_t, listID, programIndex C.int32_t, state unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	programs := wrapper.programList(listID)
	stream := vst3.NewStreamWrapper(state)
	if programs == nil || stream == nil {
		return resultInvalidArgument
	}
	data, err := stream.ReadAll()
	if err != nil {
		return resultFalse
	}
	if err := programs.decode(int32(programIndex), data, wrapper.component.params); err != nil {
		return resultFromError(err)
	}
	return resultOK
}
