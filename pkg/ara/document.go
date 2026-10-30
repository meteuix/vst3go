package ara

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"
)

// Host is what a document needs from the ARA host. The plugin runtime
// implements it for real hosts; a test can implement it in Go to drive a
// Document without a host.
type Host interface {
	// OpenAudio returns a reader of a source's samples. Model thread.
	OpenAudio(s *AudioSource) (AudioReader, error)
	// MusicalContextContent reads the host's tempo map and bar signatures
	// of a musical context (nil for what the host does not provide).
	// Model thread.
	MusicalContextContent(c *MusicalContext) ([]TempoEntry, []BarSignature)

	// The notifications run from Document.NotifyModelUpdates, on the model
	// thread.
	AnalysisProgress(s *AudioSource, state AnalysisState, value float32)
	SourceContentChanged(s *AudioSource, flags ContentUpdateFlags)
	ModificationContentChanged(m *AudioModification, flags ContentUpdateFlags)
	RegionContentChanged(r *PlaybackRegion, flags ContentUpdateFlags)
	DocumentDataChanged()
}

// AudioReader reads the samples of one audio source.
type AudioReader interface {
	// Read fills buffers (one per channel) with the frames from
	// position on. It is called from one goroutine at a time, never the
	// audio thread.
	Read(position int64, buffers [][]float32) error
	// Close releases the reader, on the model thread, after the last Read
	// returned.
	Close()
}

// Document is the plugin's model of one ARA document: the host's audio
// sources, the plugin's modifications of them, the playback regions that
// play those in the song, and the tracks (region sequences) and musical
// contexts they sit in.
//
// The host edits the model through the plugin runtime, which calls the
// editing methods below on the host's model thread (the main thread),
// grouped between BeginEditing and EndEditing. Plugins read the model on
// that thread; the audio thread reads it only through Binding.Playback.
type Document struct {
	config     Config
	host       Host
	controller Controller

	name      string
	editing   int
	restoring bool
	destroyed bool

	contexts  []*MusicalContext
	sequences []*RegionSequence
	sources   []*AudioSource
	bindings  []*Binding

	mu      sync.Mutex
	pending []func()
}

// NewDocument creates a document; it runs config.NewController.
func NewDocument(config Config, host Host, name string) *Document {
	d := &Document{config: config, host: host, name: name}
	if config.NewController != nil {
		d.controller = config.NewController(d)
	}
	return d
}

// Config returns the document's ARA configuration.
func (d *Document) Config() Config { return d.config }

// Controller returns the plugin's controller of the document.
func (d *Document) Controller() Controller { return d.controller }

// Name returns the document's name.
func (d *Document) Name() string { return d.name }

// SetName renames the document.
func (d *Document) SetName(name string) { d.name = name }

// Editing reports whether the host is inside an editing session.
func (d *Document) Editing() bool { return d.editing > 0 }

// MusicalContexts returns the document's musical contexts.
func (d *Document) MusicalContexts() []*MusicalContext { return slices.Clone(d.contexts) }

// RegionSequences returns the document's region sequences.
func (d *Document) RegionSequences() []*RegionSequence { return slices.Clone(d.sequences) }

// AudioSources returns the document's audio sources.
func (d *Document) AudioSources() []*AudioSource { return slices.Clone(d.sources) }

// BeginEditing starts an editing session.
func (d *Document) BeginEditing() { d.editing++ }

// EndEditing ends an editing session. At the end of the outermost one it
// reads the samples of sources that need them, updates the playback of
// every binding and tells the controller and the bindings.
func (d *Document) EndEditing() {
	if d.editing == 0 {
		return
	}
	d.editing--
	if d.editing > 0 {
		return
	}
	for _, s := range d.sources {
		s.startRead()
	}
	for _, b := range d.bindings {
		b.rebuild()
	}
	if o, ok := d.controller.(EditObserver); ok {
		o.DidEndEditing()
	}
	for _, b := range d.bindings {
		b.changed()
	}
}

// NotifyModelUpdates sends the pending notifications to the host.
func (d *Document) NotifyModelUpdates() {
	d.mu.Lock()
	pending := d.pending
	d.pending = nil
	d.mu.Unlock()
	for _, f := range pending {
		f()
	}
}

// post queues a notification for NotifyModelUpdates; any goroutine.
func (d *Document) post(f func()) {
	d.mu.Lock()
	d.pending = append(d.pending, f)
	d.mu.Unlock()
}

