package nam

import (
	"encoding/json"
	"fmt"
)

type linearConfig struct {
	ReceptiveField int  `json:"receptive_field"`
	Bias           bool `json:"bias"`
}

// linear is NAM's Linear model: an FIR filter over the input.
type linear struct {
	w    []float32 // reversed: w[len-1] multiplies the newest input
	bias float32
	// buf holds len(w)-1 frames of history before pos, then room for new
	// frames.
	buf []float32
	pos int
}

func newLinear(raw json.RawMessage, params []float32) (*linear, error) {
	var c linearConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.ReceptiveField < 1 {
		return nil, fmt.Errorf("receptive field %d", c.ReceptiveField)
	}
	r := &reader{w: params}
	taps := r.take(c.ReceptiveField)
	m := &linear{w: make([]float32, len(taps))}
	for i, v := range taps {
		m.w[len(taps)-1-i] = v
	}
	if c.Bias {
		m.bias = r.take(1)[0]
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	m.buf = make([]float32, len(m.w)-1+span)
	m.reset()
	return m, nil
}

func (m *linear) process(in, out []float32) {
	hist := len(m.w) - 1
	if m.pos+len(in) > len(m.buf) {
		copy(m.buf, m.buf[m.pos-hist:m.pos])
		m.pos = hist
	}
	copy(m.buf[m.pos:], in)
	for t := range in {
		x := m.buf[m.pos+t-hist : m.pos+t+1]
		y := m.bias
		for j, w := range m.w {
			y += w * x[j]
		}
		out[t] = y
	}
	m.pos += len(in)
}

func (m *linear) reset() {
	clear(m.buf)
	m.pos = len(m.w) - 1
}

func (m *linear) prewarm(float64) int { return 0 }
