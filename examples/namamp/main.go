// Command namamp is an amp and pedal simulator that runs Neural Amp
// Modeler captures (.nam files: WaveNet, LSTM or Linear) with pkg/dsp/nam.
// The input channels are mixed to mono, driven into the model and the
// result goes to every output; models trained at another sample rate are
// resampled, which adds latency (announced to the host). Normalize
// levels models that state their loudness to NAM's -18 dB reference. The
// editor page takes the path of a .nam file, typed or picked with
// vst3go.chooseFile; it is parsed on a background
// goroutine and swapped in atomically, so the audio thread never waits
// for it, and the path is saved with the plugin state and reloaded with
// it. Without a model the input passes through the gains.
//
// Build: scripts/build_vst3.sh examples/namamp NAMAmp
package main

import (
	_ "embed"
	"encoding/json"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/meteuix/vst3go/v2/pkg/dsp/nam"
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/standalone"
)

//go:embed editor.html
var editorHTML string

const (
	paramInput uint32 = iota
	paramOutput
	paramNormalize
)

// referenceLoudness is the level, in dB, NAM normalizes models to.
const referenceLoudness = -18

type namAmpPlugin struct{}

func (namAmpPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.namamp",
		Name:     "NAM Amp",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFxDistortion,
	}
}

func (namAmpPlugin) CreateProcessor() plugin.Processor {
	p := &namAmpProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
	p.Parameters().Add(
		param.New(paramInput, "Input").Range(-24, 24).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.New(paramOutput, "Output").Range(-40, 12).Default(0).Unit("dB").
			Formatter(param.DecibelFormatter, param.DecibelParser).Smooth(0.02).Build(),
		param.Choice(paramNormalize, "Normalize", []param.ChoiceOption{
			{Value: 0, Name: "Off"},
			{Value: 1, Name: "On"},
		}).Default(1).Build(),
	)
	p.status.Store(&loadStatus{})
	p.OnInitialize(p.prepare)
	p.OnReset(func() {
		if e := p.engine.Load(); e != nil {
			e.runner.Reset()
		}
	})
	return p
}

// engine is a loaded model ready to run at the host's sample rate.
type engine struct {
	runner *nam.Runner
	// level is the gain that brings the model to the reference loudness,
	// 1 when the file does not state its loudness.
	level float32
}

