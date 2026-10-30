package ara

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testHost serves sources from memory and records notifications.
type testHost struct {
	mu      sync.Mutex
	audio   map[string][][]float32 // by persistent ID
	gate    chan struct{}          // when set, every Read waits for it
	reads   int
	closed  int
	tempo   []TempoEntry
	events  []string
	openErr error
}

type testReader struct {
	h    *testHost
	data [][]float32
}

func (h *testHost) OpenAudio(s *AudioSource) (AudioReader, error) {
	if h.openErr != nil {
		return nil, h.openErr
	}
	return &testReader{h, h.audio[s.Properties().PersistentID]}, nil
}

func (r *testReader) Read(pos int64, buffers [][]float32) error {
	r.h.mu.Lock()
	r.h.reads++
	gate := r.h.gate
	r.h.mu.Unlock()
	if gate != nil {
		<-gate
	}
	for ch, b := range buffers {
		copy(b, r.data[ch][pos:])
	}
	return nil
}

func (r *testReader) Close() {
	r.h.mu.Lock()
	r.h.closed++
	r.h.mu.Unlock()
}

func (h *testHost) MusicalContextContent(c *MusicalContext) ([]TempoEntry, []BarSignature) {
	return h.tempo, nil
}

func (h *testHost) record(format string, args ...any) {
	h.mu.Lock()
	h.events = append(h.events, fmt.Sprintf(format, args...))
	h.mu.Unlock()
}

func (h *testHost) AnalysisProgress(s *AudioSource, state AnalysisState, value float32) {
	h.record("progress %s %d", s.Properties().PersistentID, state)
}
func (h *testHost) SourceContentChanged(s *AudioSource, flags ContentUpdateFlags) {
	h.record("source %s %d", s.Properties().PersistentID, flags)
}
func (h *testHost) ModificationContentChanged(m *AudioModification, flags ContentUpdateFlags) {
	h.record("modification %s %d", m.Properties().PersistentID, flags)
}
func (h *testHost) RegionContentChanged(r *PlaybackRegion, flags ContentUpdateFlags) {
	h.record("region %d %d", r.HostRef(), flags)
}
func (h *testHost) DocumentDataChanged() { h.record("document") }

func (h *testHost) takeEvents() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.events
	h.events = nil
	return e
}

// ramp returns channels whose frame i holds ch*1000+i, so a read shows
// exactly which frame it came from.
func ramp(channels, frames int) [][]float32 {
	data := make([][]float32, channels)
	for ch := range data {
		data[ch] = make([]float32, frames)
		for i := range data[ch] {
			data[ch][i] = float32(ch*1000000 + i)
		}
	}
	return data
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// newTestDocument builds a document with one source (s1, 48 kHz, stereo
// ramp of 200000 frames), its modification m1 and a region of it.
func newTestDocument(t *testing.T, config Config) (*Document, *testHost, *AudioSource, *AudioModification, *PlaybackRegion) {
	t.Helper()
	h := &testHost{audio: map[string][][]float32{"s1": ramp(2, 200000)}}
	d := NewDocument(config, h, "song")
	d.BeginEditing()
	c := d.CreateMusicalContext(1, MusicalContextProperties{Name: "ctx"})
	q := d.CreateRegionSequence(2, RegionSequenceProperties{Name: "track", Context: c})
	s := d.CreateAudioSource(3, AudioSourceProperties{Name: "take", PersistentID: "s1", Samples: 200000, SampleRate: 48000, Channels: 2})
	m := s.CreateAudioModification(4, AudioModificationProperties{PersistentID: "m1"})
	r := m.CreatePlaybackRegion(5, PlaybackRegionProperties{
		StartInModification: 0.5, DurationInModification: 0.25,
		StartInPlayback: 1, DurationInPlayback: 0.25, Sequence: q,
	})
	d.EndEditing()
	return d, h, s, m, r
}

func TestPlaybackRendersRegions(t *testing.T) {
	d, _, s, _, r := newTestDocument(t, Config{ArchiveID: "com.example.test.1"})
	b := d.Bind(AllRoles, PlaybackRenderer)
	if b.Roles() != PlaybackRenderer {
		t.Fatalf("roles %v, want the assigned PlaybackRenderer", b.Roles())
	}
	b.AddPlaybackRegion(r)
	p := b.Playback()
	if len(p.Regions) != 1 || p.Regions[0].Region != r {
		t.Fatalf("playback %+v, want the region", p)
	}
	out := [][]float32{make([]float32, 256), make([]float32, 256)}
	if p.Regions[0].Mix(out, 48000, 48000, 1) {
		t.Fatal("Mix rendered before the samples were read")
	}
	s.EnableAccess(true)
	waitFor(t, "the samples", func() bool { return s.Audio() != nil })

	// The block starts 100 frames before the region: playback 1 s is
	// modification 0.5 s, source frame 24000.
	at := int64(48000 - 100)
	if !p.Regions[0].Mix(out, at, 48000, 0.5) {
		t.Fatal("Mix did not render with the samples read")
	}
	for i := range 256 {
		for ch := range 2 {
			want := float32(0)
			if i >= 100 {
				want = 0.5 * float32(ch*1000000+24000+i-100)
			}
			if out[ch][i] != want {
				t.Fatalf("channel %d frame %d = %v, want %v", ch, i, out[ch][i], want)
			}
		}
	}

	// The region ends 0.25 s later: frame 12000 of the region is silent.
	clear(out[0])
	clear(out[1])
	p.Regions[0].Mix(out, 48000+12000-10, 48000, 1)
	if out[0][9] != float32(24000+11999) || out[0][10] != 0 {
		t.Fatalf("region end: %v %v, want %v then silence", out[0][9], out[0][10], 24000+11999)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		b.Playback().Regions[0].Mix(out, at, 48000, 1)
	}); allocs != 0 {
		t.Fatalf("Mix allocated %v times", allocs)
	}

	b.RemovePlaybackRegion(r)
	if n := len(b.Playback().Regions); n != 0 {
		t.Fatalf("%d regions after removal", n)
	}
	if n := len(p.Regions); n != 1 {
		t.Fatal("a published snapshot changed")
	}
}

