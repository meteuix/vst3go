package plugin

// #cgo CFLAGS: -I../../include
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/preset"
)

// editorSession is the open editor page of an instance, shared by the VST3
// and CLAP front ends: the platform view, messages to the page and the
// preset browser state.
type editorSession struct {
	mu         sync.RWMutex
	view       unsafe.Pointer // platform editor view (editor_view.h)
	presetName string         // preset last saved or loaded from the editor
	open       atomic.Bool    // view != nil, readable from the audio thread
}

func (s *editorSession) setEditorView(view unsafe.Pointer) {
	s.mu.Lock()
	s.view = view
	s.mu.Unlock()
	s.open.Store(view != nil)
}

func (s *editorSession) clearEditorView(view unsafe.Pointer) {
	s.mu.Lock()
	if s.view == view {
		s.view = nil
		s.open.Store(false)
	}
	s.mu.Unlock()
}

func (s *editorSession) notifyEditorParameterChanged(p *param.Parameter) {
	s.postToEditor(paramMessage(p))
}

// refreshEditor pushes every parameter value to an open editor, e.g. after
// a preset load.
func (s *editorSession) refreshEditor(params *param.Registry) {
	if !s.open.Load() {
		return
	}
	for _, p := range params.All() {
		s.notifyEditorParameterChanged(p)
	}
}

// postToEditor sends a JSON message to the open editor page, if any.
func (s *editorSession) postToEditor(message any) {
	if !s.open.Load() {
		return
	}
	if data, err := json.Marshal(message); err == nil {
		s.postRaw(data)
	}
}

// editorMessage is a message posted by the editor page.
type editorMessage struct {
	Type   string          `json:"type"`
	ID     uint32          `json:"id"`
	Value  float64         `json:"value"`
	Name   string          `json:"name"`
	Width  int             `json:"width"`
	Height int             `json:"height"`
	Data   json.RawMessage `json:"data"`
}

// handleEditorMessage serves a JSON message the page posted: parameter
// gestures ("begin-edit", "param-change", "end-edit"), preset browsing
// ("preset-list", "preset-save", "preset-load"), "resize", the open file
// dialog ("choose-file") and the custom messages of vst3go.send
// ("message").
func (s *editorSession) handleEditorMessage(c *componentImpl, host Host, data []byte) {
	var msg editorMessage
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "begin-edit":
		_ = c.BeginEdit(msg.ID)
	case "param-change":
		_ = c.EditParameter(msg.ID, msg.Value)
	case "end-edit":
		_ = c.EndEdit(msg.ID)
	case "preset-list", "preset-save", "preset-load":
		s.handlePresetMessage(c, host, msg.Type, msg.Name)
	case "resize":
		s.requestResize(c, msg.Width, msg.Height)
	case "choose-file":
		s.chooseFile(msg.ID, msg.Name, msg.Data)
	case "message":
		target := c.processor
		if c.controllerSide != nil {
			target = c.controllerSide
		}
		if receiver, ok := target.(EditorMessageProcessor); ok && msg.Name != "" {
			receiver.EditorMessage(msg.Name, msg.Data)
		}
	}
}

// requestResize asks the host to resize the editor (vst3go.resize); the
// host answers with a size change of the view.
func (s *editorSession) requestResize(c *componentImpl, width, height int) {
	if c.editor.FixedSize || width <= 0 || height <= 0 {
		return
	}
	width, height = c.editor.constrain(width, height)
	s.mu.RLock()
	view := s.view
	s.mu.RUnlock()
	if view != nil {
		C.VST3GoEditorViewRequestResize(view, C.int32_t(width), C.int32_t(height))
	}
}

// fileRequest is the data of a "choose-file" message (vst3go.chooseFile).
type fileRequest struct {
	Extensions []string `json:"extensions"`
}

// fileChosenMessage answers vst3go.chooseFile: the path, "" when the user
// cancelled, or the error that kept the dialog from opening.
type fileChosenMessage struct {
	Type  string `json:"type"` // "file-chosen"
	ID    uint32 `json:"id"`
	Path  string `json:"path"`
	Error string `json:"error,omitempty"`
}

