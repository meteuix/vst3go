package plugin

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/framework/bus"
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/v2/pkg/framework/plugin"
	"github.com/th-release/vst3go/v2/pkg/framework/process"
	"github.com/th-release/vst3go/v2/pkg/midi"
)

// recorder copies its main input to the output and records what it saw.
type recorder struct {
	editorModelTestProcessor
	blocks    []int
	positions []int64
	events    []midi.Event
	host      Host
}

func (r *recorder) SetHost(h Host) { r.host = h }

func (r *recorder) ProcessAudio(ctx *process.Context) {
	r.blocks = append(r.blocks, ctx.NumSamples())
	r.positions = append(r.positions, ctx.Transport.ProjectTimeSamples)
	for _, e := range ctx.GetAllInputEvents() {
		r.events = append(r.events, e)
		ctx.AddOutputEvent(e)
	}
	for ch := range ctx.Output {
		copy(ctx.Output[ch], ctx.Input[ch])
	}
}

type recorderPlugin struct{ r *recorder }

func (p recorderPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.example.recorder", Name: "Recorder", Vendor: "Acme"}
}
func (p recorderPlugin) CreateProcessor() Processor { return p.r }

func newRecorder(t *testing.T, setup OfflineSetup) (*Offline, *recorder) {
	t.Helper()
	registry := param.NewRegistry()
	if err := registry.Add(param.New(1, "Level").Range(0, 1).Build()); err != nil {
		t.Fatal(err)
	}
	r := &recorder{editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()}}
	o, err := NewOffline(recorderPlugin{r}, setup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { o.Close() })
	return o, r
}

func TestOfflineSplitsIntoBlocksAndAdvancesTransport(t *testing.T) {
	o, r := newRecorder(t, OfflineSetup{MaxBlockSize: 100})
	o.Transport = process.TransportInfo{IsPlaying: true, HasTempo: true, Tempo: 120, HasMusicalTime: true}
	o.SendEvent(midi.NoteOn(0, 60, 1).At(150))
	in := [][]float32{make([]float32, 250), make([]float32, 250)}
	in[0][249] = 1
	out := [][]float32{make([]float32, 250), make([]float32, 250)}
	if err := o.Process(in, out); err != nil {
		t.Fatal(err)
	}
	if len(r.blocks) != 3 || r.blocks[0] != 100 || r.blocks[2] != 50 {
		t.Fatalf("blocks = %v", r.blocks)
	}
	if r.positions[1] != 100 || r.positions[2] != 200 || o.Transport.ProjectTimeSamples != 250 {
		t.Fatalf("transport positions = %v, end %d", r.positions, o.Transport.ProjectTimeSamples)
	}
	if want := 250.0 / 48000 * 2; o.Transport.ProjectTimeMusic != want {
		t.Fatalf("musical time = %v, want %v", o.Transport.ProjectTimeMusic, want)
	}
	if len(r.events) != 1 || r.events[0].Offset != 50 {
		t.Fatalf("event seen at block offset %v, want 50 in block 2", r.events)
	}
	if ev := o.OutputEvents(); len(ev) != 1 || ev[0].Offset != 150 {
		t.Fatalf("output event offsets = %v, want 150", ev)
	}
	if out[0][249] != 1 {
		t.Fatal("audio not passed through")
	}
}

func TestOfflineRejectsBadInput(t *testing.T) {
	o, _ := newRecorder(t, OfflineSetup{})
	o.SendEvent(midi.NoteOn(0, 60, 1).At(64))
	if err := o.Process(nil, [][]float32{make([]float32, 64)}); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("event past the buffer: err = %v", err)
	}
	if err := o.Process(nil, [][]float32{make([]float32, 64)}); err != nil {
		t.Fatalf("queue must be cleared after a failed call: %v", err)
	}
	if err := o.Automate(99, 0, 1); err == nil {
		t.Fatal("unknown parameter automated")
	}
	if err := o.Process([][]float32{make([]float32, 3)}, [][]float32{make([]float32, 4)}); !errors.Is(err, errOfflineBlock) {
		t.Fatalf("mismatched lengths: err = %v", err)
	}
	if err := o.Process64(nil, nil); err == nil {
		t.Fatal("64-bit processing without DoublePrecision")
	}
	if err := o.SendController(0, 1, 0, 1); err == nil {
		t.Fatal("controller without MIDI mapping accepted")
	}
	if _, err := NewOffline(recorderPlugin{&recorder{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}}, OfflineSetup{DoublePrecision: true}); err == nil {
		t.Fatal("64-bit setup for a processor without DoublePrecisionProcessor")
	}
}

