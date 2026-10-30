// Command araclip is an ARA clip editor: in an ARA host (Studio One,
// Cubase, Logic, REAPER and others) it renders the host's clips itself,
// from the whole recording, with a gain and a reverse switch per clip
// (audio modification) that are saved in the host's song. It analyzes the
// notes of every recording, which the host reads through ARA (for example
// to show them or to make MIDI from them). The editor lists the clips of
// the song with their controls and the notes found. Outside an ARA host
// it passes its input through.
//
// Build: scripts/build_vst3.sh examples/araclip ARAClip
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/ara"
	"github.com/th-release/vst3go/pkg/dsp/pitch"
	"github.com/th-release/vst3go/pkg/framework/bus"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/standalone"
)

//go:embed editor.html
var editorHTML string

// Gain range of a clip, in dB.
const (
	minGainDB = -60
	maxGainDB = 12
)

type araClipPlugin struct{}

func (araClipPlugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.vst3go.examples.araclip",
		Name:     "ARA Clip",
		Version:  "1.0.0",
		Vendor:   "VST3Go Examples",
		Category: frameworkplugin.CategoryFx,
	}
}

func (araClipPlugin) ARA() ara.Config {
	return ara.Config{
		ArchiveID:     "com.vst3go.examples.araclip.1",
		ContentTypes:  []ara.ContentType{ara.ContentNotes},
		NewController: func(d *ara.Document) ara.Controller { return &controller{} },
	}
}

func (araClipPlugin) CreateProcessor() plugin.Processor {
	return &araClipProcessor{BaseProcessor: frameworkplugin.NewBaseProcessor(bus.NewEffectStereo())}
}

// clip is the state of an audio modification. It is never changed: a
// new one replaces it.
type clip struct {
	GainDB  float64 `json:"gainDB"`
	Reverse bool    `json:"reverse"`
	gain    float32 // linear
}

func newClip(gainDB float64, reverse bool) *clip {
	gainDB = max(minGainDB, min(maxGainDB, gainDB))
	return &clip{GainDB: gainDB, Reverse: reverse, gain: float32(math.Pow(10, gainDB/20))}
}

// unity is the state of a modification the user did not change.
var unity = newClip(0, false)

func clipOf(state any) *clip {
	if c, ok := state.(*clip); ok {
		return c
	}
	return unity
}

// controller is the plugin's side of a document: it stores the clips in
// the song and finds the notes of the recordings.
type controller struct{}

func (*controller) StoreModification(m *ara.AudioModification) ([]byte, error) {
	if m.State() == nil {
		return nil, nil
	}
	return json.Marshal(clipOf(m.State()))
}

func (*controller) RestoreModification(m *ara.AudioModification, data []byte) error {
	var c clip
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	m.SetState(newClip(c.GainDB, c.Reverse))
	return nil
}

func (*controller) PreservesSignal(m *ara.AudioModification) bool {
	c := clipOf(m.State())
	return c.GainDB == 0 && !c.Reverse
}

// Note analysis: the pitch of the mono mix every hop; a note is a run of
// clear, steady pitch.
const (
	hopSeconds   = 0.005
	minClarity   = 0.9
	minNoteHops  = 10 // 50 ms
	minFrequency = 50
	maxFrequency = 2000
)

func (*controller) Analyze(s *ara.AudioSource, audio *ara.SourceAudio, stopped func() bool) (*ara.Content, error) {
	rate := audio.SampleRate()
	hop := max(1, int(rate*hopSeconds))
	d, err := pitch.NewDetector(rate, minFrequency, min(maxFrequency, rate/4-1), hop)
	if err != nil {
		return nil, err
	}
	mono := make([]float32, hop)
	notes := []ara.Note{}
	var run struct {
		pitch      int32
		start, n   int
		freq, peak float64
	}
	end := func() {
		if run.n >= minNoteHops {
			notes = append(notes, ara.Note{
				Frequency:      float32(run.freq / float64(run.n)),
				Pitch:          run.pitch,
				Volume:         float32(min(1, run.peak)),
				Start:          float64(run.start) / rate,
				Duration:       float64(run.n*hop) / rate,
				SignalDuration: float64(run.n*hop) / rate,
			})
		}
		run.n = 0
	}
	channels := audio.Channels()
	for pos := 0; pos+hop <= audio.Frames(); pos += hop {
		if stopped() {
			return nil, errors.New("araclip: analysis stopped")
		}
		clear(mono)
		var peak float64
		for ch := range channels {
			for i, x := range audio.Channel(ch)[pos : pos+hop] {
				mono[i] += x / float32(channels)
			}
		}
		for _, x := range mono {
			peak = max(peak, math.Abs(float64(x)))
		}
		d.Process(mono)
		freq, clarity := d.Pitch()
		if freq <= 0 || clarity < minClarity {
			end()
			continue
		}
		p := int32(math.Round(69 + 12*math.Log2(freq/440)))
		if run.n > 0 && p != run.pitch {
			end()
		}
		if run.n == 0 {
			run.pitch, run.start, run.freq, run.peak = p, pos, 0, 0
		}
		run.n++
		run.freq += freq
		run.peak = max(run.peak, peak)
	}
	end()
	return &ara.Content{Grade: ara.GradeDetected, Notes: notes}, nil
}

