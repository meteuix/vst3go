package plugin

import (
	"strings"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
)

type auTestPlugin struct {
	info  frameworkplugin.Info
	buses func() *bus.Configuration
	codes [2]string
}

func (p auTestPlugin) GetInfo() frameworkplugin.Info { return p.info }
func (p auTestPlugin) CreateProcessor() Processor {
	return &editorModelTestProcessor{params: param.NewRegistry(), buses: p.buses()}
}

type auCodesPlugin struct{ auTestPlugin }

func (p auCodesPlugin) AudioUnitCodes() (string, string) { return p.codes[0], p.codes[1] }

func TestAUComponentTypes(t *testing.T) {
	effectWithMIDI := func() *bus.Configuration {
		return bus.NewBuilder().WithStereoInput("In").WithStereoOutput("Out").WithEventInput("MIDI").MustBuild()
	}
	cases := []struct {
		name     string
		category string
		buses    func() *bus.Configuration
		want     string
	}{
		{"effect", frameworkplugin.CategoryFxEQ, bus.NewEffectStereo, "aufx"},
		{"instrument", frameworkplugin.CategoryInstrumentSynth, bus.NewGenerator, "aumu"},
		{"generator without notes", frameworkplugin.CategoryFxGenerator, func() *bus.Configuration {
			return bus.NewBuilder().WithStereoOutput("Out").MustBuild()
		}, "augn"},
		{"effect with note input", frameworkplugin.CategoryFx, effectWithMIDI, "aumf"},
		{"midi effect", frameworkplugin.CategoryFxInstrument, bus.NewMIDIEffect, "aumi"},
	}
	for _, tc := range cases {
		p := auTestPlugin{info: frameworkplugin.Info{ID: "com.example." + tc.name, Name: tc.name, Vendor: "Example", Category: tc.category}, buses: tc.buses}
		c, err := newAUComponent(p)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Type != tc.want {
			t.Errorf("%s: type %q, want %q", tc.name, c.Type, tc.want)
		}
	}
}

func TestAUComponentCodes(t *testing.T) {
	p := auTestPlugin{info: frameworkplugin.Info{ID: "com.example.gain", Name: "Gain <Pro>", Version: "1.2.3", Vendor: "my co"}, buses: bus.NewEffectStereo}
	c, err := newAUComponent(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Manufacturer != "Myco" {
		t.Errorf("manufacturer %q", c.Manufacturer)
	}
	if c.Subtype != auSubtype("com.example.gain") || len(c.Subtype) != 4 || strings.ToLower(c.Subtype) != c.Subtype {
		t.Errorf("subtype %q", c.Subtype)
	}
	if auSubtype("com.example.gain") == auSubtype("com.example.gain2") {
		t.Error("subtypes of different IDs collide")
	}
	if c.Version != 0x00010203 || c.Name != "my co: Gain <Pro>" {
		t.Errorf("version %#x name %q", c.Version, c.Name)
	}
	if got := auManufacturer("x"); got != "Xxxx" {
		t.Errorf("short vendor: %q", got)
	}
	if got := auVersion("2.10-beta"); got != 0x00020a00 {
		t.Errorf("version %#x", got)
	}

	custom := auCodesPlugin{p}
	custom.codes = [2]string{"Acme", "gain"}
	if c, err := newAUComponent(custom); err != nil || c.Manufacturer != "Acme" || c.Subtype != "gain" {
		t.Errorf("custom codes: %+v %v", c, err)
	}
	for _, bad := range [][2]string{{"acme", "gain"}, {"Acm", "gain"}, {"Acme", "gaín"}} {
		custom.codes = bad
		if _, err := newAUComponent(custom); err == nil {
			t.Errorf("codes %q accepted", bad)
		}
	}
}

func TestAUComponentsPlist(t *testing.T) {
	a := auTestPlugin{info: frameworkplugin.Info{ID: "com.example.a", Name: "A & B", Vendor: "Example"}, buses: bus.NewEffectStereo}
	b := auTestPlugin{info: frameworkplugin.Info{ID: "com.example.b", Name: "Synth", Vendor: "Example", Category: frameworkplugin.CategoryInstrumentSynth}, buses: bus.NewGenerator}
	list, err := auComponents([]Plugin{a, b})
	if err != nil {
		t.Fatal(err)
	}
	plist := auComponentsPlist(list)
	for _, want := range []string{
		"<key>AudioComponents</key>",
		"<string>Example: A &amp; B</string>",
		"<key>type</key><string>aufx</string>",
		"<key>type</key><string>aumu</string>",
		"<key>factoryFunction</key><string>VST3GoAUFactory</string>",
		"<string>Synthesizer</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}

	same := auCodesPlugin{a}
	same.codes = [2]string{"Exam", auSubtype("com.example.a")}
	if _, err := auComponents([]Plugin{a, same}); err == nil {
		t.Error("duplicate component codes accepted")
	}
}