func TestEditingDefersPlaybackUpdates(t *testing.T) {
	d, _, _, _, r := newTestDocument(t, Config{})
	b := d.Bind(0, 0)
	if b.Roles() != AllRoles {
		t.Fatalf("roles %v: a host that knows no roles gives every role", b.Roles())
	}
	changes := 0
	b.OnChange(func() { changes++ })
	d.BeginEditing()
	b.AddPlaybackRegion(r)
	props := r.Properties()
	props.StartInPlayback = 3
	r.Update(props)
	if len(b.Playback().Regions) != 0 || changes != 0 {
		t.Fatal("playback changed inside the editing session")
	}
	d.EndEditing()
	p := b.Playback()
	if len(p.Regions) != 1 || p.Regions[0].StartInPlayback != 3 || changes != 1 {
		t.Fatalf("after editing: %+v, %d changes", p.Regions, changes)
	}
	d.BeginEditing()
	r.Destroy()
	d.EndEditing()
	if len(b.Playback().Regions) != 0 || len(b.PlaybackRegions()) != 0 {
		t.Fatal("a destroyed region still plays")
	}
}

func TestMixResamplesAndReverses(t *testing.T) {
	const rate = 44100.0
	frames := 44100
	x := make([]float32, frames)
	for i := range x {
		x[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / rate))
	}
	a := NewSourceAudio(rate, [][]float32{x})

	// 44.1 kHz audio read at 48 kHz: frame i is at time i/48000.
	out := make([]float32, 4800)
	a.Mix(out, 0, 1000, rate/48000, 1)
	var worst float64
	for i := 100; i < len(out)-100; i++ {
		tm := (1000 + float64(i)*rate/48000) / rate
		worst = max(worst, math.Abs(float64(out[i])-math.Sin(2*math.Pi*440*tm)))
	}
	if worst > 1e-3 {
		t.Fatalf("resampled read is %g off the sine", worst)
	}

	rev := make([]float32, 100)
	a.Mix(rev, 0, 5000, -1, 1)
	for i, v := range rev {
		if v != x[5000-i] {
			t.Fatalf("reverse frame %d = %v, want %v", i, v, x[5000-i])
		}
	}

	// Reads beyond the ends are silent, and do not panic.
	edge := make([]float32, 600)
	a.Mix(edge, 0, -300, 1.5, 1)
	a.Mix(edge, 0, float64(frames)-100, 0.7, 1)
	a.Mix(edge, 0, -1e9, 1, 1)
}

