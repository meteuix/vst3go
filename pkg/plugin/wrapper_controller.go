package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"unsafe"

	"github.com/th-release/vst3go/pkg/vst3"
)

// IEditController callbacks. A single-component object shares the parameter
// registry with the processor; the controller of a distributed plugin has
// its own registry, synchronized through the host. Neither keeps controller
// state of its own.

//export GoEditControllerSetComponentState
func GoEditControllerSetComponentState(componentID C.uintptr_t, state unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	if wrapper.kind == kindController {
		stream := vst3.NewStreamWrapper(state)
		if stream == nil {
			return resultInvalidArgument
		}
		data, err := stream.ReadAll()
		if err != nil {
			return resultFalse
		}
		if err := wrapper.component.SetState(data); err != nil {
			return resultFalse
		}
	}
	// A single component already restored this state on the shared
	// registry; make sure an open editor shows it.
	wrapper.refreshEditorParameters()
	return resultOK
}

//export GoEditControllerSetState
func GoEditControllerSetState(componentID C.uintptr_t, state unsafe.Pointer) C.Steinberg_tresult {
	return resultOK
}

//export GoEditControllerGetState
func GoEditControllerGetState(componentID C.uintptr_t, state unsafe.Pointer) C.Steinberg_tresult {
	return resultOK
}

//export GoEditControllerGetParameterCount
func GoEditControllerGetParameterCount(componentID C.uintptr_t) C.int32_t {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.int32_t(wrapper.component.GetParameterCount())
}

//export GoEditControllerGetParameterInfo
func GoEditControllerGetParameterInfo(componentID C.uintptr_t, paramIndex C.int32_t, out *C.struct_Steinberg_Vst_ParameterInfo) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || out == nil {
		return resultInvalidArgument
	}
	info, err := wrapper.component.GetParameterInfo(int32(paramIndex))
	if err != nil {
		return resultFromError(err)
	}

	out.id = C.Steinberg_Vst_ParamID(info.ID)
	copyStringToTChar(info.Title, &out.title[0], len(out.title))
	copyStringToTChar(info.ShortTitle, &out.shortTitle[0], len(out.shortTitle))
	copyStringToTChar(info.Units, &out.units[0], len(out.units))
	out.stepCount = C.Steinberg_int32(info.StepCount)
	out.defaultNormalizedValue = C.Steinberg_Vst_ParamValue(info.DefaultValue)
	out.unitId = C.Steinberg_Vst_UnitID(info.UnitID)
	out.flags = C.Steinberg_int32(info.Flags)
	return resultOK
}

// String128 is the VST3 parameter string buffer size.
const string128 = 128

//export GoEditControllerGetParamStringByValue
func GoEditControllerGetParamStringByValue(componentID C.uintptr_t, id C.Steinberg_Vst_ParamID, valueNormalized C.Steinberg_Vst_ParamValue, out *C.Steinberg_Vst_TChar) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || out == nil {
		return resultInvalidArgument
	}
	str, err := wrapper.component.GetParamStringByValue(uint32(id), float64(valueNormalized))
	if err != nil {
		return resultFromError(err)
	}
	copyStringToTChar(str, out, string128)
	return resultOK
}

//export GoEditControllerGetParamValueByString
func GoEditControllerGetParamValueByString(componentID C.uintptr_t, id C.Steinberg_Vst_ParamID, in *C.Steinberg_Vst_TChar, valueNormalized *C.Steinberg_Vst_ParamValue) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil || in == nil || valueNormalized == nil {
		return resultInvalidArgument
	}
	value, err := wrapper.component.GetParamValueByString(uint32(id), stringFromTChar(in, string128))
	if err != nil {
		return resultFalse
	}
	*valueNormalized = C.Steinberg_Vst_ParamValue(value)
	return resultOK
}

