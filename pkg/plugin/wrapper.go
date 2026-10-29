package plugin

// #cgo CFLAGS: -I../../include
// #include "vst3_bridge.h"
// #include "vst3_component.h"
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/plugin"
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
	kind      int32 // kindSingle, kindProcessor or kindController
	scratch   processScratch

	handlerMu        sync.RWMutex   // protects componentHandler
	componentHandler unsafe.Pointer // IComponentHandler (one reference held)

	editorSession
}

// components maps C handles to wrappers.
var components instances[componentWrapper]

// Object kinds (vst3_component.h).
const (
	kindSingle     = int32(C.VST3GO_KIND_SINGLE)
	kindProcessor  = int32(C.VST3GO_KIND_PROCESSOR)
	kindController = int32(C.VST3GO_KIND_CONTROLLER)
)

var registeredPlugins []Plugin

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

// Register adds a plugin to the factory of this binary. Call it from init();
// one binary can register several plugins (a plugin bundle).
func Register(p Plugin) {
	registeredPlugins = append(registeredPlugins, p)
}

// SetFactoryInfo sets the factory vendor information.
func SetFactoryInfo(info FactoryInfo) {
	globalFactoryInfo = info
}

// factoryClass is one class exported by the factory. A DistributedPlugin
// exports a processor class and a controller class.
type factoryClass struct {
	plugin     Plugin
	info       plugin.Info
	kind       int32
	cid        [16]byte
	controller [16]byte // controller class of a processor class
}

func factoryClasses() []factoryClass {
	classes := make([]factoryClass, 0, len(registeredPlugins))
	for _, p := range registeredPlugins {
		info := p.GetInfo()
		uid := info.UID()
		if d, ok := p.(DistributedPlugin); ok && d.Distributed() {
			controller := info.ControllerUID()
			classes = append(classes,
				factoryClass{plugin: p, info: info, kind: kindProcessor, cid: uid, controller: controller},
				factoryClass{plugin: p, info: info, kind: kindController, cid: controller})
			continue
		}
		classes = append(classes, factoryClass{plugin: p, info: info, kind: kindSingle, cid: uid})
	}
	return classes
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
		*result = C.Steinberg_tresult(vst3.ResultInternalError)
	}
}

func registerComponent(wrapper *componentWrapper) uintptr {
	wrapper.id = nextInstanceID()
	components.add(wrapper.id, wrapper)
	return wrapper.id
}

// instanceIDs numbers VST3 components and CLAP instances in one space, so an
// editor view handle identifies either.
var instanceIDs atomic.Uintptr

func nextInstanceID() uintptr { return instanceIDs.Add(1) }

func unregisterComponent(id uintptr) *componentWrapper {
	return components.remove(id)
}

