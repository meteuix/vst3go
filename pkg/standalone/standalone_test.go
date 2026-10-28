//go:build standalone

package standalone

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/midi"
	"github.com/th-release/vst3go/pkg/plugin"
)

// testSynth plays 0.25 on both channels while a note is held and echoes
// its notes to the MIDI output.
type testSynth struct{}

var synthBlocks atomic.Int64

func (testSynth) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.vst3go.test.standalone-synth", Name: "Standalone Test Synth", Version: "1.0.0", Vendor: "VST3Go Tests", Category: frameworkplugin.CategoryInstrument}
}

func (testSynth) CreateProcessor() plugin.Processor {
	config := bus.NewBuilder().WithStereoOutput("Out").WithEventInput("MIDI In").WithEventOutput("MIDI Out").MustBuild()
	return &synthProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(config)}
}

type synthProcessor struct {
	*frameworkplugin.BaseProcessor
	held int
}

func (p *synthProcessor) ProcessAudio(ctx *process.Context) {
	synthBlocks.Add(1)
	for _, e := range ctx.GetAllInputEvents() {
		switch e.Type {
		case midi.EventTypeNoteOn:
			p.held++
			ctx.AddOutputEvent(e)
		case midi.EventTypeNoteOff:
			p.held--
			ctx.AddOutputEvent(e)
		}
	}
	level := float32(0)
	if p.held > 0 {
		level = 0.25
	}
	for _, ch := range ctx.Output {
		for i := range ch[:ctx.NumSamples()] {
			ch[i] = level
		}
	}
}

// testEffect scales its stereo input by the Gain parameter (default 0.5).
type testEffect struct{}

func (testEffect) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.vst3go.test.standalone-effect", Name: "Standalone Test Effect", Version: "1.0.0", Vendor: "VST3Go Tests", Category: frameworkplugin.CategoryFx}
}

func (testEffect) CreateProcessor() plugin.Processor {
	p := &effectProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(param.New(0, "Gain").Range(0, 1).Default(0.5).Build())
	return p
}

type effectProcessor struct{ *frameworkplugin.BaseProcessor }

func (p *effectProcessor) ProcessAudio(ctx *process.Context) {
	gain := float32(ctx.ParamPlain(0))
	for ch := range ctx.Output {
		for i := range ctx.Output[ch][:ctx.NumSamples()] {
			ctx.Output[ch][i] = ctx.Input[ch][i] * gain
		}
	}
}

func init() {
	plugin.Register(testSynth{})
	plugin.Register(testEffect{})
}