//export GoEditControllerNormalizedParamToPlain
func GoEditControllerNormalizedParamToPlain(componentID C.uintptr_t, id C.uint32_t, valueNormalized C.double) C.double {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return valueNormalized
	}
	return C.double(wrapper.component.NormalizedParamToPlain(uint32(id), float64(valueNormalized)))
}

//export GoEditControllerPlainParamToNormalized
func GoEditControllerPlainParamToNormalized(componentID C.uintptr_t, id C.uint32_t, plainValue C.double) C.double {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return plainValue
	}
	return C.double(wrapper.component.PlainParamToNormalized(uint32(id), float64(plainValue)))
}

//export GoEditControllerGetParamNormalized
func GoEditControllerGetParamNormalized(componentID C.uintptr_t, id C.uint32_t) C.double {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.double(wrapper.component.GetParamNormalized(uint32(id)))
}

//export GoEditControllerSetParamNormalized
func GoEditControllerSetParamNormalized(componentID C.uintptr_t, id C.uint32_t, value C.double) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	if err := wrapper.component.SetParamNormalized(uint32(id), float64(value)); err != nil {
		return resultFromError(err)
	}
	if p, ok := wrapper.component.params.GetOK(uint32(id)); ok {
		wrapper.notifyEditorParameterChanged(p)
	}
	return resultOK
}

// GoEditorMessage is called by the platform editor views with each JSON
// message the page posts (editorSession.handleEditorMessage).
//
//export GoEditorMessage
func GoEditorMessage(componentID C.uintptr_t, data *C.char, size C.int) {
	defer recoverPanic(nil)
	if data == nil || size <= 0 {
		return
	}
	if panel := panels.get(uintptr(componentID)); panel != nil {
		panel.onMessage(C.GoBytes(unsafe.Pointer(data), size))
		return
	}
	if c, session, host := editorTarget(componentID); c != nil {
		session.handleEditorMessage(c, host, C.GoBytes(unsafe.Pointer(data), size))
	}
}

//export GoEditControllerSetComponentHandler
func GoEditControllerSetComponentHandler(componentID C.uintptr_t, handler unsafe.Pointer) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	wrapper.setHandler(handler)
	return resultOK
}

//export GoEditControllerCreateView
func GoEditControllerCreateView(componentID C.uintptr_t, name *C.char) unsafe.Pointer {
	if name == nil || C.GoString(name) != "editor" {
		return nil
	}
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return nil
	}
	view := C.VST3GoCreateEditorView(componentID)
	if view == nil {
		return nil
	}
	wrapper.setEditorView(view)
	return view
}

//export GoEditControllerClearEditorView
func GoEditControllerClearEditorView(componentID C.uintptr_t, view unsafe.Pointer) {
	if panel := panels.get(uintptr(componentID)); panel != nil {
		panel.clearEditorView(view)
		return
	}
	if c, session, _ := editorTarget(componentID); c != nil {
		session.clearEditorView(view)
		c.endGestures()
	}
}

//export GoEditControllerGetEditorHTML
func GoEditControllerGetEditorHTML(componentID C.uintptr_t) *C.char {
	// The caller (editor view) owns and frees the returned string.
	if panel := panels.get(uintptr(componentID)); panel != nil {
		return C.CString(panelPage(panel.html))
	}
	c, _, _ := editorTarget(componentID)
	if c == nil {
		return nil
	}
	page, err := c.editorPage()
	if err != nil {
		return nil
	}
	return C.CString(page)
}

// IMidiMapping

//export GoMidiMappingGetAssignment
func GoMidiMappingGetAssignment(componentID C.uintptr_t, busIndex C.int32_t, channel C.int16_t, controller C.int16_t, id *C.Steinberg_Vst_ParamID) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil || id == nil {
		return resultFalse
	}
	paramID, ok := wrapper.component.MIDIControllerAssignment(int32(busIndex), int16(channel), int16(controller))
	if !ok {
		return resultFalse
	}
	*id = C.Steinberg_Vst_ParamID(paramID)
	return resultOK
}