func getComponent(id C.uintptr_t) *componentWrapper {
	return components.get(uintptr(id))
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

// Internal messages between the two sides of a distributed plugin.
const (
	messagePrefix  = "vst3go."
	messageRestart = messagePrefix + "restart" // int32 flags
	messageEdit    = messagePrefix + "edit"    // uint8 op, uint32 id, float64 normalized
	messageEditor  = messagePrefix + "editor"  // JSON message for the editor page
)

// hostHandle implements Host for a processor.
type hostHandle struct {
	w *componentWrapper
}

func (w *componentWrapper) restart(flags RestartFlags) { _ = hostHandle{w}.RestartComponent(flags) }

func (h hostHandle) RestartComponent(flags RestartFlags) error {
	if h.w.kind == kindProcessor {
		data := binary.LittleEndian.AppendUint32(nil, uint32(flags))
		return h.w.sendMessage(messageRestart, data)
	}
	handler := h.w.handler()
	if handler == nil {
		return errNotSupported
	}
	if C.vst3go_handler_restart(handler, C.int32_t(flags)) != resultOK {
		return errNotSupported
	}
	return nil
}

// Edit operations forwarded from the processor side of a distributed plugin.
const (
	editPerform uint8 = iota
	editBegin
	editEnd
)

func (h hostHandle) EditParameter(id uint32, normalized float64) error {
	return h.edit(editPerform, id, normalized)
}

func (h hostHandle) BeginEdit(id uint32) error { return h.edit(editBegin, id, 0) }

func (h hostHandle) EndEdit(id uint32) error { return h.edit(editEnd, id, 0) }

func (h hostHandle) edit(op uint8, id uint32, normalized float64) error {
	if h.w.kind == kindProcessor {
		if !h.w.component.params.Has(id) {
			return errInvalidArgument
		}
		data := []byte{op}
		data = binary.LittleEndian.AppendUint32(data, id)
		data = binary.LittleEndian.AppendUint64(data, math.Float64bits(normalized))
		return h.w.sendMessage(messageEdit, data)
	}
	return h.w.component.edit(op, id, normalized)
}

func (h hostHandle) PostEditorMessage(name string, data any) error {
	msg, err := customEditorMessage(name, data)
	if err != nil {
		return err
	}
	if h.w.kind == kindProcessor {
		return h.w.sendMessage(messageEditor, msg)
	}
	h.w.postRaw(msg)
	return nil
}

func (h hostHandle) SendMessage(id string, data []byte) error {
	if h.w.kind == kindSingle {
		return errNotSupported
	}
	if id == "" || strings.HasPrefix(id, messagePrefix) {
		return errInvalidArgument
	}
	return h.w.sendMessage(id, data)
}

// sendMessage sends an IMessage to the connected peer through the host.
func (w *componentWrapper) sendMessage(id string, data []byte) error {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	var ptr unsafe.Pointer
	if len(data) > 0 {
		ptr = unsafe.Pointer(&data[0])
	}
	if C.vst3go_send_message(w.handle, cid, ptr, C.uint32_t(len(data))) != resultOK {
		return errNotSupported
	}
	return nil
}

// receiveMessage handles a message from the peer object.
func (w *componentWrapper) receiveMessage(id string, data []byte) {
	switch id {
	case messageRestart:
		if w.kind == kindController && len(data) >= 4 {
			_ = hostHandle{w}.RestartComponent(RestartFlags(int32(binary.LittleEndian.Uint32(data))))
		}
		return
	case messageState:
		_ = w.applyState(data, false)
		return
	case messageEditor:
		if w.kind == kindController {
			w.postRaw(data)
		}
		return
	case messageEdit:
		if w.kind == kindController && len(data) >= 13 {
			id := binary.LittleEndian.Uint32(data[1:])
			value := math.Float64frombits(binary.LittleEndian.Uint64(data[5:]))
			_ = w.component.edit(data[0], id, value)
		}
		return
	}
	if receiver, ok := w.component.processor.(MessageReceiver); ok {
		receiver.ReceiveMessage(id, data)
	}
}

//export GoConnectionNotify
func GoConnectionNotify(componentID C.uintptr_t, id *C.char, data unsafe.Pointer, size C.uint32_t) {
	defer recoverPanic(nil)
	wrapper := getComponent(componentID)
	if wrapper == nil || id == nil {
		return
	}
	var payload []byte
	if data != nil && size > 0 {
		payload = unsafe.Slice((*byte)(data), int(size))
	}
	wrapper.receiveMessage(C.GoString(id), payload)
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
	return C.int32_t(len(factoryClasses()))
}

//export GoGetClassInfo2
func GoGetClassInfo2(index C.int32_t, info *C.struct_Steinberg_PClassInfo2) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	classes := factoryClasses()
	if info == nil || index < 0 || int(index) >= len(classes) {
		return resultInvalidArgument
	}
	class := classes[index]

	*info = C.struct_Steinberg_PClassInfo2{}
	for i, b := range class.cid {
		info.cid[i] = C.char(b)
	}
	info.cardinality = C.Steinberg_PClassInfo_ClassCardinality_kManyInstances
	name := class.info.Name
	switch class.kind {
	case kindController:
		copyCString(&info.category[0], len(info.category), "Component Controller Class")
		name += " Controller"
	default:
		copyCString(&info.category[0], len(info.category), "Audio Module Class")
		copyCString(&info.subCategories[0], len(info.subCategories), class.info.SubCategories())
		if class.kind == kindProcessor {
			info.classFlags = C.Steinberg_Vst_ComponentFlags_kDistributable
		}
	}
	copyCString(&info.name[0], len(info.name), name)
	vendor := class.info.Vendor
	if vendor == "" {
		vendor = globalFactoryInfo.Vendor
	}
	copyCString(&info.vendor[0], len(info.vendor), vendor)
	copyCString(&info.version[0], len(info.version), class.info.Version)
	copyCString(&info.sdkVersion[0], len(info.sdkVersion), SDKVersion)
	return resultOK
}