func floats(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func bytesOf(f []float32) []byte {
	b := make([]byte, 4*len(f))
	for i, v := range f {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return b
}

// hostPlugin creates and activates a registered plugin for a test.
func hostPlugin(t *testing.T, name string, frames int) {
	t.Helper()
	index, _, err := pluginIndex(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := createPlugin(index); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(destroyPlugin)
	if err := activatePlugin(48000, frames); err != nil {
		t.Fatal(err)
	}
}

func TestPluginIndex(t *testing.T) {
	if _, _, err := pluginIndex("standalone test effect"); err != nil {
		t.Fatalf("name lookup ignores case: %v", err)
	}
	index, _, err := pluginIndex("com.vst3go.test.standalone-effect")
	if err != nil || index != 1 {
		t.Fatalf("ID lookup = %d, %v", index, err)
	}
	if _, _, err := pluginIndex("Missing"); err == nil || !strings.Contains(err.Error(), "Standalone Test Synth (com.vst3go.test.standalone-synth)") {
		t.Fatalf("missing plugin error lists the registered ones: %v", err)
	}
}

func TestInstrumentMIDIInAndOut(t *testing.T) {
	hostPlugin(t, "Standalone Test Synth", 64)
	if in, out := mainChannels(); in != 0 || out != 2 {
		t.Fatalf("main channels = %d in, %d out", in, out)
	}
	a := &app{audio: &audio{outChannels: 2}, midiIn: make(chan [3]byte, 16), midiOut: make(chan [3]byte, 16)}
	out := make([]byte, 4*2*100) // longer than the 64-frame maximum block
	a.midiIn <- [3]byte{0x90, 60, 100}
	a.process(out, nil, 100)
	for i, v := range floats(out) {
		if v != 0.25 {
			t.Fatalf("sample %d = %g while the note is held", i, v)
		}
	}
	if got := <-a.midiOut; got != [3]byte{0x90, 60, 100} {
		t.Fatalf("MIDI out = % x", got)
	}
	a.midiIn <- [3]byte{0x80, 60, 0}
	a.process(out, nil, 100)
	for i, v := range floats(out) {
		if v != 0 {
			t.Fatalf("sample %d = %g after the note off", i, v)
		}
	}
	if got := <-a.midiOut; got[0] != 0x80 || got[1] != 60 {
		t.Fatalf("MIDI out = % x", got)
	}
}

func TestEffectMonoDeviceFeedsStereoInput(t *testing.T) {
	hostPlugin(t, "Standalone Test Effect", 256)
	a := &app{audio: &audio{inChannels: 1, outChannels: 2}, midiIn: make(chan [3]byte, 1), midiOut: make(chan [3]byte, 1)}
	in := bytesOf([]float32{0.5, -1, 0.25})
	out := make([]byte, 4*2*3)
	a.process(out, in, 3)
	want := []float32{0.25, 0.25, -0.5, -0.5, 0.125, 0.125}
	for i, v := range floats(out) {
		if v != want[i] {
			t.Fatalf("output = %v, want %v", floats(out), want)
		}
	}
}

func TestStateFile(t *testing.T) {
	hostPlugin(t, "Standalone Test Effect", 256)
	a := &app{dir: t.TempDir()}
	if err := a.loadState(); err != nil {
		t.Fatalf("no state file yet: %v", err)
	}
	if err := a.saveState(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.statePath())
	if err != nil || !bytes.HasPrefix(data, []byte("VST3GO")) {
		t.Fatalf("state file = %q, %v", data, err)
	}
	if err := a.loadState(); err != nil {
		t.Fatalf("load saved state: %v", err)
	}
	if err := os.WriteFile(a.statePath(), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.loadState(); err == nil {
		t.Fatal("a corrupt state file loaded")
	}
}

func TestRunWithNullDevice(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer
	before := synthBlocks.Load()
	args := []string{"-backend", "null", "-headless", "-duration", "300ms", "-config", dir,
		"-plugin", "Standalone Test Synth", "-buffer", "128", "-sample-rate", "44100", "-midi-in", "none", "-virtual-midi=false"}
	if err := run(args, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "audio null: output default (2 ch), no input, 44100 Hz, 128 frames") {
		t.Fatalf("log = %q", log.String())
	}
	if synthBlocks.Load() == before {
		t.Fatal("the null device processed no block")
	}
	s, err := LoadSettings(dir)
	if err != nil || s != (Settings{Backend: "null", SampleRate: 44100, BufferSize: 128, MIDIInput: "none", NoVirtualMIDI: true}) {
		t.Fatalf("saved settings = %+v, %v", s, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFile)); err != nil {
		t.Fatalf("state not saved on quit: %v", err)
	}

	// The saved settings apply to the next run.
	log.Reset()
	if err := run([]string{"-headless", "-duration", "100ms", "-config", dir, "-plugin", "Standalone Test Synth"}, &log); err != nil {
		t.Fatalf("second run: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "audio null:") || !strings.Contains(log.String(), "44100 Hz, 128 frames") {
		t.Fatalf("second run ignored the settings: %q", log.String())
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-plugin", "Nope"}, `no plugin "Nope"`},
		{[]string{"-backend", "bogus"}, `unknown audio backend "bogus"`},
		{[]string{"-backend", "null", "-output", "no such device"}, `no single audio device matches "no such device"`},
		{[]string{"-buffer", "-1"}, "must not be negative"},
		{[]string{"extra"}, "unexpected arguments"},
	} {
		err := run(append([]string{"-config", dir, "-headless"}, tc.args...), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("run(%q) = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestChannelMessage(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want [3]byte
		ok   bool
	}{
		{[]byte{0x91, 60, 100}, [3]byte{0x91, 60, 100}, true},
		{[]byte{0xC2, 5}, [3]byte{0xC2, 5, 0}, true},
		{[]byte{0xE0, 0, 64}, [3]byte{0xE0, 0, 64}, true},
		{[]byte{0x90, 60}, [3]byte{}, false},
		{[]byte{0xF8}, [3]byte{}, false},
		{[]byte{0xF0, 1, 2, 0xF7}, [3]byte{}, false},
		{[]byte{60, 100}, [3]byte{}, false},
		{nil, [3]byte{}, false},
	} {
		if got, ok := channelMessage(tc.in); got != tc.want || ok != tc.ok {
			t.Errorf("channelMessage(% x) = % x, %v", tc.in, got, ok)
		}
	}
}
