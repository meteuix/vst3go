//go:build standalone

package standalone

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"slices"
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

var (
	synthBlocks atomic.Int64
	// synthTransport is the transport of the synth's last block.
	synthTransport process.TransportInfo
)

func (testSynth) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.vst3go.test.standalone-synth", Name: "Standalone Test Synth", Version: "1.0.0", Vendor: "VST3Go Tests", Category: frameworkplugin.CategoryInstrument}
}

func (testSynth) CreateProcessor() plugin.Processor {
	config := bus.NewBuilder().WithStereoOutput("Out").WithEventInput("MIDI In").WithEventOutput("MIDI Out").MustBuild()
	p := &synthProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(config)}
	p.SetProcessDeadline(0) // see testEffect
	return p
}

type synthProcessor struct {
	*frameworkplugin.BaseProcessor
	held int
}

func (p *synthProcessor) ProcessAudio(ctx *process.Context) {
	synthBlocks.Add(1)
	synthTransport = *ctx.Transport
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
	// The tests check the host's routing with blocks of a few frames: a
	// 3-frame block has a 31 µs deadline, which the cold first block of a
	// process misses under the race detector, and the guard then outputs
	// silence. The deadline guard has its own tests in pkg/plugin.
	p.SetProcessDeadline(0)
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
	for _, ok := popMIDI(); ok; _, ok = popMIDI() {
	}
	out := make([]float32, 2*100) // longer than the 64-frame maximum block
	pushMIDI([3]byte{0x90, 60, 100})
	render(out, nil, 0, 2, 100)
	for i, v := range out {
		if v != 0.25 {
			t.Fatalf("sample %d = %g while the note is held", i, v)
		}
	}
	if got, ok := popMIDI(); !ok || got != [3]byte{0x90, 60, 100} {
		t.Fatalf("MIDI out = % x, %v", got, ok)
	}
	pushMIDI([3]byte{0x80, 60, 0})
	render(out, nil, 0, 2, 100)
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d = %g after the note off", i, v)
		}
	}
	if got, ok := popMIDI(); !ok || got[0] != 0x80 || got[1] != 60 {
		t.Fatalf("MIDI out = % x, %v", got, ok)
	}
}

