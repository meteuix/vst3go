package plugin

// #cgo CFLAGS: -I../../include
// #include "editor_view.h"
// #include <stdlib.h>
import "C"

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/preset"
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

// editorParamMessage tells the page that a parameter changed.
type editorParamMessage struct {
	Type       string  `json:"type"` // "param-change"
	ID         uint32  `json:"id"`
	Normalized float64 `json:"normalized"`
	Plain      float64 `json:"plain"`
}

func (s *editorSession) notifyEditorParameterChanged(id uint32, normalized, plain float64) {
	s.postToEditor(editorParamMessage{Type: "param-change", ID: id, Normalized: normalized, Plain: plain})
}

// refreshEditor pushes every parameter value to an open editor, e.g. after
// a preset load.
func (s *editorSession) refreshEditor(params *param.Registry) {
	if !s.open.Load() {
		return
	}
	for _, p := range params.All() {
		s.notifyEditorParameterChanged(p.ID, p.GetNormalized(), p.GetPlain())
	}
}

// postToEditor sends a JSON message to the open editor page, if any.
func (s *editorSession) postToEditor(message any) {
	s.mu.RLock()
	view := s.view
	s.mu.RUnlock()
	if view == nil {
		return
	}
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	cjson := C.CString(string(data))
	defer C.free(unsafe.Pointer(cjson))
	C.VST3GoEditorViewPost(view, cjson)
}

// editorMessage is a message posted by the editor page.
type editorMessage struct {
	Type  string  `json:"type"`
	ID    uint32  `json:"id"`
	Value float64 `json:"value"`
	Name  string  `json:"name"`
}

// handleEditorMessage serves a JSON message the page posted: parameter
// gestures ("begin-edit", "param-change", "end-edit") and preset browsing
// ("preset-list", "preset-save", "preset-load").
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
