package ara

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/meteuix/vst3go/v2/pkg/dsp/resample"
)

// audioPad is the silent frames kept before and after a source's samples,
// so reads between samples near the ends find every kernel tap.
const audioPad = 256

// readChunk is the frames read from the host per request.
const readChunk = 1 << 16

// mappedBytes is the size from which a source's samples go to a temporary
// file mapped into memory instead of the heap: the system pages them in
// as they play and can drop them under memory pressure (writing them to
// the file first), so long recordings do not need their size in RAM. A
// page dropped and read again stalls the thread that reads it on the
// disk, which the audio thread may be.
var mappedBytes = 64 << 20

// mapAudio maps the file of a large source (mapFile; tests wrap it).
var mapAudio = mapFile

// SourceAudio is the samples of an audio source, read from the host as
// 32-bit floats. It is never changed after it is published, so the audio
// thread can read it without locks. Large sources live in a temporary
// file mapped into memory (see Channel).
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
	stride := frames + 2*audioPad
	if size := channels * stride * 4; size >= mappedBytes {
		if bytes, unmap, err := mapAudio(mappedDir(), size); err == nil {
			samples := unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(bytes))), channels*stride)
			for ch := range a.data {
				a.data[ch] = samples[ch*stride : (ch+1)*stride : (ch+1)*stride]
			}
			// The file goes when the audio is unreachable; it holds no
			// pointer to itself, so the cleanup can run.
			runtime.AddCleanup(a, func(unmap func()) { unmap() }, unmap)
			return a
		}
		// Without a file (no space, no cache folder) the heap holds it.
	}
	for ch := range a.data {
		a.data[ch] = make([]float32, stride)
	}
	return a
}

// mappedDir is the folder of the mapped sources: the user cache folder,
// which is on disk where the temporary folder may be in memory (tmpfs).
func mappedDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return os.TempDir()
	}
	dir = filepath.Join(dir, "vst3go-ara")
	if os.MkdirAll(dir, 0o700) != nil {
		return os.TempDir()
	}
	return dir
}

// SampleRate returns the audio's sample rate.
func (a *SourceAudio) SampleRate() float64 { return a.sampleRate }

// Frames returns the frames per channel.
func (a *SourceAudio) Frames() int { return a.frames }

// Channels returns the channel count.
func (a *SourceAudio) Channels() int { return len(a.data) }

// Channel returns the frames of a channel. Do not modify them, and keep
// the SourceAudio while using them: the frames of a large source are in a
// file mapping that goes away with it.
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