func TestEffectMonoDeviceFeedsStereoInput(t *testing.T) {
	hostPlugin(t, "Standalone Test Effect", 256)
	out := make([]float32, 2*3)
	render(out, []float32{0.5, -1, 0.25}, 1, 2, 3)
	want := []float32{0.25, 0.25, -0.5, -0.5, 0.125, 0.125}
	if !slices.Equal(out, want) {
		t.Fatalf("output = %v, want %v", out, want)
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
		{[]string{"-tempo", "5"}, "tempo 5 is outside 20 to 999 BPM"},
		{[]string{"-time-signature", "7/5"}, "time signature 7/5 is not supported"},
		{[]string{"-time-signature", "fast"}, `time signature "fast" is not beats/unit`},
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

// recorder collects the messages the application posts to a panel.
type recorder struct{ messages []any }

func (r *recorder) Post(message any) error {
	r.messages = append(r.messages, message)
	return nil
}

func (r *recorder) last() any {
	if len(r.messages) == 0 {
		return nil
	}
	return r.messages[len(r.messages)-1]
}

func TestTransport(t *testing.T) {
	hostPlugin(t, "Standalone Test Synth", 64)
	toolbar := &recorder{}
	a := &app{dir: t.TempDir(), settings: Settings{Tempo: 90, BeatsPerBar: 3, BeatUnit: 4}, toolbarPost: toolbar}
	a.applyTransport()
	a.toolbarMessage([]byte(`{"type":"rewind"}`))
	out := make([]float32, 2*48000)

	render(out, nil, 0, 2, 48000)
	if tr := synthTransport; tr.IsPlaying || !tr.HasTempo || tr.Tempo != 90 || tr.TimeSigNumerator != 3 || tr.TimeSigDenominator != 4 || tr.ProjectTimeMusic != 0 {
		t.Fatalf("stopped transport = %+v", tr)
	}

	a.toolbarMessage([]byte(`{"type":"play"}`))
	if msg, ok := toolbar.last().(transportMessage); !ok || !msg.Playing || msg.Tempo != 90 || msg.BeatsPerBar != 3 || msg.Position != "1.1" {
		t.Fatalf("toolbar after play = %+v", toolbar.last())
	}
	// Two seconds at 90 BPM are three quarter notes: one 3/4 bar.
	render(out, nil, 0, 2, 48000)
	render(out, nil, 0, 2, 48000)
	tr := synthTransport
	if !tr.IsPlaying || !tr.HasMusicalTime {
		t.Fatalf("playing transport = %+v", tr)
	}
	// The last block starts 64 frames before the end.
	if want := 3 - 64*1.5/48000; math.Abs(tr.ProjectTimeMusic-want) > 1e-6 {
		t.Fatalf("position of the last block = %v quarters, want %v", tr.ProjectTimeMusic, want)
	}
	if tr.BarPositionMusic != 0 {
		t.Fatalf("bar start = %v, want 0", tr.BarPositionMusic)
	}
	a.updatePosition()
	if msg, ok := toolbar.last().(positionMessage); !ok || msg.Position != "2.1" {
		t.Fatalf("position after a bar = %+v", toolbar.last())
	}
	render(out, nil, 0, 2, 48000)
	if tr := synthTransport; tr.BarPositionMusic != 3 {
		t.Fatalf("bar start in the second bar = %v, want 3", tr.BarPositionMusic)
	}

	// Stopped, the position holds; rewind returns to the start.
	a.toolbarMessage([]byte(`{"type":"stop"}`))
	render(out, nil, 0, 2, 64)
	held := synthTransport.ProjectTimeMusic
	render(out, nil, 0, 2, 64)
	if tr := synthTransport; tr.IsPlaying || tr.ProjectTimeMusic != held || held < 4.4 {
		t.Fatalf("stopped transport moved: %+v (held %v)", tr, held)
	}
	a.toolbarMessage([]byte(`{"type":"rewind"}`))
	if msg, ok := toolbar.last().(transportMessage); !ok || msg.Position != "1.1" {
		t.Fatalf("toolbar after rewind = %+v", toolbar.last())
	}
	render(out, nil, 0, 2, 64)
	if tr := synthTransport; tr.ProjectTimeMusic != 0 {
		t.Fatalf("position after rewind = %v", tr.ProjectTimeMusic)
	}

	// Tempo and time signature apply and persist; invalid ones revert.
	a.toolbarMessage([]byte(`{"type":"tempo","tempo":140}`))
	a.toolbarMessage([]byte(`{"type":"time-signature","beatsPerBar":6,"beatUnit":8}`))
	a.toolbarMessage([]byte(`{"type":"tempo","tempo":5}`))
	if msg, ok := toolbar.last().(transportMessage); !ok || msg.Tempo != 140 || msg.BeatsPerBar != 6 || msg.BeatUnit != 8 {
		t.Fatalf("toolbar after an invalid tempo = %+v", toolbar.last())
	}
	render(out, nil, 0, 2, 64)
	if tr := synthTransport; tr.Tempo != 140 || tr.TimeSigNumerator != 6 || tr.TimeSigDenominator != 8 {
		t.Fatalf("transport = %+v", tr)
	}
	if s, err := LoadSettings(a.dir); err != nil || s.Tempo != 140 || s.BeatsPerBar != 6 || s.BeatUnit != 8 {
		t.Fatalf("saved settings = %+v, %v", s, err)
	}
}

func TestSettingsWindow(t *testing.T) {
	index, _, err := pluginIndex("Standalone Test Effect")
	if err != nil {
		t.Fatal(err)
	}
	if err := createPlugin(index); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(destroyPlugin)
	page, toolbar := &recorder{}, &recorder{}
	a := &app{name: "Effect", dir: t.TempDir(), settingsPost: page, toolbarPost: toolbar,
		settings: Settings{Backend: "null", MIDIInput: "none", NoVirtualMIDI: true, Tempo: 100}}
	in, out := mainChannels()
	if a.audio, err = openAudio(a.settings, in, out); err != nil {
		t.Fatal(err)
	}
	if err := a.startAudio(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.stopAudio()
		a.audio.close()
		a.midi.close()
	})

	a.settingsMessage([]byte(`{"type":"ready"}`))
	devices, ok := page.last().(devicesMessage)
	if !ok || !slices.Contains(devices.Backends, "null") || !devices.AudioInput || devices.Settings.Backend != "null" || devices.Error != "" {
		t.Fatalf("devices = %+v", page.last())
	}

	a.settingsMessage([]byte(`{"type":"apply","settings":{"backend":"null","sampleRate":44100,"bufferSize":512,"midiInput":"none","noVirtualMIDI":true}}`))
	applied, ok := page.last().(appliedMessage)
	if !ok || applied.Error != "" || !strings.Contains(applied.Status, "44100 Hz, 512 frames") {
		t.Fatalf("applied = %+v", page.last())
	}
	if !a.audio.running() || a.audio.frames != 512 || a.audio.sampleRate != 44100 {
		t.Fatalf("audio after apply = %+v", a.audio)
	}
	if status, ok := toolbar.last().(statusMessage); !ok || !strings.Contains(status.Text, "512 frames") {
		t.Fatalf("toolbar status = %+v", toolbar.last())
	}
	saved, err := LoadSettings(a.dir)
	if err != nil || saved.BufferSize != 512 || saved.SampleRate != 44100 || saved.Tempo != 100 {
		t.Fatalf("saved settings = %+v, %v (the transport tempo is kept)", saved, err)
	}

	// A device that fails to open reports the error and the previous
	// devices come back.
	a.settingsMessage([]byte(`{"type":"apply","settings":{"backend":"null","output":"no such device","bufferSize":128}}`))
	if applied, ok := page.last().(appliedMessage); !ok || !strings.Contains(applied.Error, `no single audio device matches "no such device"`) {
		t.Fatalf("applied = %+v", page.last())
	}
	if !a.audio.running() || a.audio.frames != 512 || a.settings.BufferSize != 512 || a.quit.Load() {
		t.Fatalf("audio after a failed apply = %+v, settings %+v", a.audio, a.settings)
	}
}
