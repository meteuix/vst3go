package main

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/ara"
	"github.com/th-release/vst3go/v2/pkg/plugin"
	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

const rate = 48000

// song is a document with one stereo recording ("take") of frames
// frames, one clip of it ("clip") and a region playing the clip's
// 0.5..0.75 s at 1 s in the song.
type song struct {
	host   *plugintest.ARAHost
	doc    *ara.Document
	source *ara.AudioSource
	clip   *ara.AudioModification
	region *ara.PlaybackRegion
	audio  [][]float32
}

func newSong(t *testing.T, audio [][]float32) *song {
	t.Helper()
	s := &song{host: plugintest.NewARAHost(), audio: audio}
	s.host.SetAudio("take", audio)
	s.doc = ara.NewDocument(araClipPlugin{}.ARA(), s.host, "song")
	s.doc.BeginEditing()
	track := s.doc.CreateRegionSequence(1, ara.RegionSequenceProperties{Name: "Track"})
	s.source = s.doc.CreateAudioSource(2, ara.AudioSourceProperties{
		Name: "Take", PersistentID: "take", Samples: int64(len(audio[0])), SampleRate: rate, Channels: len(audio),
	})
	s.clip = s.source.CreateAudioModification(3, ara.AudioModificationProperties{Name: "Clip", PersistentID: "clip"})
	s.region = s.clip.CreatePlaybackRegion(4, ara.PlaybackRegionProperties{
		StartInModification: 0.5, DurationInModification: 0.25,
		StartInPlayback: 1, DurationInPlayback: 0.25, Sequence: track,
	})
	s.doc.EndEditing()
	s.source.EnableAccess(true)
	return s
}

// ramp returns channels whose frame i holds a distinct value.
func ramp(frames int) [][]float32 {
	out := make([][]float32, 2)
	for ch := range out {
		out[ch] = make([]float32, frames)
		for i := range out[ch] {
			out[ch][i] = float32(ch+1) * float32(i) / float32(frames)
		}
	}
	return out
}

// bound returns an instance bound to the song, rendering its region.
func bound(t *testing.T, s *song) *plugintest.Instance {
	t.Helper()
	p := plugintest.New(t, araClipPlugin{}, plugin.OfflineSetup{ARA: s.doc})
	p.ARA().AddPlaybackRegion(s.region)
	plugintest.WaitARA(t, s.source)
	return p
}

// renderAt renders n frames of the song from sample position at.
func renderAt(p *plugintest.Instance, at int64, n int) [][]float32 {
	p.Transport.IsPlaying = true
	p.Transport.ProjectTimeSamples = at
	return p.RenderSilence(n, 2, 2)
}

func TestRendersClipAtItsPosition(t *testing.T) {
	s := newSong(t, ramp(rate*2))
	p := bound(t, s)

	// Song time 1 s (frame 48000) plays clip time 0.5 s (frame 24000).
	out := renderAt(p, rate-100, 512)
	for ch := range out {
		for i, v := range out[ch] {
			want := float32(0)
			if i >= 100 {
				want = s.audio[ch][24000+i-100]
			}
			if v != want {
				t.Fatalf("channel %d frame %d = %v, want %v", ch, i, v, want)
			}
		}
	}

	// The region ends after 0.25 s: 12000 frames.
	out = renderAt(p, rate+12000-10, 64)
	if out[0][9] != s.audio[0][24000+11999] || out[0][10] != 0 {
		t.Fatalf("region end: %v, %v", out[0][9], out[0][10])
	}

	p.Transport.IsPlaying = false
	if out := p.RenderSilence(512, 2, 2); plugintest.Peak(out[0]) != 0 {
		t.Fatal("rendered while stopped")
	}
}

func TestEditorSetsGainAndReverse(t *testing.T) {
	s := newSong(t, ramp(rate*2))
	p := bound(t, s)
	edit := func(gainDB float64, reverse bool) {
		t.Helper()
		if err := p.EditorMessage("set", map[string]any{"id": "clip", "gainDB": gainDB, "reverse": reverse}); err != nil {
			t.Fatal(err)
		}
	}

	edit(-6, false)
	g := float32(math.Pow(10, -6.0/20))
	out := renderAt(p, rate, 256)
	for i, v := range out[1] {
		if want := g * s.audio[1][24000+i]; math.Abs(float64(v-want)) > 1e-6 {
			t.Fatalf("gain: frame %d = %v, want %v", i, v, want)
		}
	}

	// Reversed, the region's first frame plays the clip range's last.
	edit(0, true)
	out = renderAt(p, rate, 256)
	for i, v := range out[0] {
		if want := s.audio[0][24000+11999-i]; v != want {
			t.Fatalf("reverse: frame %d = %v, want %v", i, v, want)
		}
	}
	if s.clip.PreservesSignal() {
		t.Fatal("a reversed clip preserves the signal")
	}

	// The edit reaches the host as a content change of the clip.
	s.doc.NotifyModelUpdates()
	found := false
	for _, n := range s.host.TakeNotifications() {
		found = found || n.Kind == "modification" && n.ID == "clip"
	}
	if !found {
		t.Fatal("the host was not told about the edit")
	}

	// Gains outside the range are clamped.
	edit(40, false)
	if c := clipOf(s.clip.State()); c.GainDB != maxGainDB {
		t.Fatalf("gain %v, want the maximum %v", c.GainDB, maxGainDB)
	}
}

