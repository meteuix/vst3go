package plugin

import (
	"testing"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/midi"
)

type flexibleProcessor struct {
	editorModelTestProcessor
	accepted [][]bus.SpeakerArrangement
}

func (p *flexibleProcessor) AcceptBusArrangements(inputs, outputs []bus.SpeakerArrangement) bool {
	p.accepted = append(p.accepted, inputs, outputs)
	return inputs[0] == bus.ArrangementMono && outputs[0] == bus.ArrangementMono
}

type controllerProcessor struct {
	editorModelTestProcessor
	channels int
	mapped   map[int16]uint32
}

func (p *controllerProcessor) MIDIControllerChannels() int { return p.channels }

func (p *controllerProcessor) MIDIControllerParameter(busIndex int32, channel, controller int16) (uint32, bool) {
	id, ok := p.mapped[controller]
	return id, ok && busIndex == 0 && channel == 0
}

func newTestComponent(t *testing.T, processor Processor) *componentImpl {
	t.Helper()
	c, err := newComponent(processor, frameworkplugin.Info{ID: "com.example.test", Name: "Test"})
	if err != nil {
		t.Fatalf("newComponent() failed: %v", err)
	}
	return c
}

func TestSetBusArrangements(t *testing.T) {
	fixed := newTestComponent(t, &editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()})
	stereo := []bus.SpeakerArrangement{bus.ArrangementStereo}
	mono := []bus.SpeakerArrangement{bus.ArrangementMono}

	if !fixed.SetBusArrangements(stereo, stereo) {
		t.Fatal("the configured arrangement must be accepted")
	}
	if fixed.SetBusArrangements(mono, mono) {
		t.Fatal("a processor without BusArrangementProcessor must reject other arrangements")
	}
	if fixed.SetBusArrangements(stereo, nil) {
		t.Fatal("a bus count mismatch must be rejected")
	}

	flexible := &flexibleProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}
	c := newTestComponent(t, flexible)
	if !c.SetBusArrangements(mono, mono) {
		t.Fatal("arrangement accepted by the processor was rejected")
	}
	if got, ok := c.GetBusArrangement(int32(bus.DirectionOutput), 0); !ok || got != bus.ArrangementMono {
		t.Fatalf("output arrangement = %v, %v; want mono", got, ok)
	}
	if got := c.buses.GetBusInfo(bus.MediaTypeAudio, bus.DirectionInput, 0).ChannelCount; got != 1 {
		t.Fatalf("input channel count = %d, want 1", got)
	}
	if c.SetBusArrangements([]bus.SpeakerArrangement{bus.Arrangement51}, []bus.SpeakerArrangement{bus.Arrangement51}) {
		t.Fatal("arrangement rejected by the processor was accepted")
	}
}

