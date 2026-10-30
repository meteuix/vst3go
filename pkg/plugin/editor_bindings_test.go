package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
)

type bindingsPlugin struct{}

func (bindingsPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.example.bindings", Name: "Bindings"}
}

func (bindingsPlugin) CreateProcessor() Processor {
	registry := param.NewRegistry()
	registry.Add(param.New(1, "Cutoff Freq").Range(20, 20000).Default(1000).Unit("Hz").Build())
	registry.Add(param.New(2, "2nd Osc").Range(0, 1).Build())
	registry.Add(param.New(3, "cutoff-freq").Range(0, 1).Build())
	registry.Add(param.Choice(4, "Mode", []param.ChoiceOption{{Value: 0, Name: "A"}, {Value: 1, Name: "B"}, {Value: 2, Name: "C"}}).Build())
	registry.Add(param.New(5, "Hidden").Flags(param.IsHidden).Build())
	registry.Add(param.New(6, `Say "hi"`).Range(-1, 1).Build())
	return &editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()}
}

func TestEditorBindings(t *testing.T) {
	got, err := EditorBindings(bindingsPlugin{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`cutoffFreq: { id: 1, name: "Cutoff Freq", unit: "Hz", kind: "slider", min: 20, max: 20000, defaultValue: 1000, stepCount: 0 },`,
		`p2ndOsc: { id: 2,`,
		`cutoffFreq3: { id: 3,`,
		`mode: { id: 4, name: "Mode", unit: "", kind: "choice", min: 0, max: 2, defaultValue: 0, stepCount: 2 },`,
		`sayHi: { id: 6, name: "Say \"hi\"",`,
		"export type ParamKey = keyof typeof params;",
		"export function param(key: ParamKey): Vst3goParam {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bindings lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"Hidden"`) {
		t.Error("hidden parameters must be left out")
	}

	dir := filepath.Join(t.TempDir(), "ui")
	if err := WriteEditorBindings(bindingsPlugin{}, dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"params.ts": got, "vst3go.d.ts": EditorSDKTypes()} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != want {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBindingKey(t *testing.T) {
	for name, want := range map[string]string{"Gain": "gain", "Cutoff Freq": "cutoffFreq", "LFO 1 Rate": "lFO1Rate", "3 Band": "p3Band", "": "param", "Ü": "param", "a_b": "aB"} {
		if got := bindingKey(name); got != want {
			t.Errorf("bindingKey(%q) = %q, want %q", name, got, want)
		}
	}
}