// capabilities returns the optional interfaces an instance exposes.
func capabilities(component *componentImpl) C.int32_t {
	var caps C.int32_t
	processor := component.processor
	if _, ok := processor.(UnitProcessor); ok || component.programs != nil {
		caps |= C.VST3GO_CAP_UNITS
	}
	if component.programs != nil {
		caps |= C.VST3GO_CAP_PROGRAM_DATA
	}
	if _, ok := processor.(NoteExpressionProcessor); ok {
		caps |= C.VST3GO_CAP_NOTE_EXPRESSION
	}
	if _, ok := processor.(PhysicalUIMappingProcessor); ok {
		caps |= C.VST3GO_CAP_PHYSICAL_UI
	}
	if _, ok := processor.(KeyswitchProcessor); ok {
		caps |= C.VST3GO_CAP_KEYSWITCH
	}
	_, learn1 := processor.(MIDILearnProcessor)
	_, learn2 := processor.(MIDI2LearnProcessor)
	if learn1 || learn2 {
		caps |= C.VST3GO_CAP_MIDI_LEARN
	}
	if _, ok := processor.(ChannelInfoProcessor); ok {
		caps |= C.VST3GO_CAP_INFO_LISTENER
	}
	return caps
}

//export GoCreateInstance
func GoCreateInstance(cid *C.char, iid *C.char) (instance unsafe.Pointer) {
	defer func() {
		if recover() != nil {
			instance = nil
		}
	}()
	if cid == nil {
		return nil
	}
	requested := unsafe.Slice((*byte)(unsafe.Pointer(cid)), 16)
	var class *factoryClass
	classes := factoryClasses()
	for i := range classes {
		if string(requested) == string(classes[i].cid[:]) {
			class = &classes[i]
			break
		}
	}
	if class == nil {
		return nil
	}

	processor := class.plugin.CreateProcessor()
	if processor == nil {
		return nil
	}
	component, err := newComponent(processor, class.info)
	if err != nil {
		return nil
	}

	wrapper := &componentWrapper{component: component, kind: class.kind, scratch: newProcessScratch()}
	component.wrapper = wrapper
	id := registerComponent(wrapper)

	controller := C.CBytes(class.controller[:])
	defer C.free(controller)
	cComponent := C.createComponent(C.uintptr_t(id), C.int32_t(class.kind), capabilities(component), (*C.char)(controller))
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
	// Drops the handler reference and detaches the processor's Host.
	wrapper.setHandler(nil)
	wrapper.scratch.free()
}

// IComponent

//export GoComponentInitialize
func GoComponentInitialize(componentID C.uintptr_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	if err := wrapper.component.Initialize(); err != nil {
		return resultFromError(err)
	}
	if wrapper.kind == kindProcessor {
		wrapper.setProcessorHost(true)
	}
	return resultOK
}

//export GoComponentTerminate
func GoComponentTerminate(componentID C.uintptr_t) (result C.Steinberg_tresult) {
	defer recoverPanic(&result)
	wrapper := getComponent(componentID)
	if wrapper == nil {
		return resultFalse
	}
	if wrapper.kind == kindProcessor {
		wrapper.setProcessorHost(false)
	} else {
		wrapper.setHandler(nil)
	}
	return resultOK
}

// setProcessorHost hands the processor side of a distributed plugin its
// message-based Host.
func (w *componentWrapper) setProcessorHost(connected bool) {
	aware, ok := w.component.processor.(HostAwareProcessor)
	if !ok {
		return
	}
	if connected {
		aware.SetHost(hostHandle{w})
	} else {
		aware.SetHost(nil)
	}
}

// IEditController lifecycle of controller-kind objects. A single-component
// object is initialized through IComponent only.

//export GoControllerInitialize
func GoControllerInitialize(componentID C.uintptr_t) C.Steinberg_tresult {
	if getComponent(componentID) == nil {
		return resultFalse
	}
	return resultOK
}

//export GoControllerTerminate
func GoControllerTerminate(componentID C.uintptr_t) (result C.Steinberg_tresult) {
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
	// IoModes (simple/advanced/offline) are deprecated by the SDK.
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
	w.refreshEditor(w.component.params)
}
