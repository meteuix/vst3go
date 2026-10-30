package plugin

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

// messagingProcessor records the messages and editor messages it receives
// and keeps the Host it is given.
type messagingProcessor struct {
	editorModelTestProcessor
	host     Host
	received []string
	data     [][]byte
	values   []float64 // its Cutoff value when each message arrived
	editor   []string
}

func (p *messagingProcessor) SetHost(host Host) { p.host = host }

func (p *messagingProcessor) ReceiveMessage(id string, data []byte) {
	p.received = append(p.received, id)
	p.data = append(p.data, data)
	p.values = append(p.values, p.params.Get(1).GetNormalized())
}

func (p *messagingProcessor) EditorMessage(name string, data json.RawMessage) {
	p.editor = append(p.editor, name)
}

func messagingSides(t *testing.T) (*clapInstance, *messagingProcessor, *messagingProcessor) {
	t.Helper()
	newSide := func() *messagingProcessor {
		registry := param.NewRegistry()
		if err := registry.Add(param.New(1, "Cutoff").Range(0, 1).Build()); err != nil {
			t.Fatal(err)
		}
		return &messagingProcessor{editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()}}
	}
	processor, controller := newSide(), newSide()
	inst := &clapInstance{c: newTestComponent(t, processor), peer: newTestComponent(t, controller)}
	inst.c.controllerSide = controller
	processor.SetHost(clapHost{inst})
	controller.SetHost(clapPeerHost{inst})
	return inst, processor, controller
}

func TestCLAPDistributedMessages(t *testing.T) {
	inst, processor, controller := messagingSides(t)
	inst.c.params.Get(1).SetNormalized(0.75)

	payload := []byte{1, 2, 3}
	if err := processor.host.SendMessage("latency", payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 9 // the receiver got a copy
	if len(controller.received) != 1 || controller.received[0] != "latency" || controller.data[0][0] != 1 {
		t.Fatalf("controller side received %v %v", controller.received, controller.data)
	}
	if controller.values[0] != 0.75 {
		t.Fatalf("controller side Cutoff = %v when the message arrived, want 0.75", controller.values[0])
	}
	if len(processor.received) != 0 {
		t.Fatal("a message must not come back to its sender")
	}

	if err := controller.host.SendMessage("ack", nil); err != nil {
		t.Fatal(err)
	}
	if len(processor.received) != 1 || processor.received[0] != "ack" {
		t.Fatalf("processor side received %v", processor.received)
	}

	for _, id := range []string{"", messagePrefix + "edit"} {
		if err := processor.host.SendMessage(id, nil); !errors.Is(err, errInvalidArgument) {
			t.Fatalf("SendMessage(%q) = %v, want errInvalidArgument", id, err)
		}
		if err := controller.host.SendMessage(id, nil); !errors.Is(err, errInvalidArgument) {
			t.Fatalf("controller SendMessage(%q) = %v, want errInvalidArgument", id, err)
		}
	}

	// The editor's custom messages go to the controller side, which serves
	// the editor in VST3.
	w := &componentWrapper{component: inst.c}
	w.handleEditorMessage(inst.c, clapHost{inst}, []byte(`{"type":"message","name":"learn"}`))
	if len(controller.editor) != 1 || controller.editor[0] != "learn" || len(processor.editor) != 0 {
		t.Fatalf("editor message reached controller %v, processor %v", controller.editor, processor.editor)
	}
}

func TestCLAPSingleComponentHasNoPeer(t *testing.T) {
	registry := param.NewRegistry()
	inst := &clapInstance{c: newTestComponent(t, &editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()})}
	if err := (clapHost{inst}).SendMessage("x", nil); !errors.Is(err, errNotSupported) {
		t.Fatalf("SendMessage = %v, want errNotSupported", err)
	}
}

type learningProcessor struct {
	editorModelTestProcessor
	learned []clapLearned
}

func (p *learningProcessor) LearnMIDIController(busIndex int32, channel, controller int16) bool {
	p.learned = append(p.learned, clapLearned{port: busIndex, channel: channel, controller: controller})
	return true
}

func TestCLAPMIDILearnDelivery(t *testing.T) {
	learner := &learningProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}
	inst := &clapInstance{c: newTestComponent(t, learner), learn: newMPSCQueue[clapLearned](clapLearnQueueSize)}
	want := []clapLearned{{0, 0, 7}, {1, 3, 74}}
	for _, l := range want {
		if !inst.learn.push(l) {
			t.Fatal("queue full")
		}
	}
	inst.deliverLearned()
	if len(learner.learned) != 2 || learner.learned[0] != want[0] || learner.learned[1] != want[1] {
		t.Fatalf("learned %v, want %v", learner.learned, want)
	}
	inst.deliverLearned()
	if len(learner.learned) != 2 {
		t.Fatal("controllers must be delivered once")
	}
}

type keyswitchProcessor struct {
	editorModelTestProcessor
	perChannel bool
}

func (p *keyswitchProcessor) Keyswitches(busIndex int32, channel int16) []KeyswitchInfo {
	if busIndex != 0 || (p.perChannel && channel != 2) {
		return nil
	}
	return []KeyswitchInfo{
		{Title: "Legato", Min: 24, Max: 24},
		{Title: "Spiccato", Min: 25, Max: 26},
	}
}

func TestCLAPKeyswitchNoteNames(t *testing.T) {
	newInst := func(perChannel bool) *clapInstance {
		p := &keyswitchProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}, perChannel: perChannel}
		return &clapInstance{c: newTestComponent(t, p)}
	}
	names := newInst(false).noteNames()
	want := []clapNoteName{{24, -1, "Legato"}, {25, -1, "Spiccato"}, {26, -1, "Spiccato"}}
	if len(names) != len(want) {
		t.Fatalf("note names %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("note names %v, want %v", names, want)
		}
	}
	// Keyswitches of one channel are named on that channel only.
	names = newInst(true).noteNames()
	if len(names) != 3 || names[0] != (clapNoteName{24, 2, "Legato"}) {
		t.Fatalf("per-channel note names %v", names)
	}
}