// DocumentDataChanged tells the host that the controller's document data
// (DocumentArchiver) changed, so it saves the document again.
func (d *Document) DocumentDataChanged() {
	if !d.restoring {
		d.post(func() {
			if !d.destroyed {
				d.host.DocumentDataChanged()
			}
		})
	}
}

// update rebuilds the bindings' playback now, or at the end of the
// editing session.
func (d *Document) update() {
	if d.editing > 0 {
		return
	}
	for _, b := range d.bindings {
		b.rebuild()
		b.changed()
	}
}

// Destroy stops every read and analysis; the host destroyed the objects
// before.
func (d *Document) Destroy() {
	for _, s := range d.sources {
		s.stopRead()
		s.closeReader()
	}
	d.destroyed = true
	for _, b := range d.bindings {
		b.snapshot.Store(&Playback{})
	}
}

// MusicalContext is a tempo map and time signatures shared by tracks.
type MusicalContext struct {
	doc       *Document
	hostRef   uintptr
	props     MusicalContextProperties
	tempo     []TempoEntry
	bars      []BarSignature
	sequences []*RegionSequence
}

// CreateMusicalContext adds a musical context and reads its content.
func (d *Document) CreateMusicalContext(hostRef uintptr, props MusicalContextProperties) *MusicalContext {
	c := &MusicalContext{doc: d, hostRef: hostRef, props: props}
	d.contexts = append(d.contexts, c)
	c.tempo, c.bars = d.host.MusicalContextContent(c)
	return c
}

// HostRef returns the host's reference of the context.
func (c *MusicalContext) HostRef() uintptr { return c.hostRef }

// Properties returns the context's properties.
func (c *MusicalContext) Properties() MusicalContextProperties { return c.props }

// Tempo returns the host's tempo map of the context, nil if unknown.
func (c *MusicalContext) Tempo() []TempoEntry { return c.tempo }

// Bars returns the host's bar signatures of the context, nil if unknown.
func (c *MusicalContext) Bars() []BarSignature { return c.bars }

// RegionSequences returns the sequences that use the context.
func (c *MusicalContext) RegionSequences() []*RegionSequence { return slices.Clone(c.sequences) }

// Update sets the context's properties.
func (c *MusicalContext) Update(props MusicalContextProperties) { c.props = props }

// UpdateContent reads the context's content from the host again.
func (c *MusicalContext) UpdateContent() {
	c.tempo, c.bars = c.doc.host.MusicalContextContent(c)
}

// Destroy removes the context; no sequence uses it any more.
func (c *MusicalContext) Destroy() {
	c.doc.contexts = remove(c.doc.contexts, c)
}

// RegionSequence is a track: a list of playback regions.
type RegionSequence struct {
	doc     *Document
	hostRef uintptr
	props   RegionSequenceProperties
	regions []*PlaybackRegion
}

// CreateRegionSequence adds a region sequence.
func (d *Document) CreateRegionSequence(hostRef uintptr, props RegionSequenceProperties) *RegionSequence {
	q := &RegionSequence{doc: d, hostRef: hostRef}
	d.sequences = append(d.sequences, q)
	q.Update(props)
	return q
}

// HostRef returns the host's reference of the sequence.
func (q *RegionSequence) HostRef() uintptr { return q.hostRef }

// Properties returns the sequence's properties.
func (q *RegionSequence) Properties() RegionSequenceProperties { return q.props }

// PlaybackRegions returns the regions in the sequence.
func (q *RegionSequence) PlaybackRegions() []*PlaybackRegion { return slices.Clone(q.regions) }

// Update sets the sequence's properties.
func (q *RegionSequence) Update(props RegionSequenceProperties) {
	if c := q.props.Context; c != nil {
		c.sequences = remove(c.sequences, q)
	}
	q.props = props
	if c := props.Context; c != nil {
		c.sequences = append(c.sequences, q)
	}
}

// Destroy removes the sequence; it holds no regions any more.
func (q *RegionSequence) Destroy() {
	if c := q.props.Context; c != nil {
		c.sequences = remove(c.sequences, q)
	}
	q.doc.sequences = remove(q.doc.sequences, q)
	for _, b := range q.doc.bindings {
		b.editorSequences = remove(b.editorSequences, q)
		b.hidden = remove(b.hidden, q)
	}
}

// state holds a plugin-defined value that the audio thread may read.
type state struct{ v atomic.Pointer[any] }

func (s *state) load() any {
	if p := s.v.Load(); p != nil {
		return *p
	}
	return nil
}

