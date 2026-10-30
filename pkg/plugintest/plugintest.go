// Package plugintest runs plugins in Go tests, without a host.
//
//	func TestGain(t *testing.T) {
//		p := plugintest.New(t, gainPlugin{})
//		p.SetParameterPlain(paramGain, -6)
//		out := p.Render(plugintest.Channels(2, plugintest.Sine(1000, 48000, 4800, 0.5)), 2)
//		if db := plugintest.PeakDB(out[0]); math.Abs(db-(-12)) > 0.1 { ... }
//	}
//
// An Instance is a plugin.Offline: the processor runs through the real
// runtime, with sample-accurate automation, MIDI mapping, programs,
// smoothing and state. Its helpers fail the test instead of returning
// errors.
package plugintest

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/audiofile"
	"github.com/meteuix/vst3go/v2/pkg/midi"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

// Instance is a plugin instance driven by a test.
type Instance struct {
	*plugin.Offline
	t testing.TB
}

// New creates and activates an instance of p; it is closed when the test
// ends. The optional setup defaults to 48 kHz, 4096-sample blocks, 32-bit.
func New(t testing.TB, p plugin.Plugin, setup ...plugin.OfflineSetup) *Instance {
	t.Helper()
	var s plugin.OfflineSetup
	if len(setup) > 0 {
		s = setup[0]
	}
	o, err := plugin.NewOffline(p, s)
	if err != nil {
		t.Fatalf("plugintest: create %s: %v", p.GetInfo().Name, err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return &Instance{Offline: o, t: t}
}

// Render processes in through the main buses and returns outChannels
// output channels of the same length. Use RenderSilence for instruments.
func (i *Instance) Render(in [][]float32, outChannels int) [][]float32 {
	i.t.Helper()
	n := 0
	if len(in) > 0 {
		n = len(in[0])
	}
	out := Silence(outChannels, n)
	if err := i.Process(in, out); err != nil {
		i.t.Fatalf("plugintest: process: %v", err)
	}
	return out
}

// RenderSilence processes n samples of silent input (inChannels channels)
// and returns outChannels output channels, for instruments and tails.
func (i *Instance) RenderSilence(n, inChannels, outChannels int) [][]float32 {
	i.t.Helper()
	var in [][]float32
	if inChannels > 0 {
		in = Silence(inChannels, n)
	}
	out := Silence(outChannels, n)
	if err := i.Process(in, out); err != nil {
		i.t.Fatalf("plugintest: process: %v", err)
	}
	return out
}

// AllocsPerProcess returns the average number of heap allocations of one
// Process(in, out) call over runs calls, after a warm-up call. The offline
// runtime itself does not allocate, so a non-zero result means the
// processor allocates on the audio thread. before, if not nil, runs ahead
// of each call to queue events or automation (Note, SendEvent, Automate
// reuse their queues).
func (i *Instance) AllocsPerProcess(runs int, in, out [][]float32, before func()) float64 {
	i.t.Helper()
	var err error
	allocs := testing.AllocsPerRun(runs, func() {
		if before != nil {
			before()
		}
		if e := i.Process(in, out); e != nil && err == nil {
			err = e
		}
	})
	if err != nil {
		i.t.Fatalf("plugintest: process: %v", err)
	}
	return allocs
}

// Set sets a parameter to a plain value.
func (i *Instance) Set(id uint32, plain float64) {
	i.t.Helper()
	if err := i.SetParameterPlain(id, plain); err != nil {
		i.t.Fatalf("plugintest: set parameter %d: %v", id, err)
	}
}

// Normalized converts a plain value of a parameter to its normalized value,
// for Automate.
func (i *Instance) Normalized(id uint32, plain float64) float64 {
	i.t.Helper()
	p, ok := i.Processor().GetParameters().GetOK(id)
	if !ok {
		i.t.Fatalf("plugintest: no parameter %d", id)
	}
	return p.Normalize(plain)
}

// Plain returns a parameter's plain value.
func (i *Instance) Plain(id uint32) float64 {
	i.t.Helper()
	_, plain, ok := i.Parameter(id)
	if !ok {
		i.t.Fatalf("plugintest: no parameter %d", id)
	}
	return plain
}

// Note queues a note-on at onset and its note-off at onset+length samples
// for the next render (length 0: no note-off).
func (i *Instance) Note(channel, note uint8, velocity float32, onset, length int) {
	i.SendEvent(midi.NoteOn(channel, note, velocity).At(int32(onset)))
	if length > 0 {
		i.SendEvent(midi.NoteOff(channel, note, 0).At(int32(onset + length)))
	}
}

// SaveAudio writes channels to an audio file at the instance's sample
// rate, in the format the name gives (.wav, .aif, .aiff, .flac): 32-bit
// float for WAV and AIFF, 24-bit for FLAC. Use it to listen to renders.
func (i *Instance) SaveAudio(path string, channels [][]float32) {
	i.t.Helper()
	e := audiofile.Float32
	if strings.EqualFold(filepath.Ext(path), ".flac") {
		e = audiofile.PCM24
	}
	b := &audiofile.Buffer{SampleRate: i.SampleRate(), Channels: channels}
	if err := audiofile.Save(path, b, e); err != nil {
		i.t.Fatalf("plugintest: save %s: %v", path, err)
	}
}

// LoadAudio decodes an audio file (any format pkg/audiofile reads) and
// returns its channels and sample rate, for rendering recorded material.
func LoadAudio(t testing.TB, path string) ([][]float32, float64) {
	t.Helper()
	b, err := audiofile.Load(path)
	if err != nil {
		t.Fatalf("plugintest: load %s: %v", path, err)
	}
	return b.Channels, b.SampleRate
}

// Signals

// Silence returns channels of n zero samples.
func Silence(channels, n int) [][]float32 {
	out := make([][]float32, channels)
	for ch := range out {
		out[ch] = make([]float32, n)
	}
	return out
}

// Channels returns channels copies of signal.
func Channels(channels int, signal []float32) [][]float32 {
	out := make([][]float32, channels)
	for ch := range out {
		out[ch] = append([]float32(nil), signal...)
	}
	return out
}

// Sine returns n samples of a sine at freq Hz.
func Sine(freq, sampleRate float64, n int, amplitude float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = amplitude * float32(math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
	}
	return out
}

// Impulse returns n samples with a single 1 at index at.
func Impulse(n, at int) []float32 {
	out := make([]float32, n)
	if at >= 0 && at < n {
		out[at] = 1
	}
	return out
}

// Constant returns n samples of value.
func Constant(n int, value float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = value
	}
	return out
}

// Analysis

// Peak returns the largest absolute sample.
func Peak(x []float32) float64 {
	peak := 0.0
	for _, v := range x {
		peak = max(peak, math.Abs(float64(v)))
	}
	return peak
}

// RMS returns the root mean square of x.
func RMS(x []float32) float64 {
	if len(x) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

// DB converts a linear amplitude to decibels (-Inf for 0).
func DB(amplitude float64) float64 { return 20 * math.Log10(amplitude) }

// PeakDB is DB(Peak(x)).
func PeakDB(x []float32) float64 { return DB(Peak(x)) }

// FirstNonZero returns the index of the first sample with |x| > threshold,
// or -1.
func FirstNonZero(x []float32, threshold float64) int {
	for i, v := range x {
		if math.Abs(float64(v)) > threshold {
			return i
		}
	}
	return -1
}

// ZeroCrossingFrequency estimates the frequency of a periodic signal from
// its rising zero crossings.
func ZeroCrossingFrequency(x []float32, sampleRate float64) float64 {
	first, last, count := -1, -1, 0
	for i := 1; i < len(x); i++ {
		if x[i-1] <= 0 && x[i] > 0 {
			if first < 0 {
				first = i
			}
			last = i
			count++
		}
	}
	if count < 2 {
		return 0
	}
	return float64(count-1) * sampleRate / float64(last-first)
}
