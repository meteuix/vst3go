package plugin

// #cgo CFLAGS: -I../../include
// #include "ara.h"
// #include <stdlib.h>
import "C"

import (
	"errors"
	"fmt"
	"os"
	"runtime/cgo"
	"sync"
	"unsafe"

	"github.com/th-release/vst3go/pkg/ara"
)

// maxARAFactories is the number of ARA plugins one binary can register
// (VST3GO_ARA_MAX_FACTORIES).
const maxARAFactories = C.VST3GO_ARA_MAX_FACTORIES

// araSlot is the ARA factory of one ARAPlugin.
type araSlot struct {
	plugin  int // index in registeredPlugins
	config  ara.Config
	factory *C.ARAFactory
}

var (
	araOnce  sync.Once
	araSlots []araSlot
)

func araPluginCount() int {
	n := 0
	for _, p := range registeredPlugins {
		if _, ok := p.(ARAPlugin); ok {
			n++
		}
	}
	return n
}

// araFactories creates the ARA factories of the registered ARAPlugins, in
// registration order.
func araFactories() []araSlot {
	araOnce.Do(func() {
		for i, p := range registeredPlugins {
			ap, ok := p.(ARAPlugin)
			if !ok {
				continue
			}
			info := p.GetInfo()
			config := ap.ARA()
			vendor := info.Vendor
			if vendor == "" {
				vendor = globalFactoryInfo.Vendor
			}
			archiveID := config.ArchiveID
			if archiveID == "" {
				archiveID = info.ID
			}
			var types *C.int32_t
			if n := len(config.ContentTypes); n > 0 {
				types = (*C.int32_t)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.int32_t(0)))))
				for j, t := range config.ContentTypes {
					unsafe.Slice(types, n)[j] = C.int32_t(t)
				}
			}
			slot := len(araSlots)
			factory := C.vst3go_ara_factory_new(C.int32_t(slot), C.CString(info.ID+".ara"), C.CString(info.Name),
				C.CString(vendor), C.CString(globalFactoryInfo.URL), C.CString(info.Version), C.CString(archiveID),
				types, C.int32_t(len(config.ContentTypes)), C.int32_t(config.Transformations))
			if factory == nil {
				panic("plugin: creating the ARA factory of " + info.ID)
			}
			araSlots = append(araSlots, araSlot{plugin: i, config: config, factory: factory})
		}
	})
	return araSlots
}

// araSlotOf returns the ARA factory index of a registered plugin, -1 if it
// is not an ARAPlugin.
func araSlotOf(pluginIndex int) int {
	for i, s := range araFactories() {
		if s.plugin == pluginIndex {
			return i
		}
	}
	return -1
}

// araInstance is the ARA side of one plugin instance.
type araInstance struct {
	slot      int
	binding   *ara.Binding
	handle    cgo.Handle
	extension *C.ARAPlugInExtensionInstance
}

// newARAInstance returns the ARA side of an instance of a registered
// plugin, nil if it is not an ARAPlugin.
func newARAInstance(pluginIndex int) *araInstance {
	slot := araSlotOf(pluginIndex)
	if slot < 0 {
		return nil
	}
	return &araInstance{slot: slot}
}

func (a *araInstance) factory() *C.ARAFactory {
	if a == nil {
		return nil
	}
	return araFactories()[a.slot].factory
}

// bindARA binds the instance to the document of a document controller of
// its factory; the host does it once, before activating the instance.
func (c *componentImpl) bindARA(ref unsafe.Pointer, known, assigned C.int32_t) *C.ARAPlugInExtensionInstance {
	a := c.ara
	if a == nil || a.binding != nil || ref == nil {
		return nil
	}
	controller, ok := cgo.Handle((*C.VST3GoARAController)(ref)._go).Value().(*araController)
	if !ok || controller.slot != a.slot {
		return nil
	}
	b := controller.doc.Bind(ara.Roles(known), ara.Roles(assigned))
	a.binding = b
	a.handle = cgo.NewHandle(b)
	a.extension = C.vst3go_ara_extension_new(C.uintptr_t(a.handle), C.int32_t(b.Roles()))
	if p, ok := c.processor.(ARAProcessor); ok {
		p.BindARA(b)
	}
	return a.extension
}