func (s *state) store(v any) { s.v.Store(&v) }

// AudioSource is a piece of audio in the host: a file or a recording.
type AudioSource struct {
	doc           *Document
	hostRef       uintptr
	props         atomic.Pointer[AudioSourceProperties]
	modifications []*AudioModification
	access        bool
	deactivated   bool
	destroyed     bool

	reader AudioReader
	job    *readJob

	audio    atomic.Pointer[SourceAudio]
	content  atomic.Pointer[Content]
	analyzed atomic.Bool
	err      atomic.Pointer[error]
	state    state
}

type readJob struct {
	stop atomic.Bool
	done chan struct{}
}

// CreateAudioSource adds an audio source; reading its samples waits for
// EnableAccess.
func (d *Document) CreateAudioSource(hostRef uintptr, props AudioSourceProperties) *AudioSource {
	s := &AudioSource{doc: d, hostRef: hostRef}
	s.props.Store(&props)
	d.sources = append(d.sources, s)
	return s
}

// HostRef returns the host's reference of the source.
func (s *AudioSource) HostRef() uintptr { return s.hostRef }

// Properties returns the source's properties; any goroutine.
func (s *AudioSource) Properties() AudioSourceProperties { return *s.props.Load() }

// AudioModifications returns the source's modifications.
func (s *AudioSource) AudioModifications() []*AudioModification { return slices.Clone(s.modifications) }

// Audio returns the source's samples, nil until they are read; any
// goroutine, including the audio thread.
func (s *AudioSource) Audio() *SourceAudio { return s.audio.Load() }

// Content returns the source's content, nil until it is analyzed or set;
// any goroutine.
func (s *AudioSource) Content() *Content { return s.content.Load() }

// Err returns the error of the last read or analysis, nil if it
// succeeded.
func (s *AudioSource) Err() error {
	if p := s.err.Load(); p != nil {
		return *p
	}
	return nil
}

// State returns the plugin's value for the source (SetState), nil before.
func (s *AudioSource) State() any { return s.state.load() }

// SetState sets the plugin's value for the source, for example data a
// SourceArchiver stores. Treat values as immutable: set a new one to
// change it. Model thread.
func (s *AudioSource) SetState(v any) { s.state.store(v) }

// SetContent replaces the source's content, for example after the user
// corrected the analysis, and tells the host. Model thread.
func (s *AudioSource) SetContent(c *Content) {
	s.content.Store(c)
	s.analyzed.Store(true)
	if !s.doc.restoring {
		s.contentChanged(SignalUnchanged)
	}
}

// contentChanged queues the notifications of a source content change for
// the source, its modifications and their regions.
func (s *AudioSource) contentChanged(flags ContentUpdateFlags) {
	s.doc.post(func() {
		if s.destroyed {
			return
		}
		h := s.doc.host
		h.SourceContentChanged(s, flags)
		for _, m := range s.modifications {
			h.ModificationContentChanged(m, flags)
			for _, r := range m.regions {
				h.RegionContentChanged(r, flags)
			}
		}
	})
}

// Update sets the source's properties. A change of the sample count,
// rate or channels drops the samples read so far; they are read again
// when access is enabled.
func (s *AudioSource) Update(props AudioSourceProperties) {
	old := s.props.Load()
	s.props.Store(&props)
	if old.Samples != props.Samples || old.SampleRate != props.SampleRate || old.Channels != props.Channels {
		s.invalidate()
	}
}

// UpdateContent tells the source that the host's audio or content
// changed. Unless flags include SignalUnchanged, the samples and the
// analysis are dropped and redone.
func (s *AudioSource) UpdateContent(flags ContentUpdateFlags) {
	if flags&SignalUnchanged == 0 {
		s.invalidate()
	}
}

func (s *AudioSource) invalidate() {
	s.stopRead()
	s.audio.Store(nil)
	s.content.Store(nil)
	s.analyzed.Store(false)
	if s.doc.editing == 0 {
		s.startRead()
	}
}

// EnableAccess allows or forbids reading the source's samples. Disabling
// waits for the current read to stop and releases the reader.
func (s *AudioSource) EnableAccess(enable bool) {
	if enable == s.access {
		return
	}
	s.access = enable
	if enable {
		reader, err := s.doc.host.OpenAudio(s)
		if err != nil {
			s.err.Store(&err)
			return
		}
		s.reader = reader
		s.startRead()
		return
	}
	s.stopRead()
	s.closeReader()
}