func TestOfflineFlushAndHost(t *testing.T) {
	o, r := newRecorder(t, OfflineSetup{})
	if err := o.Automate(1, 0, 0.75); err != nil {
		t.Fatal(err)
	}
	if err := o.ProcessEvents(0); err != nil { // a host parameter flush
		t.Fatal(err)
	}
	if v, _, _ := o.Parameter(1); v != 0.75 {
		t.Fatalf("flushed value = %v", v)
	}

	path := filepath.Join(t.TempDir(), "p.vstpreset")
	if err := r.host.SavePreset(path); err != nil {
		t.Fatal(err)
	}
	o.SetParameter(1, 0)
	if err := r.host.LoadPreset(path); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := o.Parameter(1); v != 0.75 || o.TakeRestarts()&RestartParamValuesChanged == 0 {
		t.Fatalf("preset load: value %v", v)
	}
	r.host.SendMessage("hello", []byte{1})
	if m := o.TakeMessages(); len(m) != 1 || m[0].ID != "hello" || len(o.TakeMessages()) != 0 {
		t.Fatalf("messages = %+v", m)
	}
}

type passPlugin struct{}

func (passPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{ID: "com.example.pass", Name: "Pass", Vendor: "Acme"}
}

func (passPlugin) CreateProcessor() Processor {
	return frameworkplugin.NewSimpleProcessor(bus.NewEffectStereoSidechain(), func(ctx *process.Context) {
		ctx.PassThrough()
	})
}

func TestOfflineProcessDoesNotAllocate(t *testing.T) {
	o, err := NewOffline(passPlugin{}, OfflineSetup{MaxBlockSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	in := [][]float32{make([]float32, 1000), make([]float32, 1000)}
	out := [][]float32{make([]float32, 1000), make([]float32, 1000)}
	for name, run := range map[string]func(){
		"full buffers":   func() { _ = o.Process(in, out) },
		"missing input":  func() { _ = o.Process(nil, out) },
		"missing output": func() { _ = o.Process(in[:1], nil) },
		"events only":    func() { _ = o.ProcessEvents(1000) },
		"with events": func() {
			o.SendEvent(midi.NoteOn(0, 60, 1).At(300))
			_ = o.Process(in, out)
		},
	} {
		if allocs := testing.AllocsPerRun(20, run); allocs != 0 {
			t.Errorf("%s: %v allocations per call", name, allocs)
		}
	}
}

func TestOfflineCompletesBusesWithoutTouchingCallerSlices(t *testing.T) {
	o, err := NewOffline(passPlugin{}, OfflineSetup{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	left := []float32{1, 2, 3}
	// Spare capacity: completing the bus must not append into it.
	in := make([][]float32, 1, 4)
	in[0] = left
	out := [][]float32{make([]float32, 3), make([]float32, 3)}
	if err := o.Process(in, out); err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[:2][1] != nil {
		t.Fatalf("caller input slice modified: %v", in[:2])
	}
	if out[0][2] != 3 || out[1][2] != 0 {
		t.Fatalf("out = %v, want the left channel and a silent right channel", out)
	}

	// A previous call's audio must not leak into the silent buffers.
	if err := o.Process([][]float32{{9, 9, 9}}, out); err != nil {
		t.Fatal(err)
	}
	if out[1][0] != 0 {
		t.Fatalf("silent channel carries old audio: %v", out[1])
	}
}