// release unbinds the instance, which is being destroyed.
func (a *araInstance) release() {
	if a == nil || a.binding == nil {
		return
	}
	a.binding.Unbind()
	a.handle.Delete()
	C.vst3go_ara_extension_free(a.extension)
	a.binding, a.extension = nil, nil
}

//export GoARAComponentFactory
func GoARAComponentFactory(componentID C.uintptr_t) (factory *C.ARAFactory) {
	defer araRecover()
	if w := getComponent(componentID); w != nil {
		return w.component.ara.factory()
	}
	return nil
}

//export GoARAComponentBind
func GoARAComponentBind(componentID C.uintptr_t, controller unsafe.Pointer, known, assigned C.int32_t) (extension *C.ARAPlugInExtensionInstance) {
	defer araRecover()
	if w := getComponent(componentID); w != nil {
		return w.component.bindARA(controller, known, assigned)
	}
	return nil
}

//export GoClapARAFactory
func GoClapARAFactory(id C.uintptr_t) (factory *C.ARAFactory) {
	defer araRecover()
	if inst := getClap(id); inst != nil {
		return inst.c.ara.factory()
	}
	return nil
}

//export GoClapARABind
func GoClapARABind(id C.uintptr_t, controller unsafe.Pointer, known, assigned C.int32_t) (extension *C.ARAPlugInExtensionInstance) {
	defer araRecover()
	if inst := getClap(id); inst != nil {
		return inst.c.bindARA(controller, known, assigned)
	}
	return nil
}

//export GoARAFactoryCount
func GoARAFactoryCount() C.uint32_t { return C.uint32_t(len(araFactories())) }

//export GoARAPluginIndex
func GoARAPluginIndex(slot C.int32_t) C.int32_t {
	slots := araFactories()
	if slot < 0 || int(slot) >= len(slots) {
		return -1
	}
	return C.int32_t(slots[slot].plugin)
}

// araRecover keeps a panic in a plugin's ARA code from unwinding into the
// host; the call returns its zero value.
func araRecover() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "vst3go: ARA call failed: %v\n", r)
	}
}

// Document controllers

// araController is one ARA document of the host.
type araController struct {
	slot int
	c    *C.VST3GoARAController
	doc  *ara.Document
	self cgo.Handle
}

func araControllerOf(h C.uintptr_t) *araController {
	return cgo.Handle(h).Value().(*araController)
}

func araRef(v any) C.uintptr_t { return C.uintptr_t(cgo.NewHandle(v)) }

func araObject[T any](ref C.uintptr_t) T { return cgo.Handle(ref).Value().(T) }

// araOptional returns the object of a ref the host may leave NULL.
func araOptional[T any](ref C.uintptr_t) T {
	var zero T
	if ref == 0 {
		return zero
	}
	return araObject[T](ref)
}

//export GoARACreateController
func GoARACreateController(slot C.int32_t, c *C.VST3GoARAController, name *C.char) (handle C.uintptr_t) {
	defer araRecover()
	slots := araFactories()
	if int(slot) >= len(slots) {
		return 0
	}
	ctl := &araController{slot: int(slot), c: c}
	ctl.doc = ara.NewDocument(slots[slot].config, araHost{c}, C.GoString(name))
	ctl.self = cgo.NewHandle(ctl)
	return C.uintptr_t(ctl.self)
}

//export GoARADestroyController
func GoARADestroyController(h C.uintptr_t) {
	defer araRecover()
	ctl := araControllerOf(h)
	ctl.doc.Destroy()
	ctl.self.Delete()
}

//export GoARABeginEditing
func GoARABeginEditing(h C.uintptr_t) {
	defer araRecover()
	araControllerOf(h).doc.BeginEditing()
}

//export GoARAEndEditing
func GoARAEndEditing(h C.uintptr_t) {
	defer araRecover()
	araControllerOf(h).doc.EndEditing()
}

//export GoARANotifyModelUpdates
func GoARANotifyModelUpdates(h C.uintptr_t) {
	defer araRecover()
	araControllerOf(h).doc.NotifyModelUpdates()
}

//export GoARAUpdateDocument
func GoARAUpdateDocument(h C.uintptr_t, name *C.char) {
	defer araRecover()
	araControllerOf(h).doc.SetName(C.GoString(name))
}