type hostRef struct{ plugin.Host }

type araClipProcessor struct {
	*frameworkplugin.BaseProcessor
	binding atomic.Pointer[ara.Binding]
	host    atomic.Pointer[hostRef]
}

// BindARA switches the instance to rendering the host's clips.
func (p *araClipProcessor) BindARA(b *ara.Binding) { p.binding.Store(b) }

func (p *araClipProcessor) SetHost(host plugin.Host) {
	if host == nil {
		p.host.Store(nil)
		return
	}
	p.host.Store(&hostRef{host})
}

func (p *araClipProcessor) ProcessAudio(ctx *process.Context) {
	b := p.binding.Load()
	if b == nil {
		// Not in an ARA host: pass the input through. The host may
		// process in place, so read each input before writing.
		for ch, y := range ctx.Output {
			if len(ctx.Input) == 0 {
				clear(y)
				continue
			}
			copy(y, ctx.Input[min(ch, len(ctx.Input)-1)])
		}
		return
	}
	ctx.Clear()
	if !ctx.Transport.IsPlaying || len(ctx.Output) == 0 {
		return
	}
	at := ctx.Transport.ProjectTimeSamples + int64(ctx.BlockOffset())
	n := ctx.NumSamples()
	pb := b.Playback()
	for i := range pb.Regions {
		r := &pb.Regions[i]
		audio := r.Source.Audio()
		if audio == nil {
			continue
		}
		c := clipOf(r.State)
		from, to, pos, step := r.Span(at, n, ctx.SampleRate, audio.SampleRate())
		if c.Reverse {
			// Play the modification range backwards: its first frame
			// plays its last sample.
			first := r.StartInModification * audio.SampleRate()
			last := first + r.DurationInModification*audio.SampleRate() - 1
			pos, step = first+last-pos, -step
		}
		r.MixSpan(ctx.Output, from, to, pos, step, c.gain)
	}
}

// clipView is a clip as the editor shows it.
type clipView struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	GainDB    float64 `json:"gainDB"`
	Reverse   bool    `json:"reverse"`
	Notes     int     `json:"notes"`
	Analyzing bool    `json:"analyzing"`
}

type editorState struct {
	ARA   bool       `json:"ara"`
	Clips []clipView `json:"clips"`
}

// EditorMessage answers the page's polls ("state") and applies its edits
// ("set"); both run on the main thread, which owns the ARA document.
func (p *araClipProcessor) EditorMessage(name string, data json.RawMessage) {
	b := p.binding.Load()
	switch name {
	case "state":
		if host := p.host.Load(); host != nil {
			_ = host.PostEditorMessage("state", p.state(b))
		}
	case "set":
		var m struct {
			ID      string  `json:"id"`
			GainDB  float64 `json:"gainDB"`
			Reverse bool    `json:"reverse"`
		}
		if b == nil || json.Unmarshal(data, &m) != nil {
			return
		}
		if mod := findModification(b.Document(), m.ID); mod != nil {
			mod.SetState(newClip(m.GainDB, m.Reverse))
		}
	}
}

func (p *araClipProcessor) state(b *ara.Binding) editorState {
	if b == nil {
		return editorState{Clips: []clipView{}}
	}
	st := editorState{ARA: true, Clips: []clipView{}}
	for _, s := range b.Document().AudioSources() {
		notes := 0
		if c := s.Content(); c != nil {
			notes = len(c.Notes)
		}
		for _, m := range s.AudioModifications() {
			c := clipOf(m.State())
			st.Clips = append(st.Clips, clipView{
				ID:        m.Properties().PersistentID,
				Name:      m.Properties().Name,
				Source:    s.Properties().Name,
				GainDB:    c.GainDB,
				Reverse:   c.Reverse,
				Notes:     notes,
				Analyzing: s.AnalysisIncomplete(ara.ContentNotes),
			})
		}
	}
	return st
}

func findModification(d *ara.Document, id string) *ara.AudioModification {
	for _, s := range d.AudioSources() {
		for _, m := range s.AudioModifications() {
			if m.Properties().PersistentID == id {
				return m
			}
		}
	}
	return nil
}

// Editor is the clip list.
func (p *araClipProcessor) Editor() plugin.Editor {
	return plugin.Editor{HTML: editorHTML, Width: 640, Height: 360, MinWidth: 480, MinHeight: 240}
}

func init() {
	plugin.Register(araClipPlugin{})
}

func main() { standalone.Main() }