func TestEditorListsClips(t *testing.T) {
	s := newSong(t, ramp(rate))
	p := bound(t, s)
	if err := p.EditorMessage("state", nil); err != nil {
		t.Fatal(err)
	}
	msgs := p.TakeEditorMessages()
	if len(msgs) != 1 || msgs[0].Name != "state" {
		t.Fatalf("messages %v", msgs)
	}
	var st editorState
	if err := json.Unmarshal(msgs[0].Data, &st); err != nil {
		t.Fatal(err)
	}
	if !st.ARA || len(st.Clips) != 1 || st.Clips[0].ID != "clip" || st.Clips[0].Source != "Take" || st.Clips[0].GainDB != 0 {
		t.Fatalf("state %+v", st)
	}
}

func TestClipsAreSavedInTheSong(t *testing.T) {
	s := newSong(t, ramp(rate))
	s.clip.SetState(newClip(-12, true))
	data, err := s.doc.Store(nil)
	if err != nil {
		t.Fatal(err)
	}

	// The host reopens the song: it rebuilds the model and restores.
	s2 := newSong(t, ramp(rate))
	s2.doc.BeginEditing()
	err = s2.doc.Restore(data, nil)
	s2.doc.EndEditing()
	if err != nil {
		t.Fatal(err)
	}
	if c := clipOf(s2.clip.State()); c.GainDB != -12 || !c.Reverse || c.gain != newClip(-12, true).gain {
		t.Fatalf("restored %+v", c)
	}

	// An untouched clip stores nothing and preserves the signal.
	s3 := newSong(t, ramp(rate))
	if !s3.clip.PreservesSignal() {
		t.Fatal("an untouched clip changes the signal")
	}
	if data, err := (&controller{}).StoreModification(s3.clip); data != nil || err != nil {
		t.Fatalf("stored %q, %v for an untouched clip", data, err)
	}
}

func TestAnalyzesNotes(t *testing.T) {
	// 0.5 s of silence, then 1 s of A4 (MIDI 69), then silence.
	audio := plugintest.Silence(2, rate*2)
	tone := plugintest.Sine(440, rate, rate, 0.5)
	for ch := range audio {
		copy(audio[ch][rate/2:], tone)
	}
	s := newSong(t, audio)
	plugintest.WaitARA(t, s.source)
	c := s.source.Content()
	if c == nil || len(c.Notes) != 1 {
		t.Fatalf("notes %+v", c)
	}
	n := c.Notes[0]
	if n.Pitch != 69 || math.Abs(float64(n.Frequency)-440) > 2 || math.Abs(n.Start-0.5) > 0.05 || math.Abs(n.Duration-1) > 0.05 {
		t.Fatalf("note %+v, want A4 from 0.5 s for 1 s", n)
	}
	if !c.Has(ara.ContentNotes) || c.Grade != ara.GradeDetected {
		t.Fatalf("content %+v", c)
	}

	// The region plays 0.5..0.75 s of the clip at 1 s: the note's start,
	// moved to song time.
	rc := s.region.Content()
	if len(rc.Notes) != 1 || math.Abs(rc.Notes[0].Start-1) > 0.05 {
		t.Fatalf("region notes %+v", rc.Notes)
	}
}

func TestPassesThroughWithoutARA(t *testing.T) {
	p := plugintest.New(t, araClipPlugin{})
	in := plugintest.Channels(2, plugintest.Sine(1000, rate, 1024, 0.5))
	out := p.Render(in, 2)
	for ch := range out {
		for i := range out[ch] {
			if out[ch][i] != in[ch][i] {
				t.Fatalf("channel %d frame %d = %v, want %v", ch, i, out[ch][i], in[ch][i])
			}
		}
	}
}

func TestRenderDoesNotAllocate(t *testing.T) {
	s := newSong(t, ramp(rate*2))
	p := bound(t, s)
	p.Transport.IsPlaying = true
	out := plugintest.Silence(2, 512)
	if allocs := p.AllocsPerProcess(100, plugintest.Silence(2, 512), out, func() {
		p.Transport.ProjectTimeSamples = rate
	}); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
