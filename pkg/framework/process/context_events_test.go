package process

import (
	"testing"

	"github.com/th-release/vst3go/v2/pkg/framework/param"
	"github.com/th-release/vst3go/v2/pkg/midi"
)

func TestContextEventProcessing(t *testing.T) {
	registry := param.NewRegistry()
	ctx := NewContext(512, registry)

	// Set up input/output buffers first
	ctx.Input = [][]float32{make([]float32, 512)}
	ctx.Output = [][]float32{make([]float32, 512)}

	// Test adding input events
	noteOn := midi.NoteOn(0, 60, 0.8).At(100)
	noteOff := midi.NoteOff(0, 60, 0).At(200)

	ctx.AddInputEvent(noteOn)
	ctx.AddInputEvent(noteOff)

	// Test getting all input events
	events := ctx.GetAllInputEvents()
	if len(events) != 2 {
		t.Errorf("Expected 2 input events, got %d", len(events))
	}

	// Test HasInputEvents
	if !ctx.HasInputEvents() {
		t.Error("Expected HasInputEvents to return true")
	}

	// Test getting events in range
	events = ctx.GetInputEvents(0, 150)
	if len(events) != 1 {
		t.Errorf("Expected 1 event in range [0, 150), got %d", len(events))
	}

	events = ctx.GetInputEvents(150, 300)
	if len(events) != 1 {
		t.Errorf("Expected 1 event in range [150, 300), got %d", len(events))
	}

	// Test clearing input events
	ctx.ClearInputEvents()
	if ctx.HasInputEvents() {
		t.Error("Expected no input events after clear")
	}
}

func TestContextOutputEvents(t *testing.T) {
	registry := param.NewRegistry()
	ctx := NewContext(512, registry)

	// Add output events
	cc := midi.ControlChange(0, midi.CCVolume, 0.8).At(50)
	pitchBend := midi.PitchBend(0, 0.1).At(75)

	ctx.AddOutputEvent(cc)
	ctx.AddOutputEvent(pitchBend)

	// Get output events
	outputEvents := ctx.GetOutputEvents()
	if len(outputEvents) != 2 {
		t.Errorf("Expected 2 output events, got %d", len(outputEvents))
	}

	// Clear output events
	ctx.ClearOutputEvents()
	outputEvents = ctx.GetOutputEvents()
	if len(outputEvents) != 0 {
		t.Error("Expected no output events after clear")
	}
}

type testProcessor struct {
	receivedEvents []midi.Event
}

func (p *testProcessor) ProcessEvent(event midi.Event) {
	p.receivedEvents = append(p.receivedEvents, event)
}

func TestContextProcessEvents(t *testing.T) {
	registry := param.NewRegistry()
	ctx := NewContext(512, registry)
	ctx.Input = [][]float32{make([]float32, 512)}

	// Add events at different offsets
	events := []midi.Event{
		midi.NoteOn(0, 60, 0.7874).At(50),
		midi.NoteOn(0, 61, 0.7874).At(150),
		midi.NoteOn(0, 62, 0.7874).At(250),
		midi.NoteOn(0, 63, 0.7874).At(350),
	}

	for _, e := range events {
		ctx.AddInputEvent(e)
	}

	// Process events in chunks
	processor := &testProcessor{}

	// Process first chunk [0, 200)
	ctx.ProcessEvents(processor, 0, 200)
	if len(processor.receivedEvents) != 2 {
		t.Errorf("Expected 2 events in first chunk, got %d", len(processor.receivedEvents))
	}

	// Process second chunk [200, 400)
	processor.receivedEvents = nil
	ctx.ProcessEvents(processor, 200, 400)
	if len(processor.receivedEvents) != 2 {
		t.Errorf("Expected 2 events in second chunk, got %d", len(processor.receivedEvents))
	}
}

func TestContextClearAllEvents(t *testing.T) {
	registry := param.NewRegistry()
	ctx := NewContext(512, registry)

	// Add both input and output events
	ctx.AddInputEvent(midi.NoteOn(0, 60, 0.7874).At(100))
	ctx.AddOutputEvent(midi.ControlChange(0, midi.CCVolume, 0.7874).At(50))

	// Clear all events
	ctx.ClearAllEvents()

	if ctx.HasInputEvents() {
		t.Error("Expected no input events after ClearAllEvents")
	}

	if len(ctx.GetOutputEvents()) != 0 {
		t.Error("Expected no output events after ClearAllEvents")
	}
}

func TestContextParameterAccessorsHandleMissingParameters(t *testing.T) {
	ctx := NewContext(128, param.NewRegistry())

	if got := ctx.Param(999); got != 0 {
		t.Fatalf("Param(999) = %f, want 0", got)
	}

	if got := ctx.ParamPlain(999); got != 0 {
		t.Fatalf("ParamPlain(999) = %f, want 0", got)
	}
}

func TestContextAddParameterChangeGrowsPastInitialCapacity(t *testing.T) {
	ctx := NewContext(32, param.NewRegistry())

	const changeCount = 300
	for i := 0; i < changeCount; i++ {
		ctx.AddParameterChange(uint32(i+1), float64(i)/100.0, i)
	}

	changes := ctx.GetParameterChanges()
	if len(changes) != changeCount {
		t.Fatalf("GetParameterChanges() len = %d, want %d", len(changes), changeCount)
	}

	first := changes[0]
	last := changes[len(changes)-1]
	if first.ParamID != 1 || first.SampleOffset != 0 {
		t.Fatalf("first change = %+v, want ParamID=1 SampleOffset=0", first)
	}
	if last.ParamID != changeCount || last.SampleOffset != changeCount-1 {
		t.Fatalf("last change = %+v, want ParamID=%d SampleOffset=%d", last, changeCount, changeCount-1)
	}
}

func TestContextParameterChangeCapacityTracksBlockSize(t *testing.T) {
	ctx := NewContext(512, param.NewRegistry())
	if got := len(ctx.paramChanges); got < 512 {
		t.Fatalf("len(paramChanges) = %d, want at least 512", got)
	}
}