func TestSpanFollowsTimestretch(t *testing.T) {
	r := RegionPlayback{
		Flags:               Timestretch,
		StartInModification: 2, DurationInModification: 4,
		StartInPlayback: 10, DurationInPlayback: 2,
	}
	from, to, pos, step := r.Span(10*48000-5, 100, 48000, 44100)
	if from != 5 || to != 100 || pos != 2*44100 || math.Abs(step-2*44100/48000.0) > 1e-12 {
		t.Fatalf("span %d %d %v %v", from, to, pos, step)
	}
	r.Flags = 0
	_, _, _, step = r.Span(0, 1, 48000, 48000)
	if step != 1 {
		t.Fatalf("an unstretched region steps %v", step)
	}
}

type archiveController struct {
	doc      string
	restored []string
	fail     bool
}

func (c *archiveController) StoreDocument() ([]byte, error) { return []byte(c.doc), nil }
func (c *archiveController) RestoreDocument(data []byte) error {
	c.restored = append(c.restored, "doc="+string(data))
	return nil
}
func (c *archiveController) StoreSource(s *AudioSource) ([]byte, error) {
	if s.State() == nil {
		return nil, nil
	}
	return []byte(s.State().(string)), nil
}
func (c *archiveController) RestoreSource(s *AudioSource, data []byte) error {
	s.SetState(string(data))
	c.restored = append(c.restored, s.Properties().PersistentID+"="+string(data))
	return nil
}
func (c *archiveController) StoreModification(m *AudioModification) ([]byte, error) {
	if c.fail {
		return nil, errors.New("disk full")
	}
	return []byte(m.State().(string)), nil
}
func (c *archiveController) RestoreModification(m *AudioModification, data []byte) error {
	if string(data) == "bad" {
		return errors.New("bad data")
	}
	m.SetState(string(data))
	c.restored = append(c.restored, m.Properties().PersistentID+"="+string(data))
	return nil
}

func TestArchiveRoundTrip(t *testing.T) {
	ctl := &archiveController{doc: "private"}
	config := Config{NewController: func(*Document) Controller { return ctl }}
	d, h, s, m, _ := newTestDocument(t, config)
	s.SetState("analysis")
	m.SetState("edits")
	d.NotifyModelUpdates()
	h.takeEvents()
	data, err := d.Store(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Into a new document: the host recreates the graph, then restores.
	d2, h2, s2, m2, _ := newTestDocument(t, config)
	ctl.restored = nil
	d2.BeginEditing()
	if err := d2.Restore(data, nil); err != nil {
		t.Fatal(err)
	}
	d2.EndEditing()
	if want := []string{"doc=private", "s1=analysis", "m1=edits"}; !slices.Equal(ctl.restored, want) {
		t.Fatalf("restored %v, want %v", ctl.restored, want)
	}
	if s2.State() != "analysis" || m2.State() != "edits" {
		t.Fatal("states not restored")
	}
	d2.NotifyModelUpdates()
	if e := h2.takeEvents(); len(e) != 0 {
		t.Fatalf("restoring notified the host: %v", e)
	}

	// A partial archive of one modification, restored under another ID.
	m3 := s2.CreateAudioModification(9, AudioModificationProperties{PersistentID: "copy"})
	m.SetState("pasted")
	part, err := d.Store(&StoreFilter{Modifications: []*AudioModification{m}})
	if err != nil {
		t.Fatal(err)
	}
	ctl.restored = nil
	d2.BeginEditing()
	err = d2.Restore(part, &RestoreFilter{Modifications: map[string]string{"m1": "copy"}})
	d2.EndEditing()
	if err != nil || m3.State() != "pasted" || m2.State() != "edits" || !slices.Equal(ctl.restored, []string{"copy=pasted"}) {
		t.Fatalf("partial restore: %v, %v, %v, %v", err, m3.State(), m2.State(), ctl.restored)
	}

	// Errors: a failing controller, corrupt data, bad records.
	ctl.fail = true
	if _, err := d.Store(nil); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("store error %v", err)
	}
	ctl.fail = false
	if err := d2.Restore([]byte("something else"), nil); err == nil {
		t.Fatal("restored a foreign archive")
	}
	if err := d2.Restore(data[:len(data)-2], nil); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("truncated archive: %v", err)
	}
	m.SetState("bad")
	bad, _ := d.Store(nil)
	if err := d2.Restore(bad, nil); err == nil || !strings.Contains(err.Error(), "bad data") || s2.State() != "analysis" {
		t.Fatalf("a failing record: %v; the other records must still restore", err)
	}
}

