package main

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/midi"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

func TestTransposeKeepsNoteOffsPaired(t *testing.T) {
	p := plugintest.New(t, midiFxPlugin{})
	p.Set(paramTranspose, 7)
	p.Note(0, 60, 0.8, 10, 600) // note-off lands in the second block
	p.SendEvent(midi.SysEx([]byte{0xF0, 0x7D, 0x01, 0xF7}).At(20))
	if err := p.ProcessEvents(1024); err != nil {
		t.Fatal(err)
	}
	p.Set(paramTranspose, 0) // changing transpose must not orphan the note-off
	events := p.OutputEvents()
	if len(events) != 3 {
		t.Fatalf("events = %v", events)
	}
	on, sysex, off := events[0], events[1], events[2]
	if on.Type != midi.EventTypeNoteOn || on.Note != 67 || on.Offset != 10 {
		t.Fatalf("note-on = %v", on)
	}
	if sysex.Type != midi.EventTypeSysEx || len(sysex.Data) != 4 {
		t.Fatalf("sysex = %v", sysex)
	}
	if off.Type != midi.EventTypeNoteOff || off.Note != 67 || off.Offset != 610 {
		t.Fatalf("note-off = %v", off)
	}
}