//export GoARAStore
func GoARAStore(h C.uintptr_t, writer unsafe.Pointer, filter *C.VST3GoARAStoreFilter) (ok C.ARABool) {
	defer araRecover()
	ctl := araControllerOf(h)
	var f *ara.StoreFilter
	if filter != nil {
		f = &ara.StoreFilter{Document: filter.document != 0}
		for _, ref := range araRefs(unsafe.Pointer(filter.sources), C.ARASize(filter.sourceCount)) {
			f.Sources = append(f.Sources, araObject[*ara.AudioSource](ref))
		}
		for _, ref := range araRefs(unsafe.Pointer(filter.modifications), C.ARASize(filter.modificationCount)) {
			f.Modifications = append(f.Modifications, araObject[*ara.AudioModification](ref))
		}
	}
	data, err := ctl.doc.Store(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vst3go: %v\n", err)
		return C.kARAFalse
	}
	return C.vst3go_ara_archive_write(ctl.c, writer, 0, C.size_t(len(data)), unsafe.Pointer(unsafe.SliceData(data)))
}

//export GoARARestore
func GoARARestore(h C.uintptr_t, reader unsafe.Pointer, filter *C.VST3GoARARestoreFilter) (ok C.ARABool) {
	defer araRecover()
	ctl := araControllerOf(h)
	size := int(C.vst3go_ara_archive_size(ctl.c, reader))
	data := make([]byte, size)
	if size > 0 && C.vst3go_ara_archive_read(ctl.c, reader, 0, C.size_t(size), unsafe.Pointer(&data[0])) == 0 {
		fmt.Fprintln(os.Stderr, "vst3go: ara: the host could not read the archive")
		return C.kARAFalse
	}
	var f *ara.RestoreFilter
	if filter != nil {
		f = &ara.RestoreFilter{
			Document:      filter.document != 0,
			Sources:       araIDMap(filter.sourceArchiveIDs, filter.sourceCurrentIDs, filter.sourceCount),
			Modifications: araIDMap(filter.modificationArchiveIDs, filter.modificationCurrentIDs, filter.modificationCount),
		}
	}
	if err := ctl.doc.Restore(data, f); err != nil {
		fmt.Fprintf(os.Stderr, "vst3go: %v\n", err)
		return C.kARAFalse
	}
	return C.kARATrue
}

func araIDMap(archived, current **C.char, count C.size_t) map[string]string {
	ids := make(map[string]string, int(count))
	if count == 0 {
		return ids
	}
	a, c := unsafe.Slice(archived, int(count)), unsafe.Slice(current, int(count))
	for i := range a {
		ids[C.GoString(a[i])] = C.GoString(c[i])
	}
	return ids
}

func araColor(c *C.ARAColor) *ara.Color {
	if c == nil {
		return nil
	}
	return &ara.Color{R: float32(c.r), G: float32(c.g), B: float32(c.b)}
}

func araContextProps(p *C.VST3GoARAContextProps) ara.MusicalContextProperties {
	return ara.MusicalContextProperties{Name: C.GoString(p.name), Order: int32(p.order), Color: araColor(p.color)}
}

//export GoARACreateContext
func GoARACreateContext(h, hostRef C.uintptr_t, p *C.VST3GoARAContextProps) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araControllerOf(h).doc.CreateMusicalContext(uintptr(hostRef), araContextProps(p)))
}

//export GoARAUpdateContext
func GoARAUpdateContext(h, ref C.uintptr_t, p *C.VST3GoARAContextProps) {
	defer araRecover()
	araObject[*ara.MusicalContext](ref).Update(araContextProps(p))
}

//export GoARAUpdateContextContent
func GoARAUpdateContextContent(h, ref C.uintptr_t) {
	defer araRecover()
	araObject[*ara.MusicalContext](ref).UpdateContent()
}

func araSequenceProps(p *C.VST3GoARASequenceProps) ara.RegionSequenceProperties {
	return ara.RegionSequenceProperties{
		Name:    C.GoString(p.name),
		Order:   int32(p.order),
		Context: araOptional[*ara.MusicalContext](p.context),
		Color:   araColor(p.color),
	}
}

//export GoARACreateSequence
func GoARACreateSequence(h, hostRef C.uintptr_t, p *C.VST3GoARASequenceProps) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araControllerOf(h).doc.CreateRegionSequence(uintptr(hostRef), araSequenceProps(p)))
}

//export GoARAUpdateSequence
func GoARAUpdateSequence(h, ref C.uintptr_t, p *C.VST3GoARASequenceProps) {
	defer araRecover()
	araObject[*ara.RegionSequence](ref).Update(araSequenceProps(p))
}