// loadStatus is what the editor shows about the loaded model.
type loadStatus struct {
	Path         string   `json:"path"`
	Loading      bool     `json:"loading"`
	Error        string   `json:"error,omitempty"`
	Name         string   `json:"name,omitempty"`
	Gear         string   `json:"gear,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	SampleRate   float64  `json:"sampleRate,omitempty"`
	Loudness     *float64 `json:"loudness,omitempty"`
	Latency      int      `json:"latency"`
}

type hostRef struct{ plugin.Host }

type namAmpProcessor struct {
	*frameworkplugin.BaseProcessor
	engine atomic.Pointer[engine]

	// Audio-thread scratch, one block long.
	mono, in, out []float32
	inDB, outDB   float32 // the dB values in and out were computed for
	inGain        float32
	outGain       float32

	host     atomic.Pointer[hostRef]
	status   atomic.Pointer[loadStatus]
	reported int32 // latency last announced to the host

	// mu orders loads (only the newest may publish) and guards the model
	// against a sample rate change.
	mu   sync.Mutex
	path string
	file *nam.File
	rate float64
	gen  int
}

func (p *namAmpProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

// prepare sizes the scratch buffers and rebuilds the model's engine for
// the sample rate.
func (p *namAmpProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	n := max(int(maxBlockSize), 1)
	p.mono, p.in, p.out = make([]float32, n), make([]float32, n), make([]float32, n)
	p.inDB, p.outDB = float32(math.NaN()), float32(math.NaN())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rate = sampleRate
	if p.file == nil {
		return nil
	}
	e, err := newEngine(p.file, sampleRate)
	if err != nil {
		return err
	}
	p.engine.Store(e)
	st := *p.status.Load()
	st.Latency = e.runner.Latency()
	p.status.Store(&st)
	return nil
}

func newEngine(f *nam.File, sampleRate float64) (*engine, error) {
	m, err := f.New()
	if err != nil {
		return nil, err
	}
	r, err := nam.NewRunner(m, sampleRate)
	if err != nil {
		return nil, err
	}
	e := &engine{runner: r, level: 1}
	if l := f.Metadata.Loudness; l != nil {
		e.level = float32(math.Pow(10, (referenceLoudness-*l)/20))
	}
	return e, nil
}

// SetActive announces the latency when processing starts if a model with
// another latency was loaded since the last time.
func (p *namAmpProcessor) SetActive(active bool) error {
	if active {
		p.announceLatency()
	}
	return p.BaseProcessor.SetActive(active)
}

func (p *namAmpProcessor) announceLatency() {
	if latency := p.GetLatencySamples(); latency != p.reported {
		p.reported = latency
		if host := p.host.Load(); host != nil {
			_ = host.RestartComponent(plugin.RestartLatencyChanged)
		}
	}
}

func (p *namAmpProcessor) GetLatencySamples() int32 {
	if e := p.engine.Load(); e != nil {
		return int32(e.runner.Latency())
	}
	return 0
}

// GetTailSamples covers the resampling delay and a second of amp and
// cabinet response.
func (p *namAmpProcessor) GetTailSamples() int32 {
	return p.GetLatencySamples() + int32(p.SampleRate())
}

func gain(db float32) float32 { return float32(math.Pow(10, float64(db)/20)) }

func (p *namAmpProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Input) == 0 || len(ctx.Output) == 0 || n == 0 {
		ctx.Clear()
		return
	}
	e := p.engine.Load()
	level := float32(1)
	if e != nil && ctx.ParamPlain(paramNormalize) >= 0.5 {
		level = e.level
	}
	inRamp, outRamp := ctx.Smoothed(paramInput), ctx.Smoothed(paramOutput)
	scale := 1 / float32(len(ctx.Input))
	for start := 0; start < n; start += len(p.mono) {
		k := min(n-start, len(p.mono))
		in, out := p.in[:k], p.out[:k]
		inRamp.Fill(in)
		outRamp.Fill(out)
		for i, db := range in {
			if db != p.inDB {
				p.inDB, p.inGain = db, gain(db)
			}
			in[i] = p.inGain
		}
		for i, db := range out {
			if db != p.outDB {
				p.outDB, p.outGain = db, gain(db)
			}
			out[i] = p.outGain * level
		}
		if e == nil {
			// Read each input before writing its output: the host may
			// process in place.
			for ch, y := range ctx.Output {
				y = y[start : start+k]
				x := ctx.Input[min(ch, len(ctx.Input)-1)][start : start+k]
				for i := range y {
					y[i] = x[i] * in[i] * out[i]
				}
			}
			continue
		}
		mono := p.mono[:k]
		clear(mono)
		for _, x := range ctx.Input {
			for i, v := range x[start : start+k] {
				mono[i] += v
			}
		}
		for i := range mono {
			mono[i] *= scale * in[i]
		}
		e.runner.Process(mono, mono)
		for _, y := range ctx.Output {
			y = y[start : start+k]
			for i := range y {
				y[i] = mono[i] * out[i]
			}
		}
	}
}

// SaveCustomState saves the path of the loaded model.
func (p *namAmpProcessor) SaveCustomState(w io.Writer) error {
	p.mu.Lock()
	path := p.path
	p.mu.Unlock()
	_, err := io.WriteString(w, path)
	return err
}

// LoadCustomState reloads the saved model, or none when the state has no
// path.
func (p *namAmpProcessor) LoadCustomState(r io.Reader) error {
	path, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	p.load(string(path))
	return nil
}

// EditorMessage loads the model the page names ("load") and answers its
// status polls ("status"), announcing a latency the new model changed.
func (p *namAmpProcessor) EditorMessage(name string, data json.RawMessage) {
	switch name {
	case "load":
		var m struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			p.status.Store(&loadStatus{Error: err.Error()})
			return
		}
		p.load(m.Path)
	case "status":
		p.announceLatency()
		if host := p.host.Load(); host != nil {
			_ = host.PostEditorMessage("status", p.status.Load())
		}
	}
}

// load starts parsing path in the background ("" removes the model). The
// current model keeps playing until the new one is ready; a failed load
// leaves it in place.
func (p *namAmpProcessor) load(path string) {
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.mu.Unlock()
	if path == "" {
		p.publish(gen, "", nil, nil)
		return
	}
	p.status.Store(&loadStatus{Path: path, Loading: true})
	go func() {
		f, err := nam.Load(path)
		p.publish(gen, path, f, err)
	}()
}

// publish installs a finished load unless a newer one has started.
func (p *namAmpProcessor) publish(gen int, path string, f *nam.File, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if gen != p.gen {
		return
	}
	if err != nil {
		p.status.Store(&loadStatus{Path: path, Error: err.Error()})
		return
	}
	if f == nil {
		p.engine.Store(nil)
		p.path, p.file = "", nil
		p.status.Store(&loadStatus{})
		return
	}
	st := &loadStatus{
		Path:         path,
		Name:         f.Metadata.Name,
		Gear:         strings.TrimSpace(f.Metadata.GearMake + " " + f.Metadata.GearModel),
		Architecture: f.Architecture,
		SampleRate:   f.SampleRate,
		Loudness:     f.Metadata.Loudness,
	}
	// Before the first prepare there is no sample rate; prepare builds the
	// engine then.
	if p.rate > 0 {
		e, err := newEngine(f, p.rate)
		if err != nil {
			p.status.Store(&loadStatus{Path: path, Error: err.Error()})
			return
		}
		p.engine.Store(e)
		st.Latency = e.runner.Latency()
	}
	p.path, p.file = path, f
	p.status.Store(st)
}

// Editor is the page with the model path field and the parameters.
func (p *namAmpProcessor) Editor() plugin.Editor {
	return plugin.Editor{HTML: editorHTML, Width: 600, Height: 300, MinWidth: 460, MinHeight: 260}
}

func init() {
	plugin.Register(namAmpPlugin{})
}

func main() { standalone.Main() }
