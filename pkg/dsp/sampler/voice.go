package sampler

import (
	"math"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/dsp/envelope"
	"github.com/th-release/vst3go/pkg/dsp/resample"
)

// MaxLayers is the most zones one note plays at once.
const MaxLayers = 8

// Quality selects the kernel voices interpolate samples with.
type Quality int

const (
	// QualityHigh reads with resample.SincKernel (48 taps at full
	// bandwidth): flat to 0.9 of the band, 80 dB alias rejection.
	QualityHigh Quality = iota
	// QualityStandard reads with resample.ShortSincKernel (16 taps): a
	// voice renders about twice as fast, flat to 0.75 of the band, with
	// aliasing only into its top quarter. For large polyphony.
	QualityStandard
)

// Voice plays one note of a Map: every zone the note matches, up to
// MaxLayers, under one envelope. It implements voice.Voice; render its
// output with Render (stereo) or Process (mono). A mono sample plays in
// both channels.
//
// Reads are band-limited: pitching up reads a half-rate copy of the
// sample and a windowed-sinc kernel scaled to the read speed, so notes far
// above the root do not alias. The voice does not allocate. SetQuality
// trades the kernel's length for speed.
//
// A streamed zone (Streamer) takes one of its Streamer's streams for the
// note and reads the half-rate copy chosen when the note starts for the
// whole note: bends and legato moves are band-limited by the kernel
// rather than by a different copy.
type Voice struct {
	sampleRate float64
	instrument *atomic.Pointer[Map]
	kernel     *resample.Kernel
	env        *envelope.ADSR

	layers   [MaxLayers]layer
	matched  [MaxLayers]*Zone
	n        int // layers in use
	note     uint8
	velocity uint8
	gain     float64 // loudest layer's gain, for voice stealing
	active   bool
	released bool
	age      int64
	level    float64 // last envelope value
	bend     float64 // pitch bend in semitones
}

// layer is one zone of the playing note.
type layer struct {
	zone       *Zone
	pos        float64 // in frames of the full-rate sample
	step       float64 // frames per output sample
	loopStart  int
	loopEnd    int // loops while loopEnd > loopStart
	gain       float64
	panL, panR float64 // balance
	done       bool
	stream     *stream // streamed zones; nil without a free stream
	starved    bool    // a read found its window not loaded

	// Set by prepare for each block.
	lvl           int
	scale, cutoff float64
	stereo        bool
}

// NewVoice returns a voice playing from the Map in instrument (read when
// a note starts, so the Map can be swapped while voices play).
func NewVoice(sampleRate float64, instrument *atomic.Pointer[Map]) *Voice {
	return &Voice{
		sampleRate: sampleRate,
		instrument: instrument,
		kernel:     resample.SincKernel(),
		env:        envelope.New(sampleRate),
	}
}

// SetADSR configures the amplitude envelope.
func (v *Voice) SetADSR(attack, decay, sustain, release float64) {
	v.env.SetADSR(attack, decay, sustain, release)
}

// SetPitchBend sets the pitch offset in semitones.
func (v *Voice) SetPitchBend(semitones float64) {
	v.bend = semitones
	v.updateSteps()
}

func (v *Voice) IsActive() bool        { return v.active }
func (v *Voice) GetNote() uint8        { return v.note }
func (v *Voice) GetVelocity() uint8    { return v.velocity }
func (v *Voice) GetAmplitude() float64 { return v.gain * v.level }
func (v *Voice) GetAge() int64         { return v.age }

// TriggerNote starts every zone the note matches from its offset. A note
// outside every zone stays silent.
func (v *Voice) TriggerNote(note, velocity uint8) {
	zones := v.instrument.Load().Match(note, velocity, v.matched[:0])
	v.releaseStreams()
	v.n = len(zones)
	if v.n == 0 {
		v.active = false
		return
	}
	v.note, v.velocity = note, velocity
	vel := float64(velocity) / 127
	v.gain = 0
	for i, z := range zones {
		g := vel * vel * dbToGain(z.Gain)
		pan := max(-1, min(1, z.Pan))
		start, end := z.loop()
		v.layers[i] = layer{
			zone:      z,
			pos:       float64(max(0, min(z.Offset, z.Sample.Len()-1))),
			loopStart: start,
			loopEnd:   end,
			gain:      g,
			panL:      min(1, 1-pan),
			panR:      min(1, 1+pan),
		}
		v.gain = max(v.gain, g)
	}
	v.age = 0
	v.active, v.released = true, false
	v.updateSteps()
	for i := range v.layers[:v.n] {
		ly := &v.layers[i]
		if d := ly.zone.Sample.disk; d != nil {
			ly.lvl = ly.level()
			ly.stream = d.st.acquire(d)
			if ly.stream != nil {
				ly.stream.schedule(ly.lvl, ly.pos/float64(int(1)<<ly.lvl))
			}
		}
	}
	v.env.Trigger()
}