func araSourceProps(p *C.ARAAudioSourceProperties) ara.AudioSourceProperties {
	return ara.AudioSourceProperties{
		Name:         C.GoString(p.name),
		PersistentID: C.GoString(p.persistentID),
		Samples:      int64(p.sampleCount),
		SampleRate:   float64(p.sampleRate),
		Channels:     int(p.channelCount),
		Merits64Bit:  p.merits64BitSamples != 0,
	}
}

//export GoARACreateSource
func GoARACreateSource(h, hostRef C.uintptr_t, p *C.ARAAudioSourceProperties) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araControllerOf(h).doc.CreateAudioSource(uintptr(hostRef), araSourceProps(p)))
}

//export GoARAUpdateSource
func GoARAUpdateSource(h, ref C.uintptr_t, p *C.ARAAudioSourceProperties) {
	defer araRecover()
	araObject[*ara.AudioSource](ref).Update(araSourceProps(p))
}

//export GoARAUpdateSourceContent
func GoARAUpdateSourceContent(h, ref C.uintptr_t, flags C.int32_t) {
	defer araRecover()
	araObject[*ara.AudioSource](ref).UpdateContent(ara.ContentUpdateFlags(flags))
}

//export GoARAEnableSourceAccess
func GoARAEnableSourceAccess(h, ref C.uintptr_t, enable C.ARABool) {
	defer araRecover()
	araObject[*ara.AudioSource](ref).EnableAccess(enable != 0)
}

func araModificationProps(p *C.ARAAudioModificationProperties) ara.AudioModificationProperties {
	return ara.AudioModificationProperties{Name: C.GoString(p.name), PersistentID: C.GoString(p.persistentID)}
}

//export GoARACreateModification
func GoARACreateModification(h, source, hostRef C.uintptr_t, p *C.ARAAudioModificationProperties) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araObject[*ara.AudioSource](source).CreateAudioModification(uintptr(hostRef), araModificationProps(p)))
}

//export GoARACloneModification
func GoARACloneModification(h, modification, hostRef C.uintptr_t, p *C.ARAAudioModificationProperties) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araObject[*ara.AudioModification](modification).Clone(uintptr(hostRef), araModificationProps(p)))
}

//export GoARAUpdateModification
func GoARAUpdateModification(h, ref C.uintptr_t, p *C.ARAAudioModificationProperties) {
	defer araRecover()
	araObject[*ara.AudioModification](ref).Update(araModificationProps(p))
}

//export GoARAPreservesSignal
func GoARAPreservesSignal(h, ref C.uintptr_t) (preserves C.ARABool) {
	defer araRecover()
	return araBool(araObject[*ara.AudioModification](ref).PreservesSignal())
}

func araBool(b bool) C.ARABool {
	if b {
		return C.kARATrue
	}
	return C.kARAFalse
}

func araRegionProps(p *C.VST3GoARARegionProps) ara.PlaybackRegionProperties {
	return ara.PlaybackRegionProperties{
		Flags:                  ara.TransformationFlags(p.flags),
		StartInModification:    float64(p.startInModification),
		DurationInModification: float64(p.durationInModification),
		StartInPlayback:        float64(p.startInPlayback),
		DurationInPlayback:     float64(p.durationInPlayback),
		Sequence:               araOptional[*ara.RegionSequence](p.sequence),
		Name:                   C.GoString(p.name),
		Color:                  araColor(p.color),
	}
}

//export GoARACreateRegion
func GoARACreateRegion(h, modification, hostRef C.uintptr_t, p *C.VST3GoARARegionProps) (ref C.uintptr_t) {
	defer araRecover()
	return araRef(araObject[*ara.AudioModification](modification).CreatePlaybackRegion(uintptr(hostRef), araRegionProps(p)))
}

//export GoARAUpdateRegion
func GoARAUpdateRegion(h, ref C.uintptr_t, p *C.VST3GoARARegionProps) {
	defer araRecover()
	araObject[*ara.PlaybackRegion](ref).Update(araRegionProps(p))
}

//export GoARAHeadAndTail
func GoARAHeadAndTail(h, ref C.uintptr_t, head, tail *C.double) {
	defer araRecover()
	hd, tl := araObject[*ara.PlaybackRegion](ref).HeadAndTail()
	*head, *tail = C.double(hd), C.double(tl)
}

