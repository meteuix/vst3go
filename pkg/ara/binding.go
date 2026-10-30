package ara

import (
	"math"
	"slices"
	"sync/atomic"
)

// Binding is a plugin instance bound to a document. The host gives it
// roles: as a playback renderer it renders the playback regions the host
// assigns (Playback), as an editor renderer it renders the regions being
// edited, and as an editor view it shows the document and gets the host's
// selection.
type Binding struct {
	doc             *Document
	roles           Roles
	playback        []*PlaybackRegion
	editor          []*PlaybackRegion
	editorSequences []*RegionSequence
	selection       Selection
	hidden          []*RegionSequence
	snapshot        atomic.Pointer[Playback]
	onChange        func()
}

// Selection is what the user selected in the host.
type Selection struct {
	Regions   []*PlaybackRegion
	Sequences []*RegionSequence
	// Range is the selected time range in playback time, nil if none.
	Range *TimeRange
}

// Bind binds a plugin instance to the document. The instance takes every
// role the host knows and assigns, and every role the host does not know.
func (d *Document) Bind(known, assigned Roles) *Binding {
	roles := (assigned & known) | (AllRoles &^ known)
	b := &Binding{doc: d, roles: roles}
	b.snapshot.Store(&Playback{})
	d.bindings = append(d.bindings, b)
	return b
}

// Document returns the bound document.
func (b *Binding) Document() *Document { return b.doc }

// Roles returns the instance's roles.
func (b *Binding) Roles() Roles { return b.roles }

// OnChange sets a function called on the model thread whenever the
// binding's regions, the selection or the document changed (after each
// editing session).
func (b *Binding) OnChange(f func()) { b.onChange = f }

func (b *Binding) changed() {
	if b.onChange != nil {
		b.onChange()
	}
}

// PlaybackRegions returns the regions the instance renders for playback.
func (b *Binding) PlaybackRegions() []*PlaybackRegion { return slices.Clone(b.playback) }

// EditorRegions returns the regions the instance renders while editing.
func (b *Binding) EditorRegions() []*PlaybackRegion { return slices.Clone(b.editor) }

// EditorSequences returns the sequences the instance renders while
// editing.
func (b *Binding) EditorSequences() []*RegionSequence { return slices.Clone(b.editorSequences) }

// Selection returns the host's selection, for editor views.
func (b *Binding) Selection() Selection { return b.selection }

// HiddenSequences returns the sequences the host hides in the editor view.
func (b *Binding) HiddenSequences() []*RegionSequence { return slices.Clone(b.hidden) }

// AddPlaybackRegion makes the instance render r for playback.
func (b *Binding) AddPlaybackRegion(r *PlaybackRegion) {
	if !slices.Contains(b.playback, r) {
		b.playback = append(b.playback, r)
	}
	b.edited()
}

// RemovePlaybackRegion stops rendering r for playback.
func (b *Binding) RemovePlaybackRegion(r *PlaybackRegion) {
	b.playback = remove(b.playback, r)
	b.edited()
}

// AddEditorRegion makes the instance render r while editing.
func (b *Binding) AddEditorRegion(r *PlaybackRegion) {
	if !slices.Contains(b.editor, r) {
		b.editor = append(b.editor, r)
	}
	b.edited()
}

// RemoveEditorRegion stops rendering r while editing.
func (b *Binding) RemoveEditorRegion(r *PlaybackRegion) {
	b.editor = remove(b.editor, r)
	b.edited()
}

// AddEditorSequence makes the instance render the regions of q while
// editing.
func (b *Binding) AddEditorSequence(q *RegionSequence) {
	if !slices.Contains(b.editorSequences, q) {
		b.editorSequences = append(b.editorSequences, q)
	}
	b.edited()
}

// RemoveEditorSequence stops rendering q while editing.
func (b *Binding) RemoveEditorSequence(q *RegionSequence) {
	b.editorSequences = remove(b.editorSequences, q)
	b.edited()
}

// SetSelection sets the host's selection.
func (b *Binding) SetSelection(sel Selection) {
	b.selection = sel
	b.changed()
}

// HideSequences sets the sequences the host hides in the editor view.
func (b *Binding) HideSequences(seqs []*RegionSequence) {
	b.hidden = seqs
	b.changed()
}

// Unbind detaches the instance from the document.
func (b *Binding) Unbind() {
	b.doc.bindings = remove(b.doc.bindings, b)
	b.playback, b.editor, b.editorSequences = nil, nil, nil
	b.snapshot.Store(&Playback{})
}

