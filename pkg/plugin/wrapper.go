package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
// #include "vst3_component.h"
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"sync"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/vst3"
)

// SDKVersion is the VST3 SDK version reported in PClassInfo2.
const SDKVersion = "VST 3.8.1"

// componentWrapper ties one C COM object to its Go component. C holds only an
// integer handle (never a Go pointer) and looks the wrapper up per call.
type componentWrapper struct {
	component *componentImpl
	handle    unsafe.Pointer // C COM object
	id        uintptr
	scratch   processScratch

	handlerMu        sync.RWMutex   // protects componentHandler and editorView
	componentHandler unsafe.Pointer // IComponentHandler (one reference held)
	editorView       unsafe.Pointer
}

// components maps C handles to wrappers. Writes happen only on instance
// creation/destruction, so the read lock taken per call is uncontended and
// allocation-free, which keeps it safe for the audio thread.
var (
	components   = make(map[uintptr]*componentWrapper)
	componentsMu sync.RWMutex
	nextID       uintptr
)

var globalPlugin Plugin

// FactoryInfo describes the vendor reported by the plugin factory.
type FactoryInfo struct {
	Vendor string
	URL    string
	Email  string
}

var globalFactoryInfo = FactoryInfo{
	Vendor: "VST3Go",
	URL:    "https://github.com/th-release/vst3go",
	Email:  "",
}

// Register sets the plugin exported by this binary. Call it from init().
func Register(p Plugin) {
	globalPlugin = p
}

// SetFactoryInfo sets the factory vendor information.
func SetFactoryInfo(info FactoryInfo) {
	globalFactoryInfo = info
}

var (
	resultOK              = C.Steinberg_tresult(vst3.ResultOK)
	resultFalse           = C.Steinberg_tresult(vst3.ResultFalse)
	resultInvalidArgument = C.Steinberg_tresult(vst3.ResultInvalidArgument)
	resultNotImplemented  = C.Steinberg_tresult(vst3.ResultNotImplemented)
)

func resultFromError(err error) C.Steinberg_tresult {
	switch err {
	case nil:
		return resultOK
	case errInvalidArgument:
		return resultInvalidArgument
	case errNotSupported:
		return resultNotImplemented
	default:
		return resultFalse
	}
}

// recoverPanic converts a panic in a host callback into kInternalError
// instead of unwinding through C, which would crash the host.
func recoverPanic(result *C.Steinberg_tresult) {
	if r := recover(); r != nil && result != nil {
		*result = C.Steinberg_kInternalError
	}
}

func registerComponent(wrapper *componentWrapper) uintptr {
	componentsMu.Lock()
	defer componentsMu.Unlock()
	nextID++
	wrapper.id = nextID
	components[nextID] = wrapper
	return nextID
}

func unregisterComponent(id uintptr) *componentWrapper {
	componentsMu.Lock()
	defer componentsMu.Unlock()
	wrapper := components[id]
	delete(components, id)
	return wrapper
}

func getComponent(id C.uintptr_t) *componentWrapper {
	componentsMu.RLock()
	wrapper := components[uintptr(id)]
	componentsMu.RUnlock()
	return wrapper
}

// Host notifications (UI/main thread)

func (w *componentWrapper) handler() unsafe.Pointer {
	w.handlerMu.RLock()
	defer w.handlerMu.RUnlock()
	return w.componentHandler
}

func (w *componentWrapper) setHandler(handler unsafe.Pointer) {
	if handler != nil {
		C.vst3go_unknown_add_ref(handler)
	}
	w.handlerMu.Lock()
	old := w.componentHandler
	w.componentHandler = handler
	w.handlerMu.Unlock()
	if old != nil {
		C.vst3go_unknown_release(old)
	}

	if aware, ok := w.component.processor.(HostAwareProcessor); ok {
		if handler == nil {
			aware.SetHost(nil)
		} else {
			aware.SetHost(hostHandle{w})
		}
	}
}

func (w *componentWrapper) notifyParamBeginEdit(id uint32) {
	if h := w.handler(); h != nil {
		C.vst3go_handler_begin_edit(h, C.Steinberg_Vst_ParamID(id))
	}
}

