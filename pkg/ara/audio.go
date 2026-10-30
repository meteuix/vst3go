package ara

import (
	"errors"
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/resample"
)

// audioPad is the silent frames kept before and after a source's samples,
// so reads between samples near the ends find every kernel tap.
const audioPad = 256

// readChunk is the frames read from the host per request.
const readChunk = 1 << 16

// SourceAudio is the samples of an audio source, read from the host as
// 32-bit floats. It is never changed after it is published, so the audio
// thread can read it without locks.
type SourceAudio struct {
	sampleRate float64
	frames     int
	data       [][]float32 // per channel: audioPad zeros, the frames, audioPad zeros
}

// NewSourceAudio returns audio holding a copy of channels (equal lengths)
// at the sample rate, for tests and for plugins that render other audio.
func NewSourceAudio(sampleRate float64, channels [][]float32) *SourceAudio {
	frames := 0
	if len(channels) > 0 {
		frames = len(channels[0])
	}
	a := newSourceAudio(sampleRate, len(channels), frames)
	for ch, x := range channels {
		copy(a.Channel(ch), x[:frames])
	}
	return a
}

func newSourceAudio(sampleRate float64, channels, frames int) *SourceAudio {
	a := &SourceAudio{sampleRate: sampleRate, frames: frames, data: make([][]float32, channels)}
	for ch := range a.data {
		a.data[ch] = make([]float32, frames+2*audioPad)
	}
	return a
}

// SampleRate returns the audio's sample rate.
func (a *SourceAudio) SampleRate() float64 { return a.sampleRate }

// Frames returns the frames per channel.
func (a *SourceAudio) Frames() int { return a.frames }

// Channels returns the channel count.
func (a *SourceAudio) Channels() int { return len(a.data) }

// Channel returns the frames of a channel. Do not modify them.
func (a *SourceAudio) Channel(ch int) []float32 {
	return a.data[ch][audioPad : audioPad+a.frames : audioPad+a.frames]
}

// Mix adds gain·x(pos+i·step) of channel ch to dst[i], where x is the
// channel and positions are in frames: step 1 plays it at its rate, -1
// backwards, other steps resample it. Positions between frames are read
// with a windowed-sinc kernel, band-limited when |step| > 1; positions
// outside the audio read silence. It does not allocate.
func (a *SourceAudio) Mix(dst []float32, ch int, pos, step float64, gain float32) {
	x := a.data[ch]
	if (step == 1 || step == -1) && pos == math.Trunc(pos) {
		dir := int(step)
		j := int(pos)
		for i := range dst {
			if j >= 0 && j < a.frames {
				dst[i] += gain * x[audioPad+j]
			}
			j += dir
		}
		return
	}
	k := resample.SincKernel()
	// The kernel spans Zeros/c frames on each side; it must fit in the
	// padding.
	c := max(min(1, 1/math.Abs(step)), float64(k.Zeros())/(audioPad-2))
	span := float64(k.Zeros())/c + 1
	lo, hi := span-audioPad, float64(a.frames)+audioPad-1-span
	for i := range dst {
		p := pos + float64(i)*step
		if p < lo || p > hi {
			continue
		}
		y, _ := k.Interpolate(x, nil, p+audioPad, c)
		dst[i] += gain * float32(y)
	}
}

// errReadStopped ends a read the host's access change interrupted.
var errReadStopped = errors.New("ara: reading stopped")

// readAudio reads every sample of the source through reader, checking
// stopped between requests.
func readAudio(reader AudioReader, props *AudioSourceProperties, stopped func() bool) (*SourceAudio, error) {
	if props.Samples < 0 || props.Samples > math.MaxInt32 || props.Channels < 1 || !(props.SampleRate > 0) {
		return nil, errors.New("ara: the audio source's format is not readable")
	}
	frames := int(props.Samples)
	a := newSourceAudio(props.SampleRate, props.Channels, frames)
	buffers := make([][]float32, props.Channels)
	for pos := 0; pos < frames; pos += readChunk {
		if stopped() {
			return nil, errReadStopped
		}
		n := min(readChunk, frames-pos)
		for ch := range buffers {
			buffers[ch] = a.data[ch][audioPad+pos : audioPad+pos+n]
		}
		if err := reader.Read(int64(pos), buffers); err != nil {
			return nil, err
		}
	}
	return a, nil
}
