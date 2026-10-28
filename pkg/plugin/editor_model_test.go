package plugin

import (
	"testing"

	frameworkbus "github.com/th-release/vst3go/pkg/framework/bus"
	frameworkparam "github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
)

type editorModelTestProcessor struct {
	params *frameworkparam.Registry
	buses  *frameworkbus.Configuration
}

func (p *editorModelTestProcessor) Initialize(float64, int32) error { return nil }

func (p *editorModelTestProcessor) ProcessAudio(*process.Context) {}

func (p *editorModelTestProcessor) GetParameters() *frameworkparam.Registry { return p.params }

func (p *editorModelTestProcessor) GetBuses() *frameworkbus.Configuration { return p.buses }

func (p *editorModelTestProcessor) SetActive(bool) error { return nil }

func (p *editorModelTestProcessor) GetLatencySamples() int32 { return 0 }

func (p *editorModelTestProcessor) GetTailSamples() int32 { return 0 }

var _ Processor = (*editorModelTestProcessor)(nil)

func TestBuildEditorModel(t *testing.T) {
	registry := frameworkparam.NewRegistry()

	normal := frameworkparam.New(1, "Gain").Range(-24, 24).Default(0).Build()
	bypass := frameworkparam.New(2, "Bypass").Toggle().Bypass().Build()
	meter := frameworkparam.New(3, "Output").Range(0, 1).ReadOnly().Build()
	hidden := frameworkparam.New(4, "Hidden").Range(0, 1).Hidden().Build()

	if err := registry.Add(normal, bypass, meter, hidden); err != nil {
		t.Fatalf("registry.Add() failed: %v", err)
	}

	model, err := BuildEditorModel(frameworkplugin.Info{
		ID:       "com.example.editor",
		Name:     "Example Editor",
		Version:  "1.2.3",
		Vendor:   "Example Audio",
		Category: "Fx",
	}, registry)
	if err != nil {
		t.Fatalf("BuildEditorModel() failed: %v", err)
	}

	if model.Plugin.Name != "Example Editor" {
		t.Fatalf("model.Plugin.Name = %q, want %q", model.Plugin.Name, "Example Editor")
	}
	if len(model.Sections) != 1 {
		t.Fatalf("len(model.Sections) = %d, want 1", len(model.Sections))
	}

	controls := model.Sections[0].Controls
	if len(controls) != 4 {
		t.Fatalf("len(controls) = %d, want 4", len(controls))
	}

	if controls[0].Kind != "slider" {
		t.Fatalf("Gain control kind = %q, want %q", controls[0].Kind, "slider")
	}
	if controls[1].Kind != "toggle" {
		t.Fatalf("Bypass control kind = %q, want %q", controls[1].Kind, "toggle")
	}
	if controls[2].Kind != "meter" {
		t.Fatalf("Output control kind = %q, want %q", controls[2].Kind, "meter")
	}
	if !controls[2].ReadOnly {
		t.Fatal("Output control should be read-only")
	}
	if !controls[3].Hidden {
		t.Fatal("Hidden control should be hidden")
	}
}

func TestEditorModelReflectsParameterChanges(t *testing.T) {
	registry := frameworkparam.NewRegistry()
	gain := frameworkparam.New(1, "Gain").Range(-24, 24).Default(0).Build()

	if err := registry.Add(gain); err != nil {
		t.Fatalf("registry.Add() failed: %v", err)
	}

	component, err := newComponent(&editorModelTestProcessor{
		params: registry,
		buses:  frameworkbus.Stereo(),
	}, frameworkplugin.Info{
		ID:      "com.example.editor",
		Name:    "Example Editor",
		Version: "1.2.3",
	})
	if err != nil {
		t.Fatalf("newComponent() failed: %v", err)
	}

	if err := component.EditParameter(1, 0.75); err != nil {
		t.Fatalf("EditParameter() failed: %v", err)
	}

	model, err := component.EditorModel()
	if err != nil {
		t.Fatalf("EditorModel() failed: %v", err)
	}

	if got := model.Sections[0].Controls[0].Normalized; got != 0.75 {
		t.Fatalf("normalized = %v, want %v", got, 0.75)
	}
	if got := model.Sections[0].Controls[0].Plain; got != gain.Denormalize(0.75) {
		t.Fatalf("plain = %v, want %v", got, gain.Denormalize(0.75))
	}
}

func TestEditorModelNilRegistry(t *testing.T) {
	if _, err := BuildEditorModel(frameworkplugin.Info{}, nil); err == nil {
		t.Fatal("BuildEditorModel() should fail for a nil registry")
	}
}

func TestEditorSnapshotApply(t *testing.T) {
	registry := frameworkparam.NewRegistry()
	gain := frameworkparam.New(1, "Gain").Range(-24, 24).Default(0).Build()
	if err := registry.Add(gain); err != nil {
		t.Fatalf("registry.Add() failed: %v", err)
	}

	snapshot, err := BuildEditorSnapshot(frameworkplugin.Info{
		ID:   "com.example.editor",
		Name: "Example Editor",
	}, registry)
	if err != nil {
		t.Fatalf("BuildEditorSnapshot() failed: %v", err)
	}

	snapshot.Model.Sections[0].Controls[0].Normalized = 0.9
	snapshot.Model.Sections[0].Controls[0].Plain = gain.Denormalize(0.9)

	if err := snapshot.Apply(registry); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	if got := gain.GetNormalized(); got != 0.9 {
		t.Fatalf("gain.GetNormalized() = %v, want %v", got, 0.9)
	}
}
