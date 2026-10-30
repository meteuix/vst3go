package plugin

import (
	"strconv"
	"testing"

	"github.com/th-release/vst3go/pkg/ara"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
)

type testPlugin struct {
	id          string
	distributed bool
}

func (p testPlugin) GetInfo() frameworkplugin.Info { return frameworkplugin.Info{ID: p.id, Name: p.id} }
func (p testPlugin) Distributed() bool             { return p.distributed }
func (p testPlugin) CreateProcessor() Processor {
	return &editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}
}

func TestFactoryClasses(t *testing.T) {
	saved := registeredPlugins
	t.Cleanup(func() { registeredPlugins = saved })
	registeredPlugins = nil
	Register(testPlugin{id: "com.example.a"})
	Register(testPlugin{id: "com.example.b", distributed: true})

	classes := factoryClasses()
	if len(classes) != 3 {
		t.Fatalf("classes = %d, want single + processor + controller", len(classes))
	}
	a, b := classes[0].info, classes[1].info
	if classes[0].kind != kindSingle || classes[0].cid != a.UID() {
		t.Fatalf("class 0 = %+v", classes[0])
	}
	if classes[1].kind != kindProcessor || classes[1].cid != b.UID() || classes[1].controller != b.ControllerUID() {
		t.Fatalf("class 1 = %+v", classes[1])
	}
	if classes[2].kind != kindController || classes[2].cid != b.ControllerUID() {
		t.Fatalf("class 2 = %+v", classes[2])
	}
	if b.ControllerUID() == b.UID() {
		t.Fatal("controller UID must differ from the processor UID")
	}
}

func TestMIDI2ControllerBytes(t *testing.T) {
	for _, c := range []MIDI2Controller{{Bank: 0, Index: 0}, {Bank: 127, Index: 127, Registered: true}, {Bank: 3, Index: 9}} {
		b1, b2 := midi2ControllerBytes(c)
		if got := midi2ControllerFromBytes(b1, b2); got != c {
			t.Fatalf("round trip %+v -> %+v", c, got)
		}
	}
	if b1, _ := midi2ControllerBytes(MIDI2Controller{Bank: 1, Registered: true}); b1 != 0x81 {
		t.Fatalf("registered bank 1 byte1 = %#x, want 0x81", b1)
	}
}

type testARAPlugin struct{ testPlugin }

func (testARAPlugin) ARA() ara.Config { return ara.Config{ArchiveID: "com.example.ara.1"} }

func TestARAFactoryClass(t *testing.T) {
	saved := registeredPlugins
	t.Cleanup(func() { registeredPlugins = saved })
	registeredPlugins = nil
	Register(testPlugin{id: "com.example.plain"})
	Register(testARAPlugin{testPlugin{id: "com.example.ara"}})

	classes := factoryClasses()
	if len(classes) != 3 {
		t.Fatalf("classes = %d, want plain + ARA plugin + its ARA main factory", len(classes))
	}
	info := classes[1].info
	if classes[1].kind != kindSingle || classes[2].kind != kindARAFactory || classes[2].index != 1 {
		t.Fatalf("classes %+v", classes)
	}
	if classes[2].cid != info.ARAFactoryUID() || info.ARAFactoryUID() == info.UID() {
		t.Fatal("the ARA main factory class needs its own class ID")
	}
	if c := clapFeatures(classes[1].plugin, info); c[len(c)-1] != "ara:supported" {
		t.Fatalf("CLAP features %v, want ara:supported", c)
	}
	if c, err := newAUComponent(classes[1].plugin); err != nil || c.Tags[len(c.Tags)-1] != "ARA" {
		t.Fatalf("AU tags %v, %v", c.Tags, err)
	}
}

func TestRegisterLimitsARAPlugins(t *testing.T) {
	saved := registeredPlugins
	t.Cleanup(func() { registeredPlugins = saved })
	registeredPlugins = nil
	for i := range maxARAFactories {
		Register(testARAPlugin{testPlugin{id: "com.example.ara" + strconv.Itoa(i)}})
	}
	Register(testPlugin{id: "com.example.plain"})
	defer func() {
		if recover() == nil {
			t.Fatal("registered more ARA plugins than the runtime has factories")
		}
	}()
	Register(testARAPlugin{testPlugin{id: "com.example.one-too-many"}})
}