//export GoARADeactivate
func GoARADeactivate(h C.uintptr_t, kind C.int32_t, ref C.uintptr_t, deactivate C.ARABool) {
	defer araRecover()
	switch kind {
	case C.VST3GO_ARA_SOURCE:
		araObject[*ara.AudioSource](ref).SetDeactivated(deactivate != 0)
	case C.VST3GO_ARA_MODIFICATION:
		araObject[*ara.AudioModification](ref).SetDeactivated(deactivate != 0)
	}
}

//export GoARADestroyObject
func GoARADestroyObject(h C.uintptr_t, kind C.int32_t, ref C.uintptr_t) {
	defer araRecover()
	switch kind {
	case C.VST3GO_ARA_CONTEXT:
		araObject[*ara.MusicalContext](ref).Destroy()
	case C.VST3GO_ARA_SEQUENCE:
		araObject[*ara.RegionSequence](ref).Destroy()
	case C.VST3GO_ARA_SOURCE:
		araObject[*ara.AudioSource](ref).Destroy()
	case C.VST3GO_ARA_MODIFICATION:
		araObject[*ara.AudioModification](ref).Destroy()
	case C.VST3GO_ARA_REGION:
		araObject[*ara.PlaybackRegion](ref).Destroy()
	default:
		return
	}
	cgo.Handle(ref).Delete()
}

//export GoARAAnalysisIncomplete
func GoARAAnalysisIncomplete(h, ref C.uintptr_t, contentType C.int32_t) (incomplete C.ARABool) {
	defer araRecover()
	return araBool(araObject[*ara.AudioSource](ref).AnalysisIncomplete(ara.ContentType(contentType)))
}

//export GoARARequestAnalysis
func GoARARequestAnalysis(h, ref C.uintptr_t) {
	defer araRecover()
	araObject[*ara.AudioSource](ref).RequestAnalysis()
}

func araContentOf(kind C.int32_t, ref C.uintptr_t) *ara.Content {
	switch kind {
	case C.VST3GO_ARA_SOURCE:
		return araObject[*ara.AudioSource](ref).Content()
	case C.VST3GO_ARA_MODIFICATION:
		return araObject[*ara.AudioModification](ref).Content()
	case C.VST3GO_ARA_REGION:
		return araObject[*ara.PlaybackRegion](ref).Content()
	}
	return nil
}

//export GoARAContentAvailable
func GoARAContentAvailable(h C.uintptr_t, kind C.int32_t, ref C.uintptr_t, contentType C.int32_t) (available C.ARABool) {
	defer araRecover()
	return araBool(araContentOf(kind, ref).Has(ara.ContentType(contentType)))
}

//export GoARAContentGrade
func GoARAContentGrade(h C.uintptr_t, kind C.int32_t, ref C.uintptr_t, contentType C.int32_t) (grade C.int32_t) {
	defer araRecover()
	if c := araContentOf(kind, ref); c.Has(ara.ContentType(contentType)) {
		return C.int32_t(c.Grade)
	}
	return C.int32_t(ara.GradeInitial)
}

//export GoARACreateContentReader
func GoARACreateContentReader(h C.uintptr_t, kind C.int32_t, ref C.uintptr_t, contentType C.int32_t, timeRange *C.ARAContentTimeRange) (reader *C.VST3GoARAContentReader) {
	defer araRecover()
	content := araContentOf(kind, ref)
	t := ara.ContentType(contentType)
	if !content.Has(t) {
		return nil
	}
	if timeRange != nil {
		content = content.Range(ara.TimeRange{Start: float64(timeRange.start), Duration: float64(timeRange.duration)})
	}
	switch t {
	case ara.ContentNotes:
		// ARAContentNote has misaligned fields on x86 (see ara.h).
		r := araReader(len(content.Notes), C.sizeof_ARAContentNote)
		for i, n := range content.Notes {
			note := C.VST3GoARANote{
				frequency: C.float(n.Frequency),
				pitch:     C.int32_t(n.Pitch),
				volume:    C.float(n.Volume),
				start:     C.double(n.Start),
				attack:    C.double(n.Attack),
				duration:  C.double(n.Duration),
				signal:    C.double(n.SignalDuration),
			}
			C.vst3go_ara_set_note(r, C.int32_t(i), &note)
		}
		return r
	case ara.ContentTempoEntries:
		r := araReader(len(content.Tempo), C.sizeof_ARAContentTempoEntry)
		events := araEvents[C.ARAContentTempoEntry](r)
		for i, e := range content.Tempo {
			events[i] = C.ARAContentTempoEntry{timePosition: C.ARATimePosition(e.Time), quarterPosition: C.ARAQuarterPosition(e.Quarter)}
		}
		return r
	case ara.ContentBarSignatures:
		r := araReader(len(content.Bars), C.sizeof_ARAContentBarSignature)
		events := araEvents[C.ARAContentBarSignature](r)
		for i, b := range content.Bars {
			events[i] = C.ARAContentBarSignature{numerator: C.ARAInt32(b.Numerator), denominator: C.ARAInt32(b.Denominator), position: C.ARAQuarterPosition(b.Quarter)}
		}
		return r
	}
	return nil
}

