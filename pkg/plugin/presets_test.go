package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/preset"
)

func presetWrapper(t *testing.T, id string) *componentWrapper {
	t.Helper()
	registry := param.NewRegistry()
	if err := registry.Add(param.New(1, "Cutoff").Range(20, 20000).Default(1000).Build()); err != nil {
		t.Fatal(err)
	}
	c, err := newComponent(&editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()},
		frameworkplugin.Info{ID: id, Name: "Test Synth", Vendor: "Acme", Category: frameworkplugin.CategoryInstrumentSynth})
	if err != nil {
		t.Fatal(err)
	}
	return &componentWrapper{component: c, kind: kindSingle}
}

func TestSaveAndLoadPreset(t *testing.T) {
	w := presetWrapper(t, "com.example.synth")
	host := hostHandle{w}
	cutoff := w.component.params.Get(1)
	cutoff.SetPlain(440)

	path := filepath.Join(t.TempDir(), "sub", "A440"+preset.Extension)
	if err := host.SavePreset(path); err != nil {
		t.Fatal(err)
	}
	f, err := preset.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := preset.ParseMetaInfo(f.MetaInfo)
	if err != nil || meta[preset.AttrPlugInName] != "Test Synth" || meta[preset.AttrPlugInCategory] != "Instrument|Synth" {
		t.Fatalf("meta info = %v, %v", meta, err)
	}
	if f.ClassID != w.component.pluginInfo.UID() {
		t.Fatal("preset is not tagged with the processor class ID")
	}

	cutoff.SetPlain(5000)
	if err := host.LoadPreset(path); err != nil {
		t.Fatal(err)
	}
	if got := cutoff.GetPlain(); got < 439.9 || got > 440.1 {
		t.Fatalf("cutoff after load = %v, want 440", got)
	}

	other := presetWrapper(t, "com.example.other")
	if err := (hostHandle{other}).LoadPreset(path); !errors.Is(err, errForeignPreset) {
		t.Fatalf("foreign preset: err = %v", err)
	}
	if err := host.LoadPreset(filepath.Join(t.TempDir(), "missing.vstpreset")); err == nil {
		t.Fatal("missing preset loaded")
	}
}

func TestEditorPresetMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	w := presetWrapper(t, "com.example.synth")
	cutoff := w.component.params.Get(1)

	cutoff.SetPlain(300)
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"preset-save","name":"Bass/Deep"}`))
	dir, _ := preset.UserDir("Acme", "Test Synth")
	if _, err := os.Stat(filepath.Join(dir, "Bass", "Deep"+preset.Extension)); err != nil {
		t.Fatalf("preset not saved in the user folder: %v", err)
	}
	if w.currentPreset() != "Bass/Deep" {
		t.Fatalf("current preset = %q", w.currentPreset())
	}

	cutoff.SetPlain(9000)
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"preset-load","name":"Bass/Deep"}`))
	if got := cutoff.GetPlain(); got < 299.9 || got > 300.1 {
		t.Fatalf("cutoff after editor load = %v", got)
	}

	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"preset-save","name":"../escape"}`))
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape"+preset.Extension)); err == nil {
		t.Fatal("preset name escaped the preset folder")
	}
	if w.currentPreset() != "Bass/Deep" {
		t.Fatal("a failed save must not change the current preset")
	}
}

func (s *editorSession) currentPreset() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.presetName
}
