// Command sampler plays an audio file across the keyboard: 16 voices with
// band-limited pitch shifting, the file's loop (WAV smpl / AIFF markers)
// and an ADSR envelope. Until a file is loaded it plays a built-in
// harmonic tone. The editor page takes a file path (WAV, AIFF, FLAC, MP3
// or Ogg Vorbis); the file is decoded on a background goroutine and
// swapped in atomically, so the audio thread never waits for it, and the
// path is saved with the plugin state and reloaded with it.
//
// Build: scripts/build_vst3.sh examples/sampler Sampler
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/audiofile"
	"github.com/th-release/vst3go/pkg/dsp/sampler"
	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/framework/voice"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

//go:embed editor.html
var editorHTML string

const (
	paramAttack uint32 = iota
	paramDecay
	paramSustain
	paramRelease
	paramTune
	paramVolume
)

const (
	numVoices   = 16
	defaultRoot = 60      // root note of files without one
	maxSeconds  = 10 * 60 // longest file loaded
	readChunk   = 1 << 16 // frames decoded per read
)

type samplerPlugin struct{}

func (samplerPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.sampler",
		Name:     "Sampler",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryInstrumentSampler,
	}
}

func (samplerPlugin) CreateProcessor() plugin.Processor {
	p := &samplerProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewGenerator())}
	p.Parameters().Add(
		param.New(paramAttack, "Attack").Range(0.001, 2).Default(0.002).Unit("s").Build(),
		param.New(paramDecay, "Decay").Range(0.001, 4).Default(0.3).Unit("s").Build(),
		param.New(paramSustain, "Sustain").Range(0, 100).Default(100).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Build(),
		param.New(paramRelease, "Release").Range(0.001, 5).Default(0.3).Unit("s").Build(),
		param.New(paramTune, "Tune").Range(-24, 24).Default(0).Unit("st").Build(),
		param.New(paramVolume, "Volume").Range(0, 100).Default(70).Unit("%").
			Formatter(param.PercentFormatter, param.PercentParser).Smooth(0.01).Build(),
	)
	p.instrument.Store(defaultInstrument())
	p.status.Store(&loadStatus{})
	p.OnInitialize(p.prepare)
	p.OnReset(func() { p.allocator.Reset() })
	return p
}

// loadStatus is what the editor shows about the loaded file.
type loadStatus struct {
	Path     string  `json:"path"`
	Loading  bool    `json:"loading"`
	Error    string  `json:"error,omitempty"`
	Seconds  float64 `json:"seconds"`
	Channels int     `json:"channels"`
	RootNote float64 `json:"rootNote"`
	Looped   bool    `json:"looped"`
}

type hostRef struct{ plugin.Host }

type samplerProcessor struct {
	*frameworkplugin.BaseProcessor
	instrument atomic.Pointer[sampler.Map]
	voices     [numVoices]*sampler.Voice
	allocator  *voice.Allocator
	env        [4]float64
	tune       float64

	host   atomic.Pointer[hostRef]
	status atomic.Pointer[loadStatus]
	// mu orders loads: only the newest one may publish its result.
	mu   sync.Mutex
	path string
	gen  int
}