type noteAnalyzer struct{ stopped bool }

func (a *noteAnalyzer) Analyze(s *AudioSource, audio *SourceAudio, stopped func() bool) (*Content, error) {
	if audio.Frames() != int(s.Properties().Samples) {
		return nil, errors.New("wrong audio")
	}
	return &Content{Grade: GradeDetected, Notes: []Note{
		{Pitch: 60, Start: 0.1, Duration: 0.2, SignalDuration: 0.2},
		{Pitch: 62, Start: 0.6, Duration: 0.1, SignalDuration: 0.1},
		{Pitch: 64, Start: 2, Duration: 1, SignalDuration: 1},
	}}, nil
}

func TestAnalysisAndContent(t *testing.T) {
	config := Config{ContentTypes: []ContentType{ContentNotes}, NewController: func(*Document) Controller { return &noteAnalyzer{} }}
	d, h, s, m, r := newTestDocument(t, config)
	if !s.AnalysisIncomplete(ContentNotes) || s.AnalysisIncomplete(ContentTempoEntries) {
		t.Fatal("analysis state before reading")
	}
	s.EnableAccess(true)
	waitFor(t, "the analysis", func() bool { return !s.AnalysisIncomplete(ContentNotes) })
	if s.Content() == nil || len(s.Content().Notes) != 3 {
		t.Fatalf("source content %+v", s.Content())
	}
	if m.Content() != s.Content() {
		t.Fatal("the modification's content is not the source's")
	}
	// The region plays modification 0.5..0.75 s at 1 s: only the second
	// note, moved by 0.5 s.
	rc := r.Content()
	if len(rc.Notes) != 1 || rc.Notes[0].Pitch != 62 || math.Abs(rc.Notes[0].Start-1.1) > 1e-12 {
		t.Fatalf("region notes %+v", rc.Notes)
	}
	d.NotifyModelUpdates()
	want := []string{"progress s1 0", "progress s1 2", "source s1 1", "modification m1 1", "region 5 1"}
	if e := h.takeEvents(); !slices.Equal(e, want) {
		t.Fatalf("notifications %v, want %v", e, want)
	}
	d.BeginEditing()
	s.UpdateContent(0)
	d.EndEditing()
	waitFor(t, "the analysis to run again", func() bool { return !s.AnalysisIncomplete(ContentNotes) })
	if h.reads < 2 {
		t.Fatalf("the samples were not read again after a signal change (%d reads)", h.reads)
	}
}

