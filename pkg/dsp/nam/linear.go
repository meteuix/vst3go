package nam

import (
	"encoding/json"
	"errors"
	"fmt"
)

// linear is NAM's Linear model: FIR filters from each input to each
// output. Equal channel counts share one filter per channel pair; one
// input feeds every output, or every input sums into one output, through
// a filter each.
type linear struct {
	in, out int
	w       [][]float32 // reversed: w[k][len-1] multiplies the newest input
	bias    []float32
	// buf holds len(w[0])-1 frames of each input before pos, then room
	// for new frames.
	buf matrix
	pos int
}

func newLinear(s spec) (*linear, error) {
	var o object
	if err := json.Unmarshal(s.config, &o); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var rf int
	var bias bool
	in, out := 1, 1
	if err := errors.Join(o.get("receptive_field", &rf), o.get("bias", &bias),
		o.opt("in_channels", &in), o.opt("out_channels", &out)); err != nil {
		return nil, err
	}
	switch {
	case rf < 1:
		return nil, fmt.Errorf("receptive field %d", rf)
	case in < 1 || out < 1 || (in != out && in != 1 && out != 1):
		return nil, fmt.Errorf("%d to %d channels", in, out)
	}
	kernels, biases := max(in, out), out
	if in == out {
		kernels, biases = 1, 1
	}
	r := &reader{w: s.weights}
	m := &linear{in: in, out: out, bias: make([]float32, out)}
	for range kernels {
		taps := r.take(rf)
		w := make([]float32, rf)
		for i, v := range taps {
			w[rf-1-i] = v
		}
		m.w = append(m.w, w)
	}
	if bias {
		b := r.take(biases)
		for ch := range m.bias {
			m.bias[ch] = b[min(ch, biases-1)]
		}
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	m.buf = matrix{make([]float32, in*(rf-1+span)), rf - 1 + span}
	m.reset()
	return m, nil
}

func (m *linear) channels() (int, int) { return m.in, m.out }

func (m *linear) process(in, out matrix, n int) {
	hist := len(m.w[0]) - 1
	if m.pos+n > m.buf.stride {
		for ch := range m.in {
			copy(m.buf.row(ch, 0, hist), m.buf.row(ch, m.pos-hist, hist))
		}
		m.pos = hist
	}
	for ch := range m.in {
		copy(m.buf.row(ch, m.pos, n), in.row(ch, 0, n))
	}
	for ch := range m.out {
		y := out.row(ch, 0, n)
		for t := range y {
			y[t] = m.bias[ch]
		}
	}
	for path := range max(m.in, m.out) {
		ich, och, k := min(path, m.in-1), min(path, m.out-1), min(path, len(m.w)-1)
		w, y := m.w[k], out.row(och, 0, n)
		for t := range y {
			x := m.buf.row(ich, m.pos+t-hist, hist+1)
			var s float32
			for j, v := range w {
				s += v * x[j]
			}
			y[t] += s
		}
	}
	m.pos += n
}

func (m *linear) reset() {
	clear(m.buf.data)
	m.pos = len(m.w[0]) - 1
}

func (m *linear) prewarm() int { return 0 }
