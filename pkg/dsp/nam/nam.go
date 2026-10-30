// Package nam runs Neural Amp Modeler models (.nam files): the WaveNet,
// LSTM and Linear architectures of NAM's 0.5 file format, with the
// computation and weight layout of NeuralAmpModelerCore. Like the NAM
// plugin, "Tanh" activations use NAM's fast tanh approximation (within
// 5e-4 of tanh), so outputs match the plugin's rather than exactly the
// training framework's.
//
// Load or Parse reads a file; File.New builds a Model, whose Process runs
// on the audio thread without allocating. A Model runs at its file's
// sample rate; Runner resamples for a host running at another.
package nam

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultSampleRate is the rate of files that do not name one, as in NAM.
const DefaultSampleRate = 48000

// chunk is the most frames a network processes at once; Process splits
// longer blocks.
const chunk = 256

// File is a parsed .nam file. It is immutable; New builds Models from it.
type File struct {
	Version      string
	Architecture string // "WaveNet", "LSTM" or "Linear"
	// SampleRate is the rate the model was trained at, DefaultSampleRate
	// when the file does not say.
	SampleRate float64
	Metadata   Metadata

	config  json.RawMessage
	weights []float32
}

// Metadata is what a .nam file tells about the captured gear. Levels are
// nil when the file does not have them.
type Metadata struct {
	Name      string `json:"name"`
	ModeledBy string `json:"modeled_by"`
	GearType  string `json:"gear_type"`
	GearMake  string `json:"gear_make"`
	GearModel string `json:"gear_model"`
	ToneType  string `json:"tone_type"`
	// Loudness is the model's output loudness in dB for NAM's standard
	// input; NAM's output normalization adds -18 - Loudness dB.
	Loudness *float64 `json:"loudness"`
	// InputLevelDBu is the input level, in dBu, that a digital full-scale
	// sine reached the gear at when it was captured; OutputLevelDBu the
	// same for its output.
	InputLevelDBu  *float64 `json:"input_level_dbu"`
	OutputLevelDBu *float64 `json:"output_level_dbu"`
}

// Load reads a .nam file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return f, nil
}

// Parse reads the contents of a .nam file and checks that its
// architecture, configuration and weights build a model.
func Parse(data []byte) (*File, error) {
	var raw struct {
		Version      string          `json:"version"`
		Architecture string          `json:"architecture"`
		Config       json.RawMessage `json:"config"`
		Weights      []float32       `json:"weights"`
		SampleRate   *float64        `json:"sample_rate"`
		Metadata     *Metadata       `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	f := &File{Version: raw.Version, Architecture: raw.Architecture, SampleRate: DefaultSampleRate, config: raw.Config, weights: raw.Weights}
	if raw.SampleRate != nil {
		if !(*raw.SampleRate > 0) {
			return nil, fmt.Errorf("nam: sample rate %v", *raw.SampleRate)
		}
		f.SampleRate = *raw.SampleRate
	}
	if raw.Metadata != nil {
		f.Metadata = *raw.Metadata
	}
	if _, err := f.New(); err != nil {
		return nil, err
	}
	return f, nil
}

// network is a model's architecture.
type network interface {
	// process runs up to chunk frames; in and out may be the same slice.
	process(in, out []float32)
	// reset clears the state to the model's initial one.
	reset()
	// prewarm is the frames of silence that settle the state after reset.
	prewarm(sampleRate float64) int
}

// Model runs a model at its file's sample rate. It is not safe for
// concurrent use.
type Model struct {
	net        network
	sampleRate float64
	silence    [chunk]float32
	scratch    [chunk]float32
}

// New builds a Model with its own state, settled on silence. It
// allocates; call it off the audio thread.
func (f *File) New() (*Model, error) {
	var net network
	var err error
	switch f.Architecture {
	case "WaveNet":
		net, err = newWaveNet(f.config, f.weights)
	case "LSTM":
		net, err = newLSTM(f.config, f.weights)
	case "Linear":
		net, err = newLinear(f.config, f.weights)
	case "":
		return nil, errors.New("nam: no architecture")
	default:
		return nil, fmt.Errorf("nam: architecture %q is not supported", f.Architecture)
	}
	if err != nil {
		return nil, fmt.Errorf("nam: %s: %w", f.Architecture, err)
	}
	m := &Model{net: net, sampleRate: f.SampleRate}
	m.Reset()
	return m, nil
}

// SampleRate returns the rate the model runs at.
func (m *Model) SampleRate() float64 { return m.sampleRate }

// Process runs the model over in, writing out (the same length; it may
// be in itself). It does not allocate.
func (m *Model) Process(in, out []float32) {
	out = out[:len(in)]
	for len(in) > 0 {
		n := min(len(in), chunk)
		m.net.process(in[:n], out[:n])
		in, out = in[n:], out[n:]
	}
}

// Reset returns the model to its initial state and settles it on
// silence, as NAM does after loading. It does not allocate.
func (m *Model) Reset() {
	m.net.reset()
	for n := m.net.prewarm(m.sampleRate); n > 0; n -= chunk {
		m.net.process(m.silence[:min(n, chunk)], m.scratch[:min(n, chunk)])
	}
}
