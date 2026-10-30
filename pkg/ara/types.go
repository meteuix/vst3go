package ara

// TransformationFlags are the playback transformations a playback region
// asks for (ARAPlaybackTransformationFlags).
type TransformationFlags int32

const (
	// Timestretch: the region plays its modification time range in a
	// different playback duration.
	Timestretch TransformationFlags = 1 << 0
	// TimestretchReflectingTempo: the stretch follows the tempo maps of
	// the source and the musical context instead of a constant ratio.
	TimestretchReflectingTempo TransformationFlags = 1 << 1
	// ContentBasedFadeAtTail and ContentBasedFadeAtHead let the plugin
	// fade at a region border where the content allows.
	ContentBasedFadeAtTail TransformationFlags = 1 << 2
	ContentBasedFadeAtHead TransformationFlags = 1 << 3
	// ContentBasedFades is both content-based fades.
	ContentBasedFades = ContentBasedFadeAtHead | ContentBasedFadeAtTail
)

// ContentType is a kind of musical content (ARAContentType).
type ContentType int32

const (
	ContentNotes         ContentType = 10
	ContentTempoEntries  ContentType = 20
	ContentBarSignatures ContentType = 21
)

// ContentGrade is how reliable content is (ARAContentGrade).
type ContentGrade int32

const (
	GradeInitial  ContentGrade = 0 // default data, not based on the audio
	GradeDetected ContentGrade = 1 // found by analysis
	GradeAdjusted ContentGrade = 2 // edited by the user
	GradeApproved ContentGrade = 3 // confirmed by the user
)

// ContentUpdateFlags tell which parts of content did not change
// (ARAContentUpdateFlags); 0 means everything changed.
type ContentUpdateFlags int32

const (
	SignalUnchanged  ContentUpdateFlags = 1 << 0
	NotesUnchanged   ContentUpdateFlags = 1 << 1
	TimingUnchanged  ContentUpdateFlags = 1 << 2
	TuningUnchanged  ContentUpdateFlags = 1 << 3
	HarmonyUnchanged ContentUpdateFlags = 1 << 4
)

// AnalysisState is the stage of an analysis progress report.
type AnalysisState int32

const (
	AnalysisStarted   AnalysisState = 0
	AnalysisUpdated   AnalysisState = 1
	AnalysisCompleted AnalysisState = 2
)

// Roles are what a plugin instance bound to a document does
// (ARAPlugInInstanceRoleFlags).
type Roles int32

const (
	// PlaybackRenderer: the instance renders playback regions for
	// playback (Binding.Playback).
	PlaybackRenderer Roles = 1 << 0
	// EditorRenderer: the instance renders regions while they are being
	// edited (for example to audition a note).
	EditorRenderer Roles = 1 << 1
	// EditorView: the instance shows the document in its editor and gets
	// the host's selection.
	EditorView Roles = 1 << 2
	// AllRoles is every role.
	AllRoles = PlaybackRenderer | EditorRenderer | EditorView
)

// Color is an RGB color with components from 0 to 1.
type Color struct{ R, G, B float32 }

// TimeRange is a range of time in seconds.
type TimeRange struct{ Start, Duration float64 }

// Note is a note found in audio (ARAContentNote). Times are in seconds of
// the object the content belongs to.
type Note struct {
	// Frequency is the average frequency in Hz; 0 for unpitched notes.
	Frequency float32
	// Pitch is the MIDI note number; PitchNone for unpitched notes.
	Pitch int32
	// Volume is the loudness from 0 to 1.
	Volume float32
	// Start is where the note starts; Attack the time to its peak; Duration
	// its musical length; SignalDuration how long it sounds.
	Start, Attack, Duration, SignalDuration float64
}

// PitchNone is the Pitch of a note with no pitch.
const PitchNone int32 = -1 << 31

// TempoEntry maps a time in seconds to a position in quarter notes.
type TempoEntry struct{ Time, Quarter float64 }

// BarSignature is a time signature starting at a position in quarter notes.
type BarSignature struct {
	Numerator, Denominator int32
	Quarter                float64
}

