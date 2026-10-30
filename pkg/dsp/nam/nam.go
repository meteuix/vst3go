// Package nam runs Neural Amp Modeler models (.nam files, format versions
// 0.5 to 0.7) with the computation and weight layout of
// NeuralAmpModelerCore: WaveNet (gated and blended layers, bottlenecks,
// grouped convolutions, layer and head 1x1 convolutions, FiLM, condition
// models and the post-stack head), LSTM, ConvNet, Linear, Sequential
// chains and the slimmable models of NAM's A2 captures (SlimmableContainer
// and slimmable WaveNets), with every activation NAM defines. Like the NAM
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
	"regexp"
	"strconv"
)

// DefaultSampleRate is the rate of files that do not name one, as in NAM.
const DefaultSampleRate = 48000

// chunk is the most frames a network processes at once; Process splits
// longer blocks.
const chunk = 256

// span is the frames a history buffer holds past its history; each time
// it fills, the history moves back to the start.
const span = 8 * chunk

// File is a parsed .nam file. It is immutable; New builds Models from it.
type File struct {
	Version string
	// Architecture is "WaveNet", "LSTM", "ConvNet", "Linear", "Sequential"
	// or "SlimmableContainer".
	Architecture string
	// SampleRate is the rate the model was trained at, DefaultSampleRate
	// when the file does not say.
	SampleRate float64
	Metadata   Metadata
	// Sizes are the sizes at which a slimmable model switches to a larger
	// network (see NewSized); nil when the model is not slimmable.
	Sizes []float64

	spec spec
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

// spec is one model of a file: the file itself, or a model nested in it
// (a condition model, a Sequential stage, a slimmable submodel).
type spec struct {
	version      string
	architecture string
	config       json.RawMessage
	weights      []float32
	rate         float64 // the training sample rate, -1 when unknown
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

// Parse reads the contents of a .nam file and checks that its version,
// architecture, configuration and weights build a model.
func Parse(data []byte) (*File, error) {
	var meta struct {
		Metadata *Metadata `json:"metadata"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	s, err := parseSpec(data)
	if err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	f := &File{Version: s.version, Architecture: s.architecture, SampleRate: DefaultSampleRate, spec: s}
	if meta.Metadata != nil {
		f.Metadata = *meta.Metadata
	}
	net, rate, err := build(s, 1)
	if err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	if in, out := net.channels(); in != 1 || out != 1 {
		return nil, fmt.Errorf("nam: the model has %d inputs and %d outputs, want 1 and 1", in, out)
	}
	if rate > 0 {
		f.SampleRate = rate
	}
	if f.Sizes, err = sizes(s); err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	return f, nil
}

var semver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// parseSpec reads a model object: version, architecture, config, weights
// and sample rate.
func parseSpec(data []byte) (spec, error) {
	var raw struct {
		Version      string          `json:"version"`
		Architecture string          `json:"architecture"`
		Config       json.RawMessage `json:"config"`
		Weights      []float32       `json:"weights"`
		SampleRate   *float64        `json:"sample_rate"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return spec{}, err
	}
	// NeuralAmpModelerCore reads 0.5.0 and later, and every 0.7 file.
	v := semver.FindStringSubmatch(raw.Version)
	if v == nil {
		return spec{}, fmt.Errorf("version %q is not supported", raw.Version)
	}
	major, _ := strconv.Atoi(v[1])
	minor, _ := strconv.Atoi(v[2])
	if major != 0 || minor < 5 || minor > 7 {
		return spec{}, fmt.Errorf("version %s is not supported", raw.Version)
	}
	if raw.Architecture == "" {
		return spec{}, errors.New("no architecture")
	}
	s := spec{version: raw.Version, architecture: raw.Architecture, config: raw.Config, weights: raw.Weights, rate: -1}
	if raw.SampleRate != nil {
		if !(*raw.SampleRate > 0) {
			return spec{}, fmt.Errorf("sample rate %v", *raw.SampleRate)
		}
		s.rate = *raw.SampleRate
	}
	return s, nil
}

// network is a model's architecture.
type network interface {
	// channels returns the input and output channel counts.
	channels() (in, out int)
	// process runs n (at most chunk) frames of in's rows into out's rows;
	// out must not overlap in.
	process(in, out matrix, n int)
	// reset clears the state to the model's initial one.
	reset()
	// prewarm is the frames of silence that settle the state after reset.
	prewarm() int
}

// build makes the network of s at a slimmable size (1 for full size; only
// the top model of a file is slimmed). It also returns the sample rate
// the network runs at, -1 when unknown.
func build(s spec, size float64) (network, float64, error) {
	var net network
	rate := s.rate
	var err error
	switch s.architecture {
	case "WaveNet":
		net, err = newWaveNet(s, size)
	case "LSTM":
		net, err = newLSTM(s)
	case "ConvNet":
		net, err = newConvNet(s)
	case "Linear":
		net, err = newLinear(s)
	case "Sequential":
		net, rate, err = newSequential(s)
	case "SlimmableContainer":
		net, rate, err = newContainer(s, size)
	default:
		return nil, 0, fmt.Errorf("architecture %q is not supported", s.architecture)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", s.architecture, err)
	}
	return net, rate, nil
}

// sizes returns the slimmable sizes of s, nil when it is not slimmable.
func sizes(s spec) ([]float64, error) {
	switch s.architecture {
	case "SlimmableContainer":
		return containerSizes(s)
	case "WaveNet":
		return waveNetSizes(s)
	}
	return nil, nil
}

// Model runs a model at its file's sample rate. It is not safe for
// concurrent use.
type Model struct {
	net        network
	sampleRate float64
	in, out    matrix
}

// New builds a Model with its own state, settled on silence. It
// allocates; call it off the audio thread.
func (f *File) New() (*Model, error) { return f.NewSized(1) }

// NewSized builds a Model of a slimmable model at a size from 0 (the
// smallest network) to 1 (the full one, as New), as NAM's slimmable size
// control does; File.Sizes lists where the network changes. Other models
// ignore the size. It allocates; call it off the audio thread.
func (f *File) NewSized(size float64) (*Model, error) {
	net, _, err := build(f.spec, min(max(size, 0), 1))
	if err != nil {
		return nil, fmt.Errorf("nam: %w", err)
	}
	m := &Model{net: net, sampleRate: f.SampleRate, in: newBlock(1), out: newBlock(1)}
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
		copy(m.in.data, in[:n])
		m.net.process(m.in, m.out, n)
		copy(out, m.out.data[:n])
		in, out = in[n:], out[n:]
	}
}

// Reset returns the model to its initial state and settles it on
// silence, as NAM does after loading. It does not allocate.
func (m *Model) Reset() {
	m.net.reset()
	clear(m.in.data)
	for n := m.net.prewarm(); n > 0; n -= chunk {
		m.net.process(m.in, m.out, min(n, chunk))
	}
}