// SetDeactivated moves the source into the host's undo history (true) or
// back. A deactivated source is neither read nor analyzed.
func (s *AudioSource) SetDeactivated(deactivated bool) {
	s.deactivated = deactivated
	if deactivated {
		s.stopRead()
	} else if s.doc.editing == 0 {
		s.startRead()
	}
}

// AnalysisIncomplete reports whether an analysis of the given type, one
// of Config.ContentTypes, has not finished.
func (s *AudioSource) AnalysisIncomplete(t ContentType) bool {
	return slices.Contains(s.doc.config.ContentTypes, t) && !s.analyzed.Load()
}

// RequestAnalysis asks for the analysis of the source. The analysis runs
// whenever the samples are read and it has not run yet, so this only
// starts reading when it was not started.
func (s *AudioSource) RequestAnalysis() { s.startRead() }

// Destroy removes the source; it has no modifications any more.
func (s *AudioSource) Destroy() {
	s.stopRead()
	s.closeReader()
	s.destroyed = true
	s.doc.sources = remove(s.doc.sources, s)
}

func (s *AudioSource) closeReader() {
	if s.reader != nil {
		s.reader.Close()
		s.reader = nil
	}
}

// startRead reads the samples and runs the analysis on a goroutine when
// they are missing and the source may be read.
func (s *AudioSource) startRead() {
	analyzer, _ := s.doc.controller.(Analyzer)
	if len(s.doc.config.ContentTypes) == 0 {
		analyzer = nil
	}
	needAnalysis := analyzer != nil && !s.analyzed.Load()
	if s.job != nil {
		select {
		case <-s.job.done:
			s.job = nil
		default:
		}
	}
	if s.reader == nil || s.deactivated || s.job != nil || (s.audio.Load() != nil && !needAnalysis) {
		return
	}
	job := &readJob{done: make(chan struct{})}
	s.job = job
	reader := s.reader
	go func() {
		defer close(job.done)
		audio := s.audio.Load()
		if audio == nil {
			props := s.Properties()
			a, err := readAudio(reader, &props, job.stop.Load)
			if err != nil {
				if err != errReadStopped {
					s.err.Store(&err)
				}
				return
			}
			s.err.Store(nil)
			s.audio.Store(a)
			audio = a
		}
		if !needAnalysis {
			return
		}
		s.doc.post(func() {
			if !s.destroyed {
				s.doc.host.AnalysisProgress(s, AnalysisStarted, 0)
			}
		})
		content, err := analyzer.Analyze(s, audio, job.stop.Load)
		if job.stop.Load() {
			return
		}
		if err != nil {
			s.err.Store(&err)
		} else {
			s.content.Store(content)
			s.analyzed.Store(true)
		}
		s.doc.post(func() {
			if !s.destroyed {
				s.doc.host.AnalysisProgress(s, AnalysisCompleted, 1)
			}
		})
		if err == nil {
			s.contentChanged(SignalUnchanged)
		}
	}()
}

// stopRead stops the read or analysis and waits for it.
func (s *AudioSource) stopRead() {
	if s.job == nil {
		return
	}
	s.job.stop.Store(true)
	<-s.job.done
	s.job = nil
}

// AudioModification is the plugin's edits of an audio source.
type AudioModification struct {
	doc         *Document
	hostRef     uintptr
	props       AudioModificationProperties
	source      *AudioSource
	regions     []*PlaybackRegion
	deactivated bool
	state       state
}

// CreateAudioModification adds a modification of s.
func (s *AudioSource) CreateAudioModification(hostRef uintptr, props AudioModificationProperties) *AudioModification {
	m := &AudioModification{doc: s.doc, hostRef: hostRef, props: props, source: s}
	s.modifications = append(s.modifications, m)
	return m
}

// Clone adds a modification of the same source with m's state.
func (m *AudioModification) Clone(hostRef uintptr, props AudioModificationProperties) *AudioModification {
	c := m.source.CreateAudioModification(hostRef, props)
	c.state.store(m.state.load())
	return c
}

// HostRef returns the host's reference of the modification.
func (m *AudioModification) HostRef() uintptr { return m.hostRef }

// Properties returns the modification's properties.
func (m *AudioModification) Properties() AudioModificationProperties { return m.props }

// Source returns the modified source.
func (m *AudioModification) Source() *AudioSource { return m.source }

// PlaybackRegions returns the regions playing the modification.
func (m *AudioModification) PlaybackRegions() []*PlaybackRegion { return slices.Clone(m.regions) }

