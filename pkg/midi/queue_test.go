package midi

import "testing"

func TestEventQueue(t *testing.T) {
	q := NewEventQueue()
	if !q.IsEmpty() {
		t.Fatal("new queue should be empty")
	}

	q.Add(NoteOn(0, 62, 1).At(300))
	q.Add(NoteOn(0, 60, 1).At(100))
	q.AddMultiple([]Event{NoteOff(0, 60, 0).At(200), ControlChange(0, CCSustain, 1).At(100)})
	if q.Size() != 4 {
		t.Fatalf("Size = %d, want 4", q.Size())
	}

	all := q.GetAllEvents()
	for i, want := range []int32{100, 100, 200, 300} {
		if all[i].Offset != want {
			t.Fatalf("event %d offset = %d, want %d", i, all[i].Offset, want)
		}
	}
	// Stable sort keeps insertion order for equal offsets.
	if all[0].Type != EventTypeNoteOn || all[1].Type != EventTypeControlChange {
		t.Fatalf("equal offsets reordered: %v %v", all[0].Type, all[1].Type)
	}

	if got := q.GetEventsInRange(100, 300); len(got) != 3 {
		t.Fatalf("GetEventsInRange = %d events, want 3", len(got))
	}
	if got := q.GetEventsInRange(400, 500); got != nil {
		t.Fatalf("empty range returned %v", got)
	}

	q.RemoveProcessedEvents(150)
	if q.Size() != 2 {
		t.Fatalf("after RemoveProcessedEvents Size = %d, want 2", q.Size())
	}

	q.OffsetEvents(-200)
	if got := q.GetAllEvents(); got[0].Offset != 0 || got[1].Offset != 100 {
		t.Fatalf("OffsetEvents = %v", got)
	}

	q.Clear()
	if !q.IsEmpty() {
		t.Fatal("Clear left events")
	}
}

type recorder struct{ events []Event }

func (r *recorder) ProcessEvent(e Event) { r.events = append(r.events, e) }

func TestEventBuffer(t *testing.T) {
	b := NewEventBuffer()
	b.AddInputEvent(NoteOn(0, 60, 1).At(10))
	b.AddInputEvent(NoteOn(0, 61, 1).At(20))
	b.AddOutputEvent(NoteOff(0, 60, 0))

	if got := b.GetInputEvents(0, 15); len(got) != 1 || got[0].Note != 60 {
		t.Fatalf("GetInputEvents = %v", got)
	}
	if got := b.GetOutputEvents(); len(got) != 1 {
		t.Fatalf("GetOutputEvents = %v", got)
	}

	var r recorder
	b.inputQueue.ProcessEvents(&r, 0, 100)
	if len(r.events) != 2 {
		t.Fatalf("ProcessEvents delivered %d events", len(r.events))
	}

	b.ClearAll()
	if len(b.GetOutputEvents()) != 0 || len(b.GetInputEvents(0, 100)) != 0 {
		t.Fatal("ClearAll left events")
	}
}
