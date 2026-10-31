package plugin

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
)

func editorMeterInstance(t *testing.T) (*clapInstance, *process.Context) {
	t.Helper()
	r := param.NewRegistry()
	if err := r.Add(param.New(1, "Hold").Toggle().Build(),
		param.New(10, "Peak").Range(-120, 60).Default(-120).ReadOnly().Build()); err != nil {
		t.Fatal(err)
	}
	inst := &clapInstance{c: newTestComponent(t, &editorModelTestProcessor{params: r, buses: bus.NewEffectStereo()}),
		params: []*param.Parameter{r.Get(1)}} // AU/CLAP host list excludes meters.
	return inst, process.NewContext(128, r)
}

func TestCLAPMeterRefreshWithoutHostAutomation(t *testing.T) {
	inst, ctx := editorMeterInstance(t)
	inst.open.Store(true)
	ctx.BeginBlock(128)
	ctx.SetOutputParameter(10, .6)
	if !inst.publishProcessParameters(ctx) {
		t.Fatal("processor meter changed without host automation: missing main-thread editor refresh request")
	}
	if got := inst.c.params.Get(10).GetNormalized(); got != .6 {
		t.Fatalf("meter must be published before the refresh request: %v", got)
	}
	// More updates before the main thread runs coalesce into one request,
	// while the editor will read the newest value, not the first one.
	ctx.BeginBlock(128)
	ctx.SetOutputParameter(10, .7)
	if inst.publishProcessParameters(ctx) {
		t.Fatal("a second callback was requested while one was pending")
	}
	if got := inst.c.params.Get(10).GetNormalized(); got != .7 {
		t.Fatalf("coalescing lost the newest meter value: %v", got)
	}
	if !inst.editorPending.Swap(false) {
		t.Fatal("main thread has no pending refresh to consume")
	}
	ctx.BeginBlock(128)
	ctx.SetOutputParameter(10, .8)
	if !inst.publishProcessParameters(ctx) {
		t.Fatal("meter refresh did not resume after servicing the callback")
	}
}

func TestCLAPMeterClosedEditorAndAutomation(t *testing.T) {
	inst, ctx := editorMeterInstance(t)
	ctx.BeginBlock(128)
	ctx.SetOutputParameter(10, .5)
	if inst.publishProcessParameters(ctx) || inst.editorPending.Load() {
		t.Fatal("closed editor should not request GUI callbacks")
	}
	if got := inst.c.params.Get(10).GetNormalized(); got != .5 {
		t.Fatal("closed editor stopped publishing meter values")
	}
	inst.open.Store(true)
	ctx.BeginBlock(128)
	if inst.publishProcessParameters(ctx) {
		t.Fatal("block with no parameter updates requested a callback")
	}
	inst.hostChanged = true
	if !inst.publishProcessParameters(ctx) {
		t.Fatal("host automation no longer requests an editor refresh")
	}
}
