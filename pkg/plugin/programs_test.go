package plugin

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
)

type programProcessor struct {
	editorModelTestProcessor
	list  ProgramList
	units []Unit
}

func (p *programProcessor) ProgramList() ProgramList { return p.list }
func (p *programProcessor) Units() []Unit            { return p.units }

func newProgramComponent(t *testing.T) (*componentImpl, *param.Registry) {
	t.Helper()
	registry := param.NewRegistry()
	if err := registry.Add(
		param.New(1, "Cutoff").Range(0, 100).Default(50).Build(),
		param.New(2, "Drive").Range(0, 10).Default(0).Build(),
		param.New(3, "Meter").Range(0, 1).ReadOnly().Build(),
		param.New(4, "Bypass").Toggle().Bypass().Build(),
	); err != nil {
		t.Fatal(err)
	}
	processor := &programProcessor{
		editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()},
		list: ProgramList{Programs: []Program{
			{Name: "Init"},
			{Name: "Bright", Values: map[uint32]float64{1: 100}, PitchNames: map[int16]string{36: "Kick"}},
			{Name: "Dirty", Values: map[uint32]float64{2: 10}},
		}},
		units: []Unit{{ID: 0, Name: "Main"}, {ID: 1, ParentID: 0, Name: "Filter"}},
	}
	return newTestComponent(t, processor), registry
}

func TestProgramSelectorParameter(t *testing.T) {
	c, registry := newProgramComponent(t)
	selector, ok := registry.GetOK(ProgramParamID)
	if !ok {
		t.Fatal("program selector was not registered")
	}
	if selector.StepCount != 2 || selector.Flags&(param.IsList|param.IsProgramChange|param.CanAutomate) != param.IsList|param.IsProgramChange|param.CanAutomate {
		t.Fatalf("selector steps=%d flags=%#x", selector.StepCount, selector.Flags)
	}
	if got := selector.FormatValue(selector.Normalize(2)); got != "Dirty" {
		t.Fatalf("selector string = %q, want Dirty", got)
	}
	if v, err := selector.ParseValue("Bright"); err != nil || v != selector.Normalize(1) {
		t.Fatalf("parse Bright = %v, %v", v, err)
	}
	if c.programs.list.Name != "Factory Presets" {
		t.Fatalf("default list name = %q", c.programs.list.Name)
	}
}

func TestProgramSelectionAppliesValues(t *testing.T) {
	c, registry := newProgramComponent(t)
	selector := registry.Get(ProgramParamID)
	registry.Get(3).SetNormalized(0.7)
	registry.Get(4).SetNormalized(1)

	if err := c.SetParamNormalized(ProgramParamID, selector.Normalize(1)); err != nil {
		t.Fatal(err)
	}
	if got := registry.Get(1).GetPlain(); got != 100 {
		t.Fatalf("cutoff = %v, want the program value 100", got)
	}
	if got := registry.Get(2).GetPlain(); got != 0 {
		t.Fatalf("drive = %v, want its default 0", got)
	}
	if registry.Get(3).GetNormalized() != 0.7 || registry.Get(4).GetNormalized() != 1 {
		t.Fatal("programs must not touch read-only or bypass parameters")
	}

	// Re-sending the same selection keeps user edits.
	registry.Get(1).SetPlain(20)
	_ = c.SetParamNormalized(ProgramParamID, selector.Normalize(1))
	if got := registry.Get(1).GetPlain(); got != 20 {
		t.Fatalf("cutoff = %v after re-selecting the same program, want 20", got)
	}

	// Restoring a state with an edited program must not re-apply it.
	state, err := c.GetState()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetParamNormalized(ProgramParamID, selector.Normalize(2))
	if err := c.SetState(state); err != nil {
		t.Fatal(err)
	}
	if got := registry.Get(1).GetPlain(); got != 20 {
		t.Fatalf("cutoff = %v after state restore, want 20", got)
	}
	_ = c.SetParamNormalized(ProgramParamID, selector.Normalize(1))
	if got := registry.Get(1).GetPlain(); got != 20 {
		t.Fatalf("restored selection was re-applied: cutoff = %v", got)
	}
}

func TestProgramAppliesOnAudioThread(t *testing.T) {
	c, registry := newProgramComponent(t)
	ctx := process.NewContext(64, registry)

	c.applyProgram(ctx, 2, 10)
	changes := ctx.GetParameterChanges()
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want cutoff and drive", changes)
	}
	for _, change := range changes {
		if change.SampleOffset != 10 {
			t.Fatalf("change %+v not at the selection offset", change)
		}
	}
	if changes[1].ParamID != 2 || changes[1].Value != 1 {
		t.Fatalf("drive change = %+v, want normalized 1", changes[1])
	}

	ctx.ResetParameterChanges()
	c.applyProgram(ctx, 2, 0)
	if ctx.HasParameterChanges() {
		t.Fatal("an unchanged selection must not re-apply the program")
	}
}

func TestProgramDataRoundTrip(t *testing.T) {
	c, registry := newProgramComponent(t)
	data, err := c.programs.encode(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.programs.decode(2, data, registry); err != nil {
		t.Fatal(err)
	}
	if got := c.programs.valuesOf(2); got[1].normalized != 0 || got[0].normalized != 0.5 {
		t.Fatalf("program 2 after loading program 0 data = %+v", got)
	}
	if err := c.programs.decode(1, []byte("garbage"), registry); err != errProgramData {
		t.Fatalf("garbage decode error = %v", err)
	}
	if _, err := c.programs.encode(3); err != errInvalidArgument {
		t.Fatalf("out-of-range encode error = %v", err)
	}
}

func TestUnits(t *testing.T) {
	c, _ := newProgramComponent(t)
	units := c.units()
	if len(units) != 2 || units[0].Name != "Main" || units[0].ParentID != noParentUnitID || units[1].Name != "Filter" {
		t.Fatalf("units = %+v", units)
	}
}

func TestProgramReservedID(t *testing.T) {
	registry := param.NewRegistry()
	if err := registry.Add(param.New(ProgramParamID, "Clash").Build()); err != nil {
		t.Fatal(err)
	}
	processor := &programProcessor{
		editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()},
		list:                     ProgramList{Programs: []Program{{Name: "A"}}},
	}
	if _, err := newComponent(processor, frameworkplugin.Info{ID: "x"}); err == nil {
		t.Fatal("a plugin parameter using ProgramParamID must be rejected")
	}
	empty := &programProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}
	if _, err := newComponent(empty, frameworkplugin.Info{ID: "x"}); err == nil {
		t.Fatal("an empty program list must be rejected")
	}
}
