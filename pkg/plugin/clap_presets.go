package plugin

// #cgo CFLAGS: -I../../include
// #include "clap_bridge.h"
// #include <stdlib.h>
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/preset"
)

// CLAP preset discovery: every plugin has a provider that declares its user
// preset folder (preset.UserDir, the folder the editor's preset browser and
// VST3 hosts use) and describes the .vstpreset files in it. Instances load
// them through the preset-load extension.

const (
	clapPresetLocationFile = 0      // CLAP_PRESET_DISCOVERY_LOCATION_FILE
	clapPresetUserContent  = 1 << 1 // CLAP_PRESET_DISCOVERY_IS_USER_CONTENT
)

// clapPresetMeta is what a provider reports about one preset file.
type clapPresetMeta struct {
	Name     string
	VST3ID   string            // the plugin's VST3 class ID as a UUID
	Features []string          // instrument, style and character tags
	Extra    map[string]string // other meta info attributes
	Modified int64             // Unix seconds
}

// clapPresetMetadata reads a preset file for the plugin. ok is false for a
// file of another plugin.
func clapPresetMetadata(info plugin.Info, path string) (meta clapPresetMeta, ok bool, err error) {
	f, err := preset.ReadFile(path)
	if err != nil {
		return meta, false, err
	}
	if f.ClassID != info.UID() {
		return meta, false, nil
	}
	meta.Name = strings.TrimSuffix(filepath.Base(path), preset.Extension)
	meta.VST3ID = uuidString(f.ClassID)
	if st, err := os.Stat(path); err == nil {
		meta.Modified = st.ModTime().Unix()
	}
	if len(f.MetaInfo) > 0 {
		attrs, err := preset.ParseMetaInfo(f.MetaInfo)
		if err != nil {
			return meta, false, err
		}
		for _, key := range []string{preset.AttrInstrument, preset.AttrStyle, preset.AttrCharacter} {
			for _, tag := range strings.Split(attrs[key], "|") {
				if tag = strings.TrimSpace(tag); tag != "" {
					meta.Features = append(meta.Features, tag)
				}
			}
			delete(attrs, key)
		}
		delete(attrs, preset.AttrMediaType)
		if len(attrs) > 0 {
			meta.Extra = attrs
		}
	}
	return meta, true, nil
}

// uuidString formats a VST3 class ID the way the CLAP universal plugin ID
// names VST3 plugins: "123e4567-e89b-12d3-a456-426614174000".
func uuidString(id [16]byte) string {
	s := strings.ToLower(preset.ClassIDString(id))
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

var (
	clapProvidersOnce sync.Once
	clapProviders     []*C.clap_preset_discovery_provider_descriptor_t
)

func clapProviderList() []*C.clap_preset_discovery_provider_descriptor_t {
	clapProvidersOnce.Do(func() {
		for i := range clapDescriptorList() {
			info := registeredPlugins[i].GetInfo()
			desc := (*C.clap_preset_discovery_provider_descriptor_t)(C.calloc(1, C.size_t(unsafe.Sizeof(C.clap_preset_discovery_provider_descriptor_t{}))))
			desc.clap_version = C.clap_version_t{major: C.CLAP_VERSION_MAJOR, minor: C.CLAP_VERSION_MINOR, revision: C.CLAP_VERSION_REVISION}
			desc.id = C.CString(info.ID + ".presets")
			desc.name = C.CString(info.Name + " presets")
			desc.vendor = clapDescriptorList()[i].vendor
			clapProviders = append(clapProviders, desc)
		}
	})
	return clapProviders
}

//export GoClapPresetProviderDescriptor
func GoClapPresetProviderDescriptor(index C.uint32_t) *C.clap_preset_discovery_provider_descriptor_t {
	list := clapProviderList()
	if int(index) >= len(list) {
		return nil
	}
	return list[index]
}

// GoClapPresetProviderInit declares the .vstpreset file type and the user
// preset folder, when it exists (hosts treat a missing location as an error).
//
//export GoClapPresetProviderInit
func GoClapPresetProviderInit(index C.uint32_t, indexer *C.clap_preset_discovery_indexer_t) C.bool {
	defer recoverPanic(nil)
	if int(index) >= len(registeredPlugins) {
		return false
	}
	info := registeredPlugins[index].GetInfo()
	name := C.CString("VST3 Preset")
	ext := C.CString(strings.TrimPrefix(preset.Extension, "."))
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(ext))
	if !C.vst3go_clap_declare_filetype(indexer, name, ext) {
		return false
	}
	dir, err := preset.UserDir(info.Vendor, info.Name)
	if err != nil {
		return true
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return true
	}
	location := C.CString(dir)
	locationName := C.CString("User presets")
	defer C.free(unsafe.Pointer(location))
	defer C.free(unsafe.Pointer(locationName))
	return C.vst3go_clap_declare_location(indexer, clapPresetUserContent, locationName, location)
}