// chooseFile shows the platform's open file dialog for the page; the
// answer comes back through GoEditorFileChosen.
func (s *editorSession) chooseFile(request uint32, title string, data json.RawMessage) {
	var req fileRequest
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &req); err != nil {
			s.postToEditor(fileChosenMessage{Type: "file-chosen", ID: request, Error: "choose-file: " + err.Error()})
			return
		}
	}
	for i, ext := range req.Extensions {
		ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
		if ext == "" || strings.ContainsAny(ext, ";/\\") {
			s.postToEditor(fileChosenMessage{Type: "file-chosen", ID: request, Error: fmt.Sprintf("choose-file: invalid extension %q", req.Extensions[i])})
			return
		}
		req.Extensions[i] = ext
	}
	s.mu.RLock()
	view := s.view
	s.mu.RUnlock()
	if view == nil {
		return
	}
	ctitle := C.CString(title)
	cexts := C.CString(strings.Join(req.Extensions, ";"))
	defer C.free(unsafe.Pointer(ctitle))
	defer C.free(unsafe.Pointer(cexts))
	C.VST3GoEditorViewChooseFile(view, C.uint32_t(request), ctitle, cexts)
}

// GoEditorFileChosen delivers the open file dialog's answer to the page.
//
//export GoEditorFileChosen
func GoEditorFileChosen(id C.uintptr_t, request C.uint32_t, path, errText *C.char) {
	_, session, _ := editorTarget(id)
	if session == nil {
		return
	}
	reply := fileChosenMessage{Type: "file-chosen", ID: uint32(request)}
	if path != nil {
		reply.Path = C.GoString(path)
	}
	if errText != nil {
		reply.Error = C.GoString(errText)
	}
	session.postToEditor(reply)
}

// postRaw sends an encoded message to the open editor page, if any.
func (s *editorSession) postRaw(data []byte) {
	s.mu.RLock()
	view := s.view
	s.mu.RUnlock()
	if view == nil {
		return
	}
	cjson := C.CString(string(data))
	defer C.free(unsafe.Pointer(cjson))
	C.VST3GoEditorViewPost(view, cjson)
}

// GoEditorSize reports the initial editor size to the platform views.
//
//export GoEditorSize
func GoEditorSize(id C.uintptr_t, width, height *C.int32_t) {
	if panel := panels.get(uintptr(id)); panel != nil {
		*width, *height = C.int32_t(panel.width), C.int32_t(panel.height)
		return
	}
	e := editorSpec(nil)
	if c, _, _ := editorTarget(id); c != nil {
		e = c.editor
	}
	*width, *height = C.int32_t(e.Width), C.int32_t(e.Height)
}

// GoEditorCanResize reports whether the host may resize the editor.
//
//export GoEditorCanResize
func GoEditorCanResize(id C.uintptr_t) C.int {
	if c, _, _ := editorTarget(id); c != nil && c.editor.FixedSize {
		return 0
	}
	return 1
}

// GoEditorConstrainSize clamps a host-proposed size to the editor's bounds.
//
//export GoEditorConstrainSize
func GoEditorConstrainSize(id C.uintptr_t, width, height *C.int32_t) {
	if c, _, _ := editorTarget(id); c != nil {
		w, h := c.editor.constrain(int(*width), int(*height))
		*width, *height = C.int32_t(w), C.int32_t(h)
	}
}

// Editor preset browser messages.

type editorPresetsMessage struct {
	Type    string   `json:"type"` // "presets"
	Presets []string `json:"presets"`
	Current string   `json:"current"`
	Error   string   `json:"error,omitempty"`
}

// handlePresetMessage serves the preset browser from the user preset folder
// and answers with the preset list.
func (s *editorSession) handlePresetMessage(c *componentImpl, host Host, kind, name string) {
	reply := editorPresetsMessage{Type: "presets"}
	dir, err := c.presetDir()
	if err == nil {
		switch kind {
		case "preset-save":
			var path string
			if path, err = preset.PathFor(dir, name); err == nil {
				err = host.SavePreset(path)
			}
		case "preset-load":
			var path string
			if path, err = preset.PathFor(dir, name); err == nil {
				err = host.LoadPreset(path)
			}
		}
		if err == nil && kind != "preset-list" {
			s.mu.Lock()
			s.presetName = name
			s.mu.Unlock()
		}
	}
	if err != nil {
		reply.Error = err.Error()
	}
	if dir != "" {
		presets, listErr := preset.List(dir)
		if listErr != nil && reply.Error == "" {
			reply.Error = listErr.Error()
		}
		for _, p := range presets {
			reply.Presets = append(reply.Presets, p.Name)
		}
	}
	s.mu.RLock()
	reply.Current = s.presetName
	s.mu.RUnlock()
	s.postToEditor(reply)
}

// editorTarget is the instance behind an editor view handle: a VST3
// component or a CLAP instance (they share one ID space).
func editorTarget(id C.uintptr_t) (*componentImpl, *editorSession, Host) {
	if w := getComponent(id); w != nil {
		return w.component, &w.editorSession, hostHandle{w}
	}
	if inst := getClap(id); inst != nil {
		return inst.c, &inst.editorSession, clapHost{inst}
	}
	return nil, nil, nil
}
