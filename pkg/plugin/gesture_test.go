package plugin

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

func gestureComponent(t *testing.T) *componentImpl {
	t.Helper()
	registry := param.NewRegistry()
	if err := registry.Add(param.New(1, "Cutoff").Range(0, 1).Build()); err != nil {
		t.Fatal(err)
	}
	return newTestComponent(t, &editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()})
}

func TestEditGestures(t *testing.T) {
	c := gestureComponent(t)
	if err := c.BeginEdit(99); err == nil {
		t.Fatal("BeginEdit on an unknown parameter must fail")
	}
	if err := c.BeginEdit(1); err != nil || !c.inGesture(1) {
		t.Fatalf("BeginEdit: err=%v open=%v", err, c.inGesture(1))
	}
	for _, v := range []float64{0.2, 0.4, 1.5} {
		if err := c.EditParameter(1, v); err != nil {
			t.Fatal(err)
		}
		if !c.inGesture(1) {
			t.Fatal("EditParameter inside a gesture must keep it open")
		}
	}
	if got := c.params.Get(1).GetNormalized(); got != 1 {
		t.Fatalf("value = %v, want clamped 1", got)
	}
	if err := c.EndEdit(1); err != nil || c.inGesture(1) {
		t.Fatalf("EndEdit: err=%v open=%v", err, c.inGesture(1))
	}
	if err := c.EndEdit(1); err != nil {
		t.Fatal("EndEdit without an open gesture is a no-op")
	}
	// Outside a gesture an edit is complete and leaves none open.
	if err := c.EditParameter(1, 0.5); err != nil || c.inGesture(1) {
		t.Fatalf("standalone edit: err=%v open=%v", err, c.inGesture(1))
	}
	c.BeginEdit(1)
	c.endGestures()
	if c.inGesture(1) {
		t.Fatal("endGestures must close open gestures")
	}
}

func TestEditorMessages(t *testing.T) {
	c := gestureComponent(t)
	w := &componentWrapper{component: c}
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"begin-edit","id":1}`))
	if !c.inGesture(1) {
		t.Fatal("begin-edit must open a gesture")
	}
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"param-change","id":1,"value":0.25}`))
	if got := c.params.Get(1).GetNormalized(); got != 0.25 {
		t.Fatalf("param-change value = %v", got)
	}
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"end-edit","id":1}`))
	if c.inGesture(1) {
		t.Fatal("end-edit must close the gesture")
	}
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`not json`))
	w.handleEditorMessage(w.component, hostHandle{w}, []byte(`{"type":"param-change","id":42,"value":1}`))
}

func TestForwardedEditOps(t *testing.T) {
	c := gestureComponent(t)
	if err := c.edit(editBegin, 1, 0); err != nil || !c.inGesture(1) {
		t.Fatal("editBegin")
	}
	if err := c.edit(editPerform, 1, 0.75); err != nil || c.params.Get(1).GetNormalized() != 0.75 {
		t.Fatal("editPerform")
	}
	if err := c.edit(editEnd, 1, 0); err != nil || c.inGesture(1) {
		t.Fatal("editEnd")
	}
}