func (p *samplerProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

func (p *samplerProcessor) prepare(sampleRate float64, maxBlockSize int32) error {
	vs := make([]voice.Voice, numVoices)
	for i := range p.voices {
		p.voices[i] = sampler.NewVoice(sampleRate, &p.instrument)
		vs[i] = p.voices[i]
	}
	p.allocator = voice.NewAllocator(vs)
	p.env = [4]float64{-1}
	p.tune = math.NaN()
	return nil
}

// MIDIControllerChannels delivers the sustain pedal and All Notes Off.
func (p *samplerProcessor) MIDIControllerChannels() int { return 1 }

func (p *samplerProcessor) GetTailSamples() int32 { return int32(5 * p.SampleRate()) }

func (p *samplerProcessor) ProcessAudio(ctx *process.Context) {
	n := ctx.NumSamples()
	if len(ctx.Output) == 0 || n == 0 {
		return
	}
	ctx.Clear()
	env := [4]float64{
		ctx.ParamPlain(paramAttack), ctx.ParamPlain(paramDecay),
		ctx.ParamPlain(paramSustain) / 100, ctx.ParamPlain(paramRelease),
	}
	tune := ctx.ParamPlain(paramTune)
	if env != p.env || tune != p.tune {
		p.env, p.tune = env, tune
		for _, v := range p.voices {
			v.SetADSR(env[0], env[1], env[2], env[3])
			v.SetPitchBend(tune)
		}
	}

	left := ctx.Output[0][:n]
	var right []float32
	if len(ctx.Output) > 1 {
		right = ctx.Output[1][:n]
	}
	events := ctx.GetAllInputEvents()
	offset := ctx.BlockOffset()
	next := 0
	for start := 0; start < n; {
		for next < len(events) && int(events[next].Offset)-offset <= start {
			p.allocator.ProcessEvent(events[next])
			next++
		}
		end := n
		if next < len(events) {
			end = min(int(events[next].Offset)-offset, n)
		}
		var r []float32
		if right != nil {
			r = right[start:end]
		}
		for _, v := range p.voices {
			v.Render(left[start:end], r)
		}
		start = end
	}
	for ; next < len(events); next++ {
		p.allocator.ProcessEvent(events[next])
	}
	volume := ctx.Smoothed(paramVolume)
	for i := range left {
		g := float32(volume.Next() / 100)
		left[i] *= g
		if right != nil {
			right[i] *= g
		}
	}
	for ch := 2; ch < len(ctx.Output); ch++ {
		clear(ctx.Output[ch])
	}
}

// SaveCustomState saves the path of the loaded file.
func (p *samplerProcessor) SaveCustomState(w io.Writer) error {
	p.mu.Lock()
	path := p.path
	p.mu.Unlock()
	_, err := io.WriteString(w, path)
	return err
}

// LoadCustomState reloads the saved file, or the built-in tone when the
// state has no path.
func (p *samplerProcessor) LoadCustomState(r io.Reader) error {
	path, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	p.load(string(path))
	return nil
}

// editorMessage is the data of the page's "load" message.
type editorMessage struct {
	Path string `json:"path"`
}

// EditorMessage loads the file the page names ("load") and answers its
// status polls ("status").
func (p *samplerProcessor) EditorMessage(name string, data json.RawMessage) {
	switch name {
	case "load":
		var m editorMessage
		if err := json.Unmarshal(data, &m); err != nil {
			p.status.Store(&loadStatus{Error: err.Error()})
			return
		}
		p.load(m.Path)
	case "status":
		if host := p.host.Load(); host != nil {
			_ = host.PostEditorMessage("status", p.status.Load())
		}
	}
}

// load starts decoding path in the background ("" restores the built-in
// tone). Voices keep playing the current instrument until the new one is
// ready; a failed load leaves it in place.
func (p *samplerProcessor) load(path string) {
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.mu.Unlock()
	if path == "" {
		p.publish(gen, "", defaultInstrument(), &loadStatus{})
		return
	}
	p.status.Store(&loadStatus{Path: path, Loading: true})
	go func() {
		m, st, err := loadInstrument(path)
		if err != nil {
			p.publish(gen, "", nil, &loadStatus{Path: path, Error: err.Error()})
			return
		}
		p.publish(gen, path, m, st)
	}()
}

// publish installs a finished load unless a newer one has started.
func (p *samplerProcessor) publish(gen int, path string, m *sampler.Map, st *loadStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if gen != p.gen {
		return
	}
	if m != nil {
		p.instrument.Store(m)
		p.path = path
	}
	p.status.Store(st)
}

// loadInstrument decodes an audio file into a one-sample instrument
// spanning the keyboard.
func loadInstrument(path string) (*sampler.Map, *loadStatus, error) {
	r, err := audiofile.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	info := r.Info()
	if info.Channels < 1 || !(info.SampleRate > 0) {
		return nil, nil, fmt.Errorf("%s: no audio", filepath.Base(path))
	}
	limit := int(maxSeconds * info.SampleRate)
	data := make([][]float32, info.Channels)
	chunk := make([][]float32, info.Channels)
	for ch := range chunk {
		chunk[ch] = make([]float32, readChunk)
	}
	for len(data[0]) < limit {
		n, err := r.Read(chunk)
		for ch := range data {
			data[ch] = append(data[ch], chunk[ch][:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
	}
	if len(data[0]) == 0 {
		return nil, nil, fmt.Errorf("%s: no audio", filepath.Base(path))
	}
	if len(data[0]) > limit {
		return nil, nil, fmt.Errorf("%s: longer than %d minutes", filepath.Base(path), maxSeconds/60)
	}
	root := float64(defaultRoot)
	if info.RootNote >= 0 {
		root = float64(info.RootNote)
	}
	s, err := sampler.NewSample(data[:min(2, len(data))], info.SampleRate, root)
	if err != nil {
		return nil, nil, err
	}
	if len(info.Loops) > 0 {
		s.SetLoop(int(info.Loops[0].Start), int(info.Loops[0].End))
	}
	m, err := keyboard(s)
	if err != nil {
		return nil, nil, err
	}
	return m, &loadStatus{
		Path:     path,
		Seconds:  float64(s.Len()) / s.SampleRate(),
		Channels: s.Channels(),
		RootNote: root,
		Looped:   s.Looped(),
	}, nil
}

// keyboard maps s to every key and velocity.
func keyboard(s *sampler.Sample) (*sampler.Map, error) {
	return sampler.NewMap(sampler.Zone{Sample: s, LowKey: 0, HighKey: 127, LowVelocity: 1, HighVelocity: 127})
}

// defaultInstrument is a looped harmonic tone (eight harmonics falling at
// 6 dB per octave) whose period is exactly 184 samples at 48 kHz, so the
// loop over whole periods is seamless.
var defaultInstrument = sync.OnceValue(func() *sampler.Map {
	const rate, period, periods = 48000, 184, 200
	freq := float64(rate) / period
	x := make([]float32, period*periods)
	for i := range x {
		v := 0.0
		for h := 1; h <= 8; h++ {
			v += math.Sin(2*math.Pi*freq*float64(h)*float64(i)/rate) / float64(h)
		}
		x[i] = float32(0.4 * v)
	}
	s, err := sampler.NewSample([][]float32{x}, rate, 69+12*math.Log2(freq/440))
	if err != nil {
		panic(err)
	}
	s.SetLoop(period*(periods/2), len(x))
	m, err := keyboard(s)
	if err != nil {
		panic(err)
	}
	return m
})

// Editor is the page with the file path field and the parameters.
func (p *samplerProcessor) Editor() plugin.Editor {
	return plugin.Editor{HTML: editorHTML, Width: 640, Height: 360, MinWidth: 480, MinHeight: 300}
}

func init() {
	plugin.Register(samplerPlugin{})
}

func main() { standalone.Main() }