//export GoClapPresetMetadata
func GoClapPresetMetadata(index C.uint32_t, kind C.uint32_t, location *C.char, receiver *C.clap_preset_discovery_metadata_receiver_t) (ok C.bool) {
	defer recoverPanic(nil)
	if int(index) >= len(registeredPlugins) || kind != clapPresetLocationFile || location == nil {
		return false
	}
	info := registeredPlugins[index].GetInfo()
	meta, ours, err := clapPresetMetadata(info, C.GoString(location))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		cmsg := C.CString(err.Error())
		defer C.free(unsafe.Pointer(cmsg))
		C.vst3go_clap_metadata_error(receiver, cmsg)
		return true
	}
	if !ours {
		return true
	}
	var strs []*C.char
	cstr := func(s string) *C.char {
		p := C.CString(s)
		strs = append(strs, p)
		return p
	}
	defer func() {
		for _, p := range strs {
			C.free(unsafe.Pointer(p))
		}
	}()
	if !C.vst3go_clap_metadata_begin(receiver, cstr(meta.Name)) {
		return true
	}
	C.vst3go_clap_metadata_plugin_id(receiver, cstr("clap"), cstr(info.ID))
	C.vst3go_clap_metadata_plugin_id(receiver, cstr("vst3"), cstr(meta.VST3ID))
	C.vst3go_clap_metadata_flags(receiver, clapPresetUserContent)
	if meta.Modified > 0 {
		C.vst3go_clap_metadata_timestamps(receiver, C.uint64_t(meta.Modified))
	}
	for _, f := range meta.Features {
		C.vst3go_clap_metadata_feature(receiver, cstr(f))
	}
	for _, key := range slices.Sorted(maps.Keys(meta.Extra)) {
		C.vst3go_clap_metadata_extra(receiver, cstr(key), cstr(meta.Extra[key]))
	}
	return true
}

// GoClapPresetLoad loads a preset file into an instance (preset-load).
//
//export GoClapPresetLoad
func GoClapPresetLoad(id C.uintptr_t, kind C.uint32_t, location, loadKey *C.char) C.bool {
	defer recoverPanic(nil)
	inst := getClap(id)
	if inst == nil {
		return false
	}
	err := errInvalidArgument
	path := ""
	if kind == clapPresetLocationFile && location != nil {
		path = C.GoString(location)
		err = clapHost{inst}.LoadPreset(path)
	}
	if err != nil {
		cmsg := C.CString(fmt.Sprintf("load preset %q: %v", path, err))
		defer C.free(unsafe.Pointer(cmsg))
		C.vst3go_clap_preset_error(inst.plugin, kind, location, loadKey, cmsg)
		return false
	}
	inst.showLoadedPreset(path)
	C.vst3go_clap_preset_loaded(inst.plugin, kind, location, loadKey)
	return true
}

// showLoadedPreset makes a preset of the user folder that the host loaded
// the current preset of the editor's preset browser.
func (inst *clapInstance) showLoadedPreset(path string) {
	dir, err := inst.c.presetDir()
	if err != nil {
		return
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	inst.mu.Lock()
	inst.presetName = filepath.ToSlash(strings.TrimSuffix(rel, preset.Extension))
	inst.mu.Unlock()
	if inst.open.Load() {
		inst.handlePresetMessage(inst.c, clapHost{inst}, "preset-list", "")
	}
}