func (w *componentWrapper) notifyParamPerformEdit(id uint32, normalized float64) {
	if h := w.handler(); h != nil {
		C.vst3go_handler_perform_edit(h, C.Steinberg_Vst_ParamID(id), C.Steinberg_Vst_ParamValue(normalized))
	}
}

func (w *componentWrapper) notifyParamEndEdit(id uint32) {
	if h := w.handler(); h != nil {
		C.vst3go_handler_end_edit(h, C.Steinberg_Vst_ParamID(id))
	}
}

func (w *componentWrapper) setEditorView(view unsafe.Pointer) {
	w.handlerMu.Lock()
	w.editorView = view
	w.handlerMu.Unlock()
}

func (w *componentWrapper) clearEditorView(view unsafe.Pointer) {
	w.handlerMu.Lock()
	if w.editorView == view {
		w.editorView = nil
	}
	w.handlerMu.Unlock()
}

func (w *componentWrapper) notifyEditorParameterChanged(id uint32, normalized, plain float64) {
	w.handlerMu.RLock()
	view := w.editorView
	w.handlerMu.RUnlock()
	if view != nil {
		C.VST3GoEditorViewUpdateParameter(view, C.Steinberg_Vst_ParamID(id), C.Steinberg_Vst_ParamValue(normalized), C.Steinberg_Vst_ParamValue(plain))
	}
}

// hostHandle implements Host for a processor.
type hostHandle struct {
	w *componentWrapper
}

func (h hostHandle) RestartComponent(flags RestartFlags) error {
	handler := h.w.handler()
	if handler == nil {
		return errNotSupported
	}
	if C.vst3go_handler_restart(handler, C.int32_t(flags)) != resultOK {
		return errNotSupported
	}
	return nil
}

func (h hostHandle) EditParameter(id uint32, normalized float64) error {
	return h.w.component.EditParameter(id, normalized)
}

// Factory callbacks

//export GoGetFactoryInfo
func GoGetFactoryInfo(info *C.struct_Steinberg_PFactoryInfo) {
	copyCString(&info.vendor[0], len(info.vendor), globalFactoryInfo.Vendor)
	copyCString(&info.url[0], len(info.url), globalFactoryInfo.URL)
	copyCString(&info.email[0], len(info.email), globalFactoryInfo.Email)
	info.flags = C.Steinberg_PFactoryInfo_FactoryFlags_kUnicode
}

//export GoCountClasses
func GoCountClasses() C.int32_t {
	if globalPlugin == nil {
		return 0
	}
	return 1
}

//export GoGetClassInfo2
func GoGetClassInfo2(index C.int32_t, info *C.struct_Steinberg_PClassInfo2) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	if globalPlugin == nil || index != 0 {
		return resultInvalidArgument
	}

	pluginInfo := globalPlugin.GetInfo()
	uid := pluginInfo.UID()
	for i, b := range uid {
		info.cid[i] = C.char(b)
	}
	info.cardinality = C.Steinberg_PClassInfo_ClassCardinality_kManyInstances
	copyCString(&info.category[0], len(info.category), "Audio Module Class")
	copyCString(&info.name[0], len(info.name), pluginInfo.Name)
	info.classFlags = 0
	copyCString(&info.subCategories[0], len(info.subCategories), pluginInfo.SubCategories())
	vendor := pluginInfo.Vendor
	if vendor == "" {
		vendor = globalFactoryInfo.Vendor
	}
	copyCString(&info.vendor[0], len(info.vendor), vendor)
	copyCString(&info.version[0], len(info.version), pluginInfo.Version)
	copyCString(&info.sdkVersion[0], len(info.sdkVersion), SDKVersion)
	return resultOK
}

//export GoCreateInstance
func GoCreateInstance(cid *C.char, iid *C.char) (instance unsafe.Pointer) {
	defer func() {
		if recover() != nil {
			instance = nil
		}
	}()
	if globalPlugin == nil || cid == nil {
		return nil
	}

	pluginInfo := globalPlugin.GetInfo()
	requested := unsafe.Slice((*byte)(unsafe.Pointer(cid)), 16)
	uid := pluginInfo.UID()
	if string(requested) != string(uid[:]) {
		return nil
	}

	processor := globalPlugin.CreateProcessor()
	if processor == nil {
		return nil
	}
	component, err := newComponent(processor, pluginInfo)
	if err != nil {
		return nil
	}

	wrapper := &componentWrapper{component: component, scratch: newProcessScratch()}
	component.wrapper = wrapper
	id := registerComponent(wrapper)

	cComponent := C.createComponent(C.uintptr_t(id))
	if cComponent == nil {
		unregisterComponent(id)
		wrapper.scratch.free()
		return nil
	}
	wrapper.handle = cComponent
	return cComponent
}