func TestDisablingAccessStopsReading(t *testing.T) {
	d, h, s, _, _ := newTestDocument(t, Config{})
	h.gate = make(chan struct{})
	s.EnableAccess(true)
	waitFor(t, "the first read", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.reads > 0
	})
	done := make(chan struct{})
	go func() {
		s.EnableAccess(false)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("disabling access returned while a read was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(h.gate)
	<-done
	if s.Audio() != nil || h.closed != 1 {
		t.Fatalf("after disabling: audio %v, %d readers closed", s.Audio() != nil, h.closed)
	}
	h.gate = nil
	s.EnableAccess(true)
	waitFor(t, "the samples", func() bool { return s.Audio() != nil })
	d.BeginEditing()
	s.Destroy()
	d.EndEditing()
	if h.closed != 2 || len(d.AudioSources()) != 0 {
		t.Fatal("destroying the source did not release its reader")
	}

	h.openErr = errors.New("offline")
	s2 := d.CreateAudioSource(7, AudioSourceProperties{PersistentID: "s2", Samples: 10, SampleRate: 48000, Channels: 1})
	s2.EnableAccess(true)
	if s2.Err() == nil || s2.Audio() != nil {
		t.Fatal("a failing reader was not reported")
	}
}

func TestSetStateNotifiesAndRenders(t *testing.T) {
	d, h, _, m, r := newTestDocument(t, Config{})
	b := d.Bind(PlaybackRenderer, PlaybackRenderer)
	b.AddPlaybackRegion(r)
	m.SetState(2.5)
	if got := b.Playback().Regions[0].State; got != 2.5 {
		t.Fatalf("playback state %v", got)
	}
	d.DocumentDataChanged()
	d.NotifyModelUpdates()
	if e := h.takeEvents(); !slices.Equal(e, []string{"modification m1 0", "region 5 0", "document"}) {
		t.Fatalf("notifications %v", e)
	}
	clone := m.Clone(8, AudioModificationProperties{PersistentID: "m2"})
	if clone.State() != 2.5 || clone.Source() != m.Source() {
		t.Fatal("clone did not copy the state")
	}
}

func TestContentRange(t *testing.T) {
	c := &Content{
		Tempo: []TempoEntry{{0, 0}, {2, 4}, {4, 6}},
		Bars:  []BarSignature{{4, 4, 0}, {3, 4, 4}, {4, 4, 7}},
	}
	got := c.Range(TimeRange{Start: 2.5, Duration: 1})
	// No entry lies inside 2.5..3.5 s: the one before and the one after
	// it cover the range.
	if !slices.Equal(got.Tempo, []TempoEntry{{2, 4}, {4, 6}}) {
		t.Fatalf("tempo %v, want the entries around the range", got.Tempo)
	}
	// 2.5..3.5 s is quarter 4.5..5.5: inside the 3/4 bar from quarter 4.
	if len(got.Bars) != 1 || got.Bars[0].Numerator != 3 {
		t.Fatalf("bars %v", got.Bars)
	}
	if got := (&Content{Bars: c.Bars}).Range(TimeRange{Start: 2.5, Duration: 1}); len(got.Bars) != 3 {
		t.Fatal("without a tempo map every bar signature applies")
	}
	if !got.Has(ContentTempoEntries) || got.Has(ContentNotes) {
		t.Fatal("Has")
	}
}

// rampReader serves frame i of every channel as (i mod 1000) + channel.
type rampReader struct{}

func (rampReader) Read(pos int64, buffers [][]float32) error {
	for ch, b := range buffers {
		for i := range b {
			b[i] = float32((pos+int64(i))%1000) + float32(ch)
		}
	}
	return nil
}

func (rampReader) Close() {}

func TestLargeSourceIsMapped(t *testing.T) {
	var mapped, unmapped atomic.Int32
	mapAudio = func(dir string, size int) ([]byte, func(), error) {
		data, unmap, err := mapFile(dir, size)
		if err == nil {
			mapped.Add(1)
			inner := unmap
			unmap = func() { unmapped.Add(1); inner() }
		}
		return data, unmap, err
	}
	t.Cleanup(func() { mapAudio = mapFile })

	// Two channels of 9M frames are 72 MB of samples: a file mapping, not
	// the heap.
	const frames = 9_000_000
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	props := AudioSourceProperties{Samples: frames, SampleRate: 48000, Channels: 2}
	a, err := readAudio(rampReader{}, &props, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if mapped.Load() != 1 {
		t.Fatalf("%d mappings for a 72 MB source", mapped.Load())
	}
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 8<<20 {
		t.Fatalf("the heap grew by %d bytes for a mapped source", grown)
	}
	for _, i := range []int{0, 1, 999, 1000, 4_567_891, frames - 1} {
		for ch := range 2 {
			if got, want := a.Channel(ch)[i], float32(i%1000+ch); got != want {
				t.Fatalf("channel %d frame %d = %v, want %v", ch, i, got, want)
			}
		}
	}
	out := make([]float32, 4)
	a.Mix(out, 1, frames-2, 1, 1) // the last two frames, then silence
	if want := []float32{float32((frames-2)%1000 + 1), float32((frames-1)%1000 + 1), 0, 0}; !slices.Equal(out, want) {
		t.Fatalf("mix at the end = %v, want %v", out, want)
	}

	// Small sources stay on the heap; an unreachable mapped one unmaps.
	NewSourceAudio(48000, [][]float32{make([]float32, 1000)})
	if mapped.Load() != 1 {
		t.Fatal("a small source was mapped")
	}
	runtime.KeepAlive(a)
	for i := 0; i < 50 && unmapped.Load() == 0; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if unmapped.Load() != 1 {
		t.Fatal("the mapping of an unreachable source stays")
	}
}

// algorithmAnalyzer finds one note whose pitch is 60 plus the algorithm.
type algorithmAnalyzer struct {
	archiveController
	runs atomic.Int32
}

func (a *algorithmAnalyzer) Analyze(s *AudioSource, audio *SourceAudio, stopped func() bool) (*Content, error) {
	a.runs.Add(1)
	return &Content{Grade: GradeDetected, Notes: []Note{{Pitch: 60 + int32(s.Algorithm()), Duration: 1, SignalDuration: 1}}}, nil
}

// RestoreSource restores the analysis as well, as pitch 99.
func (a *algorithmAnalyzer) RestoreSource(s *AudioSource, data []byte) error {
	s.SetContent(&Content{Grade: GradeDetected, Notes: []Note{{Pitch: 99, Duration: 1, SignalDuration: 1}}})
	return a.archiveController.RestoreSource(s, data)
}

func TestAlgorithms(t *testing.T) {
	ctl := &algorithmAnalyzer{}
	config := Config{
		ContentTypes:  []ContentType{ContentNotes},
		Algorithms:    []Algorithm{{"auto", "Automatic"}, {"poly", "Polyphonic"}, {"perc", "Percussive"}},
		NewController: func(*Document) Controller { return ctl },
	}
	d, h, s, _, _ := newTestDocument(t, config)
	pitch := func() int32 {
		if c := s.Content(); c != nil && !s.AnalysisIncomplete(ContentNotes) {
			return c.Notes[0].Pitch
		}
		return -1
	}
	s.EnableAccess(true)
	waitFor(t, "the first analysis", func() bool { return pitch() == 60 })
	if s.Algorithm() != 0 {
		t.Fatalf("a new source uses algorithm %d", s.Algorithm())
	}

	// The host's request, inside an editing session, analyzes again after
	// it; the samples are not read again.
	reads := h.reads
	d.BeginEditing()
	s.SetAlgorithm(2)
	if s.Content() != nil || !s.AnalysisIncomplete(ContentNotes) {
		t.Fatal("changing the algorithm kept the old content")
	}
	d.EndEditing()
	waitFor(t, "the analysis with algorithm 2", func() bool { return pitch() == 62 })
	if h.reads != reads {
		t.Fatal("changing the algorithm read the samples again")
	}
	runs := ctl.runs.Load()
	s.SetAlgorithm(2)
	s.SetAlgorithm(3)
	s.SetAlgorithm(-1)
	if s.Algorithm() != 2 || s.Content() == nil || ctl.runs.Load() != runs {
		t.Fatal("the same or an unknown algorithm analyzed again")
	}

	// Archives keep the algorithm: restoring it with the source's data
	// keeps the restored content instead of analyzing again.
	s.SetState("analysis")
	data, err := d.Store(&StoreFilter{Sources: []*AudioSource{s}})
	if err != nil {
		t.Fatal(err)
	}
	d2, _, s2, _, _ := newTestDocument(t, config)
	d2.BeginEditing()
	err = d2.Restore(data, &RestoreFilter{Sources: map[string]string{"s1": "s1"}})
	if err != nil || s2.Algorithm() != 2 {
		t.Fatalf("restored algorithm %d, %v", s2.Algorithm(), err)
	}
	if s2.State() != "analysis" || s2.Content() == nil || s2.Content().Notes[0].Pitch != 99 || s2.AnalysisIncomplete(ContentNotes) {
		t.Fatal("the source's data was not restored")
	}
	d2.EndEditing()
	s2.EnableAccess(true)
	waitFor(t, "the samples", func() bool { return s2.Audio() != nil })
	if s2.Content().Notes[0].Pitch != 99 {
		t.Fatal("the restored analysis was redone")
	}

	// An algorithm a newer version had keeps the source's.
	config.Algorithms = config.Algorithms[:2]
	d3, _, s3, _, _ := newTestDocument(t, config)
	d3.BeginEditing()
	err = d3.Restore(data, nil)
	d3.EndEditing()
	if err != nil || s3.Algorithm() != 0 {
		t.Fatalf("unknown algorithm restored as %d, %v", s3.Algorithm(), err)
	}
}
