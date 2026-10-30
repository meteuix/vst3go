package nam

import (
	"math"

	"github.com/th-release/vst3go/v2/pkg/dsp/resample"
)

// Runner runs a Model at a host sample rate. When the rates differ it
// resamples to the model's rate and back, which delays the output by
// Latency frames; when they match it adds nothing.
type Runner struct {
	model   *Model
	up      *resample.Resampler // host → model
	down    *resample.Resampler // model → host
	latency int
	work    []float32 // model-rate frames for one piece
	fifo    []float32 // host-rate output ring
	head    int       // next frame to read from fifo
	fill    int       // frames in fifo
}

// NewRunner returns a Runner for m at hostRate. It allocates; call it off
// the audio thread.
func NewRunner(m *Model, hostRate float64) (*Runner, error) {
	r := &Runner{model: m}
	if hostRate == m.SampleRate() {
		return r, nil
	}
	var err error
	if r.up, err = resample.NewResampler(hostRate, m.SampleRate()); err != nil {
		return nil, err
	}
	if r.down, err = resample.NewResampler(m.SampleRate(), hostRate); err != nil {
		return nil, err
	}
	// Each resampler holds back Latency input frames; the up one's are
	// host frames, the down one's model frames. Two more cover the
	// rounding of the per-piece output counts.
	r.latency = r.up.Latency() + int(math.Ceil(float64(r.down.Latency())*hostRate/m.SampleRate())) + 2
	// A piece of chunk host frames makes at most ceil(chunk·ratio)+1 model
	// frames; the ring holds the latency plus one piece with room to
	// spare for the rounding.
	r.work = make([]float32, int(math.Ceil(chunk*m.SampleRate()/hostRate))+1)
	r.fifo = make([]float32, 2*(r.latency+chunk))
	r.Reset()
	return r, nil
}

// Model returns the model the Runner runs.
func (r *Runner) Model() *Model { return r.model }

// Latency returns how many host frames the output lags the input.
func (r *Runner) Latency() int { return r.latency }

// Process runs in through the model into out (the same length; it may
// be in itself). It does not allocate.
func (r *Runner) Process(in, out []float32) {
	if r.up == nil {
		r.model.Process(in, out)
		return
	}
	out = out[:len(in)]
	for len(in) > 0 {
		n := min(len(in), chunk)
		r.piece(in[:n], out[:n])
		in, out = in[n:], out[n:]
	}
}

func (r *Runner) piece(in, out []float32) {
	_, made := r.up.Process(in, r.work)
	r.model.Process(r.work[:made], r.work[:made])
	src := r.work[:made]
	for len(src) > 0 {
		// Write into the free run of the ring after its last frame.
		tail := (r.head + r.fill) % len(r.fifo)
		room := len(r.fifo) - tail
		if free := len(r.fifo) - r.fill; room > free {
			room = free
		}
		used, produced := r.down.Process(src, r.fifo[tail:tail+room])
		r.fill += produced
		src = src[used:]
	}
	for i := range out {
		out[i] = r.fifo[r.head]
		r.head = (r.head + 1) % len(r.fifo)
	}
	r.fill -= len(out)
}

// Reset clears the model and the resamplers.
func (r *Runner) Reset() {
	r.model.Reset()
	if r.up == nil {
		return
	}
	r.up.Reset()
	r.down.Reset()
	clear(r.fifo)
	r.head, r.fill = 0, r.latency
}