// Content is musical content of an audio source, a modification or a
// playback region. A Content is never changed after it is published.
type Content struct {
	Grade ContentGrade
	Notes []Note
	Tempo []TempoEntry
	Bars  []BarSignature
}

// Has reports whether the content holds the given type.
func (c *Content) Has(t ContentType) bool {
	if c == nil {
		return false
	}
	switch t {
	case ContentNotes:
		return c.Notes != nil
	case ContentTempoEntries:
		return c.Tempo != nil
	case ContentBarSignatures:
		return c.Bars != nil
	}
	return false
}

// MusicalContextProperties describe a musical context.
type MusicalContextProperties struct {
	Name  string
	Order int32
	Color *Color
}

// RegionSequenceProperties describe a region sequence (a track).
type RegionSequenceProperties struct {
	Name    string
	Order   int32
	Context *MusicalContext
	Color   *Color
}

// AudioSourceProperties describe an audio source (an audio file or a
// recording).
type AudioSourceProperties struct {
	Name         string
	PersistentID string
	Samples      int64
	SampleRate   float64
	Channels     int
	Merits64Bit  bool
}

// AudioModificationProperties describe an audio modification (the plugin's
// edits of a source).
type AudioModificationProperties struct {
	Name         string
	PersistentID string
}

// PlaybackRegionProperties describe a playback region: the part of a
// modification that plays, and where in the song.
type PlaybackRegionProperties struct {
	Flags                  TransformationFlags
	StartInModification    float64
	DurationInModification float64
	StartInPlayback        float64
	DurationInPlayback     float64
	Sequence               *RegionSequence
	Name                   string
	Color                  *Color
}

// Config is a plugin's ARA support.
type Config struct {
	// ArchiveID identifies the plugin's archives in the host's documents
	// (ARAFactory::documentArchiveID), for example
	// "com.example.myplugin.ara.1". Change it when the data the
	// controller stores changes incompatibly.
	ArchiveID string
	// Transformations are the playback transformations the plugin renders.
	Transformations TransformationFlags
	// ContentTypes are the content types the plugin's Analyzer finds.
	ContentTypes []ContentType
	// NewController creates the plugin's controller of a document, on the
	// model thread; nil when the plugin keeps no data of its own.
	NewController func(d *Document) Controller
}

// Controller is a plugin's side of one document. Every method runs on the
// host's model thread. It implements any of the interfaces below.
type Controller any

// ModificationArchiver stores the plugin's data of audio modifications in
// the host's document. Store returns nil when there is nothing to store.
type ModificationArchiver interface {
	StoreModification(m *AudioModification) ([]byte, error)
	RestoreModification(m *AudioModification, data []byte) error
}

// SourceArchiver stores the plugin's data of audio sources (such as edited
// analysis results).
type SourceArchiver interface {
	StoreSource(s *AudioSource) ([]byte, error)
	RestoreSource(s *AudioSource, data []byte) error
}

// DocumentArchiver stores the plugin's own document-wide data. Call
// Document.DocumentDataChanged after changing it.
type DocumentArchiver interface {
	StoreDocument() ([]byte, error)
	RestoreDocument(data []byte) error
}

// Analyzer finds the Config.ContentTypes in an audio source. It runs on
// its own goroutine once the source's samples are read; it should return
// early with an error when stopped() turns true (the host disabled access
// to the samples or destroyed the source).
type Analyzer interface {
	Analyze(s *AudioSource, audio *SourceAudio, stopped func() bool) (*Content, error)
}

// ModificationContentProvider provides the content of modifications that
// differs from their source's, such as notes the user moved.
type ModificationContentProvider interface {
	ModificationContent(m *AudioModification) *Content
}

// SignalPreserver reports whether a modification plays its source
// unchanged; without it, every modification counts as changing it.
type SignalPreserver interface {
	PreservesSignal(m *AudioModification) bool
}

// HeadTailer reports how far a playback region's output reaches before
// its start and after its end, in seconds.
type HeadTailer interface {
	HeadAndTail(r *PlaybackRegion) (head, tail float64)
}

// EditObserver is told when the host finished an editing session.
type EditObserver interface {
	DidEndEditing()
}