// araReader allocates a content reader of count events of size bytes; the
// host frees it with destroyContentReader.
func araReader(count int, size C.size_t) *C.VST3GoARAContentReader {
	r := C.vst3go_ara_content_reader_new(C.int32_t(count), C.int32_t(size))
	if r == nil {
		panic("ara: out of memory")
	}
	return r
}

// araEvents returns the events of a reader of naturally aligned events E.
func araEvents[E any](r *C.VST3GoARAContentReader) []E {
	return unsafe.Slice((*E)(unsafe.Pointer(r.events)), int(r.count))
}

// Plug-in extension

//export GoARARendererEdit
func GoARARendererEdit(binding C.uintptr_t, op C.int32_t, ref C.uintptr_t) {
	defer araRecover()
	b := araObject[*ara.Binding](binding)
	switch op {
	case C.VST3GO_ARA_ADD_PLAYBACK_REGION:
		b.AddPlaybackRegion(araObject[*ara.PlaybackRegion](ref))
	case C.VST3GO_ARA_REMOVE_PLAYBACK_REGION:
		b.RemovePlaybackRegion(araObject[*ara.PlaybackRegion](ref))
	case C.VST3GO_ARA_ADD_EDITOR_REGION:
		b.AddEditorRegion(araObject[*ara.PlaybackRegion](ref))
	case C.VST3GO_ARA_REMOVE_EDITOR_REGION:
		b.RemoveEditorRegion(araObject[*ara.PlaybackRegion](ref))
	case C.VST3GO_ARA_ADD_EDITOR_SEQUENCE:
		b.AddEditorSequence(araObject[*ara.RegionSequence](ref))
	case C.VST3GO_ARA_REMOVE_EDITOR_SEQUENCE:
		b.RemoveEditorSequence(araObject[*ara.RegionSequence](ref))
	}
}

//export GoARASelection
func GoARASelection(binding C.uintptr_t, s *C.ARAViewSelection) {
	defer araRecover()
	var sel ara.Selection
	for _, ref := range araRefs(unsafe.Pointer(s.playbackRegionRefs), s.playbackRegionRefsCount) {
		sel.Regions = append(sel.Regions, araObject[*ara.PlaybackRegion](ref))
	}
	for _, ref := range araRefs(unsafe.Pointer(s.regionSequenceRefs), s.regionSequenceRefsCount) {
		sel.Sequences = append(sel.Sequences, araObject[*ara.RegionSequence](ref))
	}
	if s.timeRange != nil {
		sel.Range = &ara.TimeRange{Start: float64(s.timeRange.start), Duration: float64(s.timeRange.duration)}
	}
	araObject[*ara.Binding](binding).SetSelection(sel)
}

//export GoARAHideSequences
func GoARAHideSequences(binding C.uintptr_t, count C.size_t, refs *C.uintptr_t) {
	defer araRecover()
	var seqs []*ara.RegionSequence
	for _, ref := range araRefs(unsafe.Pointer(refs), C.ARASize(count)) {
		seqs = append(seqs, araObject[*ara.RegionSequence](ref))
	}
	araObject[*ara.Binding](binding).HideSequences(seqs)
}

func araRefs(list unsafe.Pointer, count C.ARASize) []C.uintptr_t {
	if list == nil || count == 0 {
		return nil
	}
	return unsafe.Slice((*C.uintptr_t)(list), int(count))
}

// Host side

// araHost is the ara.Host of a document, calling the host's interfaces.
type araHost struct{ c *C.VST3GoARAController }

