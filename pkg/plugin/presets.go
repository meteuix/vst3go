package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/th-release/vst3go/pkg/preset"
)

// messageState carries a component state loaded from a preset to the other
// side of a distributed plugin.
const messageState = messagePrefix + "state"

// errForeignPreset reports a preset saved by another plugin.
var errForeignPreset = errors.New("preset belongs to another plugin")

// presetFile captures the current state as a preset.
func (c *componentImpl) presetFile() (*preset.File, error) {
	state, err := c.GetState()
	if err != nil {
		return nil, err
	}
	meta := preset.MetaInfo{
		preset.AttrPlugInName:     c.pluginInfo.Name,
		preset.AttrPlugInCategory: c.pluginInfo.SubCategories(),
	}
	return &preset.File{ClassID: c.pluginInfo.UID(), Component: state, MetaInfo: meta.Encode()}, nil
}

// presetDir is the user preset folder of the plugin.
func (c *componentImpl) presetDir() (string, error) {
	return preset.UserDir(c.pluginInfo.Vendor, c.pluginInfo.Name)
}

// savePreset writes the current state to a .vstpreset file.
func (c *componentImpl) savePreset(path string) error {
	f, err := c.presetFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return preset.WriteFile(path, f)
}

// readPreset returns the component state of a .vstpreset file saved for
// this plugin.
func (c *componentImpl) readPreset(path string) ([]byte, error) {
	f, err := preset.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if f.ClassID != c.pluginInfo.UID() {
		return nil, fmt.Errorf("%s: %w", path, errForeignPreset)
	}
	return f.Component, nil
}

func (h hostHandle) SavePreset(path string) error { return h.w.component.savePreset(path) }

func (h hostHandle) LoadPreset(path string) error {
	state, err := h.w.component.readPreset(path)
	if err != nil {
		return err
	}
	return h.w.applyState(state, true)
}

// applyState restores a component state outside the host's setState: it
// updates this side, the other side of a distributed plugin (when
// propagate is set), the host's view of the parameters and the editor.
func (w *componentWrapper) applyState(state []byte, propagate bool) error {
	if err := w.component.SetState(state); err != nil {
		return err
	}
	if propagate && w.kind != kindSingle {
		if err := w.sendMessage(messageState, state); err != nil {
			return err
		}
	}
	if w.kind != kindProcessor {
		// Without a connected host there is nothing to refresh.
		_ = hostHandle{w}.RestartComponent(RestartParamValuesChanged)
		w.refreshEditorParameters()
	}
	return nil
}

// Editor preset browser messages.

type editorPresetsMessage struct {
	Type    string   `json:"type"` // "presets"
	Presets []string `json:"presets"`
	Current string   `json:"current"`
	Error   string   `json:"error,omitempty"`
}

// handlePresetMessage serves "preset-list", "preset-save" and
// "preset-load" from the editor page with the user preset folder, and
// answers with the preset list.
func (w *componentWrapper) handlePresetMessage(kind, name string) {
	reply := editorPresetsMessage{Type: "presets"}
	dir, err := w.component.presetDir()
	if err == nil {
		switch kind {
		case "preset-save":
			var path string
			if path, err = preset.PathFor(dir, name); err == nil {
				err = hostHandle{w}.SavePreset(path)
			}
		case "preset-load":
			var path string
			if path, err = preset.PathFor(dir, name); err == nil {
				err = hostHandle{w}.LoadPreset(path)
			}
		}
		if err == nil && kind != "preset-list" {
			w.setCurrentPreset(name)
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
	reply.Current = w.currentPreset()
	w.postToEditor(reply)
}

func (w *componentWrapper) setCurrentPreset(name string) {
	w.handlerMu.Lock()
	w.presetName = name
	w.handlerMu.Unlock()
}

func (w *componentWrapper) currentPreset() string {
	w.handlerMu.RLock()
	defer w.handlerMu.RUnlock()
	return w.presetName
}