//export GoReleaseComponent
func GoReleaseComponent(componentID C.uintptr_t) {
	wrapper := unregisterComponent(uintptr(componentID))
	if wrapper == nil {
		return
	}
	wrapper.setHandler(nil)
	wrapper.scratch.free()
}

// IComponent

//export GoComponentInitialize
func GoComponentInitialize(componentID C.uintptr_t, context unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	return resultFromError(wrapper.component.Initialize())
}

//export GoComponentTerminate
func GoComponentTerminate(componentID C.uintptr_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	wrapper.setHandler(nil)
	return resultOK
}

//export GoComponentSetIoMode
func GoComponentSetIoMode(componentID C.uintptr_t, mode C.int32_t) C.Steinberg_tresult {
	// IoModes only matter for split component/controller plugins.
	return resultNotImplemented
}

//export GoComponentGetBusCount
func GoComponentGetBusCount(componentID C.uintptr_t, mediaType, dir C.int32_t) C.int32_t {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return 0
	}
	return C.int32_t(wrapper.component.GetBusCount(int32(mediaType), int32(dir)))
}

//export GoComponentGetBusInfo
func GoComponentGetBusInfo(componentID C.uintptr_t, mediaType, dir, index C.int32_t, out *C.struct_Steinberg_Vst_BusInfo) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil || out == nil {
		return resultInvalidArgument
	}
	info := wrapper.component.busInfo(int32(mediaType), int32(dir), int32(index))
	if info == nil {
		return resultInvalidArgument
	}

	out.mediaType = C.Steinberg_Vst_MediaType(info.MediaType)
	out.direction = C.Steinberg_Vst_BusDirection(info.Direction)
	out.channelCount = C.Steinberg_int32(info.ChannelCount)
	copyStringToTChar(info.Name, &out.name[0], len(out.name))
	out.busType = C.Steinberg_Vst_BusType(info.BusType)
	// Main audio and event buses start active; aux buses (sidechains,
	// extra outputs) are opt-in for the host, as in the Steinberg SDK.
	out.flags = 0
	if info.BusType == bus.TypeMain || info.MediaType == bus.MediaTypeEvent {
		out.flags = C.Steinberg_Vst_BusInfo_BusFlags_kDefaultActive
	}
	return resultOK
}

//export GoComponentActivateBus
func GoComponentActivateBus(componentID C.uintptr_t, mediaType, dir, index, state C.int32_t) C.Steinberg_tresult {
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	return resultFromError(wrapper.component.ActivateBus(int32(mediaType), int32(dir), int32(index), state != 0))
}

//export GoComponentSetActive
func GoComponentSetActive(componentID C.uintptr_t, state C.int32_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	return resultFromError(wrapper.component.SetActive(state != 0))
}

//export GoComponentSetState
func GoComponentSetState(componentID C.uintptr_t, state unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	stream := vst3.NewStreamWrapper(state)
	if wrapper == nil || stream == nil {
		return resultInvalidArgument
	}
	data, err := stream.ReadAll()
	if err != nil {
		return resultFalse
	}
	if err := wrapper.component.SetState(data); err != nil {
		return resultFalse
	}
	wrapper.refreshEditorParameters()
	return resultOK
}

//export GoComponentGetState
func GoComponentGetState(componentID C.uintptr_t, state unsafe.Pointer) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	stream := vst3.NewStreamWrapper(state)
	if wrapper == nil || stream == nil {
		return resultInvalidArgument
	}
	data, err := wrapper.component.GetState()
	if err != nil {
		return resultFalse
	}
	if _, err := stream.Write(data); err != nil {
		return resultFalse
	}
	return resultOK
}

// refreshEditorParameters pushes every parameter value to an open editor,
// e.g. after a preset load.
func (w *componentWrapper) refreshEditorParameters() {
	for _, p := range w.component.params.All() {
		w.notifyEditorParameterChanged(p.ID, p.GetNormalized(), p.GetPlain())
	}
}