func (h araHost) OpenAudio(s *ara.AudioSource) (ara.AudioReader, error) {
	r := C.vst3go_ara_audio_reader_new(h.c, C.uintptr_t(s.HostRef()))
	if r == nil {
		return nil, errors.New("ara: the host could not open the audio source")
	}
	return &araAudioReader{c: h.c, reader: r}, nil
}

// araAudioReader reads through a host audio reader into C memory, which
// the host writes, and copies the samples out.
type araAudioReader struct {
	c       *C.VST3GoARAController
	reader  unsafe.Pointer
	scratch *C.float
	size    int
}

func (r *araAudioReader) Read(position int64, buffers [][]float32) error {
	if len(buffers) == 0 {
		return nil
	}
	frames, channels := len(buffers[0]), len(buffers)
	if n := frames * channels; n > r.size {
		C.free(unsafe.Pointer(r.scratch))
		r.scratch = (*C.float)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.float(0)))))
		if r.scratch == nil {
			r.size = 0
			return errors.New("ara: out of memory")
		}
		r.size = n
	}
	if C.vst3go_ara_audio_read(r.c, r.reader, C.int64_t(position), C.int64_t(frames), C.int32_t(channels), r.scratch) == 0 {
		return errors.New("ara: the host could not read the audio source")
	}
	data := unsafe.Slice((*float32)(unsafe.Pointer(r.scratch)), frames*channels)
	for ch, b := range buffers {
		copy(b, data[ch*frames:(ch+1)*frames])
	}
	return nil
}

func (r *araAudioReader) Close() {
	C.vst3go_ara_audio_reader_free(r.c, r.reader)
	C.free(unsafe.Pointer(r.scratch))
	r.scratch, r.size = nil, 0
}

func (h araHost) MusicalContextContent(mc *ara.MusicalContext) (tempo []ara.TempoEntry, bars []ara.BarSignature) {
	ref := C.uintptr_t(mc.HostRef())
	if r := C.vst3go_ara_context_reader_new(h.c, ref, C.kARAContentTypeTempoEntries); r != nil {
		n := int(C.vst3go_ara_host_reader_count(h.c, r))
		tempo = make([]ara.TempoEntry, 0, n)
		for i := range n {
			e := (*C.ARAContentTempoEntry)(C.vst3go_ara_host_reader_event(h.c, r, C.int32_t(i)))
			tempo = append(tempo, ara.TempoEntry{Time: float64(e.timePosition), Quarter: float64(e.quarterPosition)})
		}
		C.vst3go_ara_host_reader_free(h.c, r)
	}
	if r := C.vst3go_ara_context_reader_new(h.c, ref, C.kARAContentTypeBarSignatures); r != nil {
		n := int(C.vst3go_ara_host_reader_count(h.c, r))
		bars = make([]ara.BarSignature, 0, n)
		for i := range n {
			e := (*C.ARAContentBarSignature)(C.vst3go_ara_host_reader_event(h.c, r, C.int32_t(i)))
			bars = append(bars, ara.BarSignature{Numerator: int32(e.numerator), Denominator: int32(e.denominator), Quarter: float64(e.position)})
		}
		C.vst3go_ara_host_reader_free(h.c, r)
	}
	return tempo, bars
}

func (h araHost) AnalysisProgress(s *ara.AudioSource, state ara.AnalysisState, value float32) {
	C.vst3go_ara_notify_progress(h.c, C.uintptr_t(s.HostRef()), C.int32_t(state), C.float(value))
}

func (h araHost) SourceContentChanged(s *ara.AudioSource, flags ara.ContentUpdateFlags) {
	C.vst3go_ara_notify_content(h.c, C.VST3GO_ARA_SOURCE, C.uintptr_t(s.HostRef()), C.int32_t(flags))
}

func (h araHost) ModificationContentChanged(m *ara.AudioModification, flags ara.ContentUpdateFlags) {
	C.vst3go_ara_notify_content(h.c, C.VST3GO_ARA_MODIFICATION, C.uintptr_t(m.HostRef()), C.int32_t(flags))
}

func (h araHost) RegionContentChanged(r *ara.PlaybackRegion, flags ara.ContentUpdateFlags) {
	C.vst3go_ara_notify_content(h.c, C.VST3GO_ARA_REGION, C.uintptr_t(r.HostRef()), C.int32_t(flags))
}

func (h araHost) DocumentDataChanged() { C.vst3go_ara_notify_document(h.c) }