// releaseStreams returns the layers' streams.
func (v *Voice) releaseStreams() {
	for i := range v.layers[:v.n] {
		if ly := &v.layers[i]; ly.stream != nil {
			ly.stream.release()
			ly.stream = nil
		}
	}
}

// ChangeNote moves the voice to a new pitch without restarting the sample
// or envelope (legato).
func (v *Voice) ChangeNote(note uint8) {
	v.note = note
	v.updateSteps()
}

// ReleaseNote releases the envelope, unless every layer is a one-shot,
// and lets sustain loops play out.
func (v *Voice) ReleaseNote() {
	if v.released {
		return
	}
	v.released = true
	oneShot := true
	for i := range v.layers[:v.n] {
		l := &v.layers[i]
		if l.zone.Loop == LoopSustain {
			l.loopStart, l.loopEnd = 0, 0
		}
		oneShot = oneShot && l.zone.Loop == OneShot
	}
	if !oneShot {
		v.env.Release()
	}
}

// SetQuality selects the interpolation kernel (QualityHigh by default); it
// can change while the voice plays.
func (v *Voice) SetQuality(q Quality) {
	if q == QualityStandard {
		v.kernel = resample.ShortSincKernel()
	} else {
		v.kernel = resample.SincKernel()
	}
}

func (v *Voice) Stop() {
	v.releaseStreams()
	v.active = false
	v.n = 0
	clear(v.layers[:])
	v.env.Reset()
	v.level = 0
}

func (v *Voice) updateSteps() {
	for i := range v.layers[:v.n] {
		l := &v.layers[i]
		z, s := l.zone, l.zone.Sample
		semis := (float64(v.note)-s.rootNote)*z.keyTrack()/100 + z.Tune/100 + v.bend
		l.step = math.Exp2(semis/12) * s.sampleRate / v.sampleRate
	}
}

// Process writes the mono output to output, overwriting it.
func (v *Voice) Process(output []float32) {
	clear(output)
	v.Render(output, nil)
}

// Render adds the voice to left and right (right may be nil for mono; the
// layers' pan then has no effect).
func (v *Voice) Render(left, right []float32) {
	if !v.active {
		return
	}
	for i := range v.layers[:v.n] {
		ly := &v.layers[i]
		ly.prepare(right != nil)
		if ly.stream != nil && !ly.done {
			ly.stream.schedule(ly.lvl, ly.pos/ly.scale)
		}
	}
	defer v.countUnderruns()
	for i := range left {
		e := float64(v.env.Next())
		v.level = e
		var l, r, mono float64
		alive := false
		for j := range v.layers[:v.n] {
			ly := &v.layers[j]
			if ly.done {
				continue
			}
			a, b := ly.read(v.kernel)
			mono += a * ly.gain
			l += a * ly.gain * ly.panL
			r += b * ly.gain * ly.panR
			ly.advance()
			alive = alive || !ly.done
		}
		if right == nil {
			left[i] += float32(mono * e)
		} else {
			left[i] += float32(l * e)
			right[i] += float32(r * e)
		}
		v.age++
		if !alive || v.env.GetStage() == envelope.StageIdle {
			v.Stop()
			return
		}
	}
}

// countUnderruns counts the layers that read unloaded windows in the
// block and returns the streams of finished layers.
func (v *Voice) countUnderruns() {
	for i := range v.layers[:v.n] {
		ly := &v.layers[i]
		if ly.starved {
			ly.starved = false
			ly.zone.Sample.disk.st.underruns.Add(1)
		}
		if ly.done && ly.stream != nil {
			ly.stream.release()
			ly.stream = nil
		}
	}
}

// level returns the copy of the sample to read at the layer's step: the
// one at which a step is between 1 and 2 frames.
func (ly *layer) level() int {
	if ly.step <= 1 {
		return 0
	}
	return min(ly.zone.Sample.levelCount()-1, int(math.Floor(math.Log2(ly.step))))
}