func TestMIDIControllerParameters(t *testing.T) {
	registry := param.NewRegistry()
	if err := registry.Add(param.New(1, "Vibrato").Build()); err != nil {
		t.Fatal(err)
	}
	processor := &controllerProcessor{
		editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewGenerator()},
		channels:                 2,
		mapped:                   map[int16]uint32{1: 1},
	}
	c := newTestComponent(t, processor)

	if got, want := c.GetParameterCount(), int32(1+2*midiControllersPerChannel); got != want {
		t.Fatalf("parameter count = %d, want %d", got, want)
	}
	info, err := c.GetParameterInfo(1 + midiControllersPerChannel + int32(MIDIControllerPitchBend))
	if err != nil {
		t.Fatal(err)
	}
	if info.Flags&param.IsHidden == 0 || info.DefaultValue != 0.5 || info.Title != "MIDI Pitch Bend [Ch 2]" {
		t.Fatalf("pitch bend info = %+v", info)
	}

	// The processor mapping wins; unmapped controllers use hidden params.
	if id, ok := c.MIDIControllerAssignment(0, 0, 1); !ok || id != 1 {
		t.Fatalf("CC1 assignment = %d, %v; want parameter 1", id, ok)
	}
	id, ok := c.MIDIControllerAssignment(0, 1, 7)
	if !ok || id != MIDIControllerParamIDBase+midiControllersPerChannel+7 {
		t.Fatalf("CC7 ch2 assignment = %#x, %v", id, ok)
	}
	if _, ok := c.MIDIControllerAssignment(0, 2, 7); ok {
		t.Fatal("channel beyond MIDIControllerChannels must not be assigned")
	}
	if _, ok := c.MIDIControllerAssignment(1, 0, 7); ok {
		t.Fatal("only event bus 0 delivers controllers")
	}

	if err := c.SetParamNormalized(id, 1); err != nil || c.GetParamNormalized(id) != 1 {
		t.Fatalf("hidden controller value not stored: %v", err)
	}
	if s, err := c.GetParamStringByValue(id, 1); err != nil || s != "127" {
		t.Fatalf("controller string = %q, %v", s, err)
	}

	cc := c.midiCC.event(id, 1, 12)
	if cc.Type != midi.EventTypeControlChange || cc.Controller != 7 || cc.Value7() != 127 || cc.Channel != 1 || cc.Offset != 12 {
		t.Fatalf("controller event = %v", cc)
	}
	bendID, _ := c.midiCC.paramID(0, int(MIDIControllerPitchBend))
	for _, tc := range []struct {
		value float64
		want  int16
	}{{0, -8192}, {0.5, 0}, {1, 8191}} {
		bend := c.midiCC.event(bendID, tc.value, 0)
		if bend.Type != midi.EventTypePitchBend || bend.PitchBend14() != tc.want {
			t.Fatalf("pitch bend %v = %d, want %d", tc.value, bend.PitchBend14(), tc.want)
		}
	}
	if bend := c.midiCC.event(bendID, 0, 0); bend.Value != -1 {
		t.Fatalf("pitch bend minimum = %v, want -1", bend.Value)
	}

	// IMidiMapping2 enumerates the same assignments as IMidiMapping.
	assignments := c.midi1Assignments()
	if want := 2 * midiControllersPerChannel; len(assignments) != want {
		t.Fatalf("MIDI 1 assignments = %d, want %d", len(assignments), want)
	}
	for _, a := range assignments {
		if id, ok := c.MIDIControllerAssignment(a.busIndex, a.channel, a.controller); !ok || id != a.paramID {
			t.Fatalf("assignment %+v disagrees with IMidiMapping (%d, %v)", a, id, ok)
		}
	}
}

func TestMIDIControllerIDCollision(t *testing.T) {
	registry := param.NewRegistry()
	if err := registry.Add(param.New(MIDIControllerParamIDBase+3, "Clash").Build()); err != nil {
		t.Fatal(err)
	}
	processor := &controllerProcessor{
		editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewGenerator()},
		channels:                 1,
	}
	if _, err := newComponent(processor, frameworkplugin.Info{ID: "x"}); err == nil {
		t.Fatal("a parameter inside the MIDI controller ID range must be rejected")
	}
}

func TestParameterInfoAndTail(t *testing.T) {
	registry := param.NewRegistry()
	gain := param.GainParameter(5, "Gain").Default(-6).Build()
	if err := registry.Add(gain); err != nil {
		t.Fatal(err)
	}
	c := newTestComponent(t, &editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()})

	info, err := c.GetParameterInfo(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := gain.Normalize(-6); info.DefaultValue != want {
		t.Fatalf("default = %v, want normalized %v", info.DefaultValue, want)
	}
	if _, err := c.GetParameterInfo(1); err != errInvalidArgument {
		t.Fatalf("out-of-range index error = %v", err)
	}

	value, err := c.GetParamValueByString(5, c.mustString(t, 5, gain.Normalize(-6)))
	if err != nil || value != gain.Normalize(-6) {
		t.Fatalf("string round trip = %v, %v", value, err)
	}
}

func (c *componentImpl) mustString(t *testing.T, id uint32, value float64) string {
	t.Helper()
	s, err := c.GetParamStringByValue(id, value)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type tailProcessor struct {
	editorModelTestProcessor
}

func (tailProcessor) GetTailSamples() int32 { return TailInfinite }

func TestInfiniteTail(t *testing.T) {
	c := newTestComponent(t, &tailProcessor{editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewGenerator()}})
	if got := c.GetTailSamples(); got != 0xFFFFFFFF {
		t.Fatalf("tail = %#x, want kInfiniteTail", got)
	}
}