// Deactivated reports whether the modification is in the undo history.
func (m *AudioModification) Deactivated() bool { return m.deactivated }

// State returns the plugin's value for the modification, nil before
// SetState; any goroutine.
func (m *AudioModification) State() any { return m.state.load() }

// SetState sets the plugin's value for the modification: its edits, which
// rendering reads through RegionPlayback.State. Treat values as
// immutable: set a new one to change it. It updates the playback of every
// binding and tells the host that the modification's content changed.
// Model thread.
func (m *AudioModification) SetState(v any) {
	m.state.store(v)
	if m.doc.restoring {
		return
	}
	m.doc.post(func() {
		if m.source.destroyed || !slices.Contains(m.source.modifications, m) {
			return
		}
		m.doc.host.ModificationContentChanged(m, 0)
		for _, r := range m.regions {
			m.doc.host.RegionContentChanged(r, 0)
		}
	})
	m.doc.update()
}

// Content returns the modification's content: the controller's
// ModificationContent, else the source's.
func (m *AudioModification) Content() *Content {
	if p, ok := m.doc.controller.(ModificationContentProvider); ok {
		return p.ModificationContent(m)
	}
	return m.source.Content()
}

// PreservesSignal reports whether the modification plays its source
// unchanged (SignalPreserver).
func (m *AudioModification) PreservesSignal() bool {
	if p, ok := m.doc.controller.(SignalPreserver); ok {
		return p.PreservesSignal(m)
	}
	return false
}

// Update sets the modification's properties.
func (m *AudioModification) Update(props AudioModificationProperties) { m.props = props }

// SetDeactivated moves the modification into the undo history or back.
func (m *AudioModification) SetDeactivated(deactivated bool) { m.deactivated = deactivated }

// Destroy removes the modification; it has no regions any more.
func (m *AudioModification) Destroy() {
	m.source.modifications = remove(m.source.modifications, m)
}

// PlaybackRegion plays part of a modification at a position in the song.
type PlaybackRegion struct {
	doc          *Document
	hostRef      uintptr
	props        PlaybackRegionProperties
	modification *AudioModification
	head, tail   atomic.Uint64 // float64 bits, seconds
}

// CreatePlaybackRegion adds a region playing m.
func (m *AudioModification) CreatePlaybackRegion(hostRef uintptr, props PlaybackRegionProperties) *PlaybackRegion {
	r := &PlaybackRegion{doc: m.doc, hostRef: hostRef, modification: m}
	m.regions = append(m.regions, r)
	r.Update(props)
	return r
}

// HostRef returns the host's reference of the region.
func (r *PlaybackRegion) HostRef() uintptr { return r.hostRef }

// Properties returns the region's properties.
func (r *PlaybackRegion) Properties() PlaybackRegionProperties { return r.props }

// AudioModification returns the modification the region plays.
func (r *PlaybackRegion) AudioModification() *AudioModification { return r.modification }

// Update sets the region's properties.
func (r *PlaybackRegion) Update(props PlaybackRegionProperties) {
	if q := r.props.Sequence; q != nil {
		q.regions = remove(q.regions, r)
	}
	r.props = props
	if q := props.Sequence; q != nil {
		q.regions = append(q.regions, r)
	}
	r.updateHeadTail()
	r.doc.update()
}

func (r *PlaybackRegion) updateHeadTail() {
	var head, tail float64
	if h, ok := r.doc.controller.(HeadTailer); ok {
		head, tail = h.HeadAndTail(r)
	}
	r.head.Store(math.Float64bits(head))
	r.tail.Store(math.Float64bits(tail))
}

// HeadAndTail returns how far the region's output reaches before its
// start and after its end; any goroutine, including the audio thread.
func (r *PlaybackRegion) HeadAndTail() (head, tail float64) {
	return math.Float64frombits(r.head.Load()), math.Float64frombits(r.tail.Load())
}

// Destroy removes the region; no binding renders it any more.
func (r *PlaybackRegion) Destroy() {
	if q := r.props.Sequence; q != nil {
		q.regions = remove(q.regions, r)
	}
	r.modification.regions = remove(r.modification.regions, r)
	for _, b := range r.doc.bindings {
		b.playback = remove(b.playback, r)
		b.editor = remove(b.editor, r)
	}
	r.doc.update()
}

func remove[T comparable](list []T, v T) []T {
	if i := slices.Index(list, v); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return list
}