// prepare picks the copy of the sample to read for the block; streamed
// layers keep theirs.
func (ly *layer) prepare(stereoOut bool) {
	s := ly.zone.Sample
	if s.disk == nil {
		ly.lvl = ly.level()
	}
	ly.scale = float64(int(1) << ly.lvl)
	ly.cutoff = min(1, 1/(ly.step/ly.scale))
	ly.stereo = s.channels == 2 && stereoOut
}

// advance moves one output sample on, wrapping in the loop and finishing
// at the end of the sample.
func (ly *layer) advance() {
	ly.pos += ly.step
	if ly.loopEnd > ly.loopStart {
		if ly.pos >= float64(ly.loopEnd) {
			ly.pos -= float64(ly.loopEnd - ly.loopStart)
		}
	} else if ly.pos >= float64(ly.zone.Sample.Len()) {
		ly.done = true
	}
}

// read interpolates the layer's copy at its position with the kernel
// scaled to the cutoff, wrapping reads past the loop end. A mono copy
// reads into both channels.
func (ly *layer) read(kernel *resample.Kernel) (float64, float64) {
	if ly.zone.Sample.disk != nil {
		return ly.readDisk(kernel)
	}
	data := ly.zone.Sample.levels[ly.lvl]
	n := len(data[0])
	p := ly.pos / ly.scale
	loopStart := int(math.Round(float64(ly.loopStart) / ly.scale))
	loopEnd := min(n, int(math.Round(float64(ly.loopEnd)/ly.scale)))
	looped := loopEnd-loopStart >= 2
	span := float64(kernel.Zeros()) / ly.cutoff
	first := int(math.Ceil(p - span))
	last := int(math.Floor(p + span))
	if first >= 0 && last < n && (!looped || last < loopEnd) {
		// Every tap is inside the sample, before any wrap.
		var r []float32
		if ly.stereo {
			r = data[1]
		}
		a, b := kernel.Interpolate(data[0], r, p, ly.cutoff)
		if !ly.stereo {
			b = a
		}
		return a, b
	}
	var l, r float64
	for i := first; i <= last; i++ {
		j := i
		if looped && j >= loopEnd {
			j = loopStart + (j-loopStart)%(loopEnd-loopStart)
		}
		if j < 0 || j >= n {
			continue
		}
		w := ly.cutoff * kernel.Weight(ly.cutoff*(p-float64(i)))
		l += w * float64(data[0][j])
		if ly.stereo {
			r += w * float64(data[1][j])
		}
	}
	if !ly.stereo {
		r = l
	}
	return l, r
}

// readDisk is read for a streamed sample: from a kept part or a loaded
// window when they hold every tap, else tap by tap, with the taps of
// unloaded windows silent.
func (ly *layer) readDisk(kernel *resample.Kernel) (float64, float64) {
	d := ly.zone.Sample.disk
	n := d.frames[ly.lvl]
	p := ly.pos / ly.scale
	loopStart := int(math.Round(float64(ly.loopStart) / ly.scale))
	loopEnd := min(n, int(math.Round(float64(ly.loopEnd)/ly.scale)))
	looped := loopEnd-loopStart >= 2
	span := float64(kernel.Zeros()) / ly.cutoff
	first := int(math.Ceil(p - span))
	last := int(math.Floor(p + span))
	if !looped || last < loopEnd {
		if data, base, ok := d.span(ly.stream, ly.lvl, first, last); ok {
			var r []float32
			if ly.stereo {
				r = data[1]
			}
			a, b := kernel.Interpolate(data[0], r, p-float64(base), ly.cutoff)
			if !ly.stereo {
				b = a
			}
			return a, b
		}
	}
	var l, r float64
	for i := first; i <= last; i++ {
		j := i
		if looped && j >= loopEnd {
			j = loopStart + (j-loopStart)%(loopEnd-loopStart)
		}
		if j < 0 || j >= n {
			continue
		}
		data, base, ok := d.span(ly.stream, ly.lvl, j, j)
		if !ok {
			ly.starved = true
			continue
		}
		w := ly.cutoff * kernel.Weight(ly.cutoff*(p-float64(i)))
		l += w * float64(data[0][j-base])
		if ly.stereo {
			r += w * float64(data[1][j-base])
		}
	}
	if !ly.stereo {
		r = l
	}
	return l, r
}