// edited updates the playback now, or at the end of the editing session.
func (b *Binding) edited() {
	if b.doc.editing > 0 {
		return
	}
	b.rebuild()
	b.changed()
}

func (b *Binding) rebuild() {
	p := &Playback{Regions: make([]RegionPlayback, 0, len(b.playback))}
	for _, r := range b.playback {
		m := r.modification
		p.Regions = append(p.Regions, RegionPlayback{
			Region:                 r,
			Modification:           m,
			Source:                 m.source,
			State:                  m.State(),
			Flags:                  r.props.Flags,
			StartInModification:    r.props.StartInModification,
			DurationInModification: r.props.DurationInModification,
			StartInPlayback:        r.props.StartInPlayback,
			DurationInPlayback:     r.props.DurationInPlayback,
		})
	}
	b.snapshot.Store(p)
}

// Playback returns what the instance renders for playback. It is safe on
// the audio thread and does not allocate; the result does not change.
func (b *Binding) Playback() *Playback {
	if b == nil {
		return &emptyPlayback
	}
	return b.snapshot.Load()
}

var emptyPlayback Playback

// Playback is a snapshot of the regions a playback renderer renders.
type Playback struct {
	Regions []RegionPlayback
}

// RegionPlayback is a playback region as rendering sees it.
type RegionPlayback struct {
	Region       *PlaybackRegion
	Modification *AudioModification
	Source       *AudioSource
	// State is the modification's state (AudioModification.SetState).
	State                                       any
	Flags                                       TransformationFlags
	StartInModification, DurationInModification float64
	StartInPlayback, DurationInPlayback         float64
}

// Span returns which frames of a block the region plays: the block
// starts at sample position at of the song, at sampleRate, and has frames
// frames. Frames [from, to) are inside the region; the first of them maps
// to frame pos of the source, and each next one step frames further. With
// Timestretch the modification range is spread over the playback
// duration (a plugin that stretches uses this mapping as its time map),
// otherwise the region plays its modification time unchanged.
func (r *RegionPlayback) Span(at int64, frames int, sampleRate, sourceRate float64) (from, to int, pos, step float64) {
	ratio := 1.0
	if r.Flags&Timestretch != 0 && r.DurationInPlayback > 0 {
		ratio = r.DurationInModification / r.DurationInPlayback
	}
	start := r.StartInPlayback*sampleRate - float64(at)
	end := (r.StartInPlayback+r.DurationInPlayback)*sampleRate - float64(at)
	from = int(min(max(math.Ceil(start-1e-9), 0), float64(frames)))
	to = int(min(max(math.Ceil(end-1e-9), float64(from)), float64(frames)))
	t := float64(at+int64(from))/sampleRate - r.StartInPlayback
	pos = (r.StartInModification + t*ratio) * sourceRate
	step = ratio * sourceRate / sampleRate
	// Sample-aligned regions at the source's rate read whole frames.
	if math.Abs(pos-math.Round(pos)) < 1e-6 {
		pos = math.Round(pos)
	}
	return from, to, pos, step
}

// Mix adds gain times the region's source audio to dst (one slice per
// output channel) for a block starting at sample position at, with the
// region's time map (Span). It returns false, adding nothing, while the
// source's samples are not read yet. It does not allocate.
func (r *RegionPlayback) Mix(dst [][]float32, at int64, sampleRate float64, gain float32) bool {
	audio := r.Source.Audio()
	if audio == nil || len(dst) == 0 {
		return audio != nil
	}
	from, to, pos, step := r.Span(at, len(dst[0]), sampleRate, audio.SampleRate())
	r.MixSpan(dst, from, to, pos, step, gain)
	return true
}

// MixSpan adds gain times the source audio to frames [from, to) of dst,
// reading from source frame pos on, step frames per output frame (negative
// to play backwards), for plugins with their own time map. A mono source
// plays on every channel; a mono output gets the average of the source's
// channels; otherwise output channel i plays source channel i modulo the
// source's channels. It returns false while the samples are not read yet.
// It does not allocate.
func (r *RegionPlayback) MixSpan(dst [][]float32, from, to int, pos, step float64, gain float32) bool {
	audio := r.Source.Audio()
	if audio == nil {
		return false
	}
	if from >= to || len(dst) == 0 {
		return true
	}
	channels := audio.Channels()
	if len(dst) == 1 && channels > 1 {
		g := gain / float32(channels)
		for ch := range channels {
			audio.Mix(dst[0][from:to], ch, pos, step, g)
		}
		return true
	}
	for i, out := range dst {
		audio.Mix(out[from:to], i%channels, pos, step, gain)
	}
	return true
}
