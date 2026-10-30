package nam

import (
	"encoding/json"
	"fmt"
)

type lstmConfig struct {
	InputSize  int `json:"input_size"`
	HiddenSize int `json:"hidden_size"`
	NumLayers  int `json:"num_layers"`
}

// lstm is NAM's LSTM: stacked cells run one sample at a time and a linear
// head on the last hidden state.
type lstm struct {
	cells    []lstmCell
	head     []float32
	headBias float32
}

type lstmCell struct {
	in, hidden int
	w          []float32 // 4·hidden × (in+hidden), row-major; gates i, f, g, o
	b          []float32
	h0, c0     []float32 // the trained initial state
	xh         []float32 // the input followed by the hidden state
	c          []float32
	gates      []float32
}

func newLSTM(raw json.RawMessage, params []float32) (*lstm, error) {
	var c lstmConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.InputSize != 1 || c.HiddenSize < 1 || c.NumLayers < 1 {
		return nil, fmt.Errorf("input size %d, hidden size %d and %d layers", c.InputSize, c.HiddenSize, c.NumLayers)
	}
	r := &reader{w: params}
	m := &lstm{}
	for i := range c.NumLayers {
		in, h := c.HiddenSize, c.HiddenSize
		if i == 0 {
			in = c.InputSize
		}
		cell := lstmCell{
			in: in, hidden: h,
			w: r.take(4 * h * (in + h)), b: r.take(4 * h),
			h0: r.take(h), c0: r.take(h),
			xh: make([]float32, in+h), c: make([]float32, h), gates: make([]float32, 4*h),
		}
		m.cells = append(m.cells, cell)
	}
	m.head = r.take(c.HiddenSize)
	m.headBias = r.take(1)[0]
	if err := r.done(); err != nil {
		return nil, err
	}
	m.reset()
	return m, nil
}

func (m *lstm) process(in, out []float32) {
	last := &m.cells[len(m.cells)-1]
	for t, x := range in {
		m.cells[0].xh[0] = x
		for i := range m.cells {
			c := &m.cells[i]
			if i > 0 {
				copy(c.xh[:c.in], m.cells[i-1].xh[m.cells[i-1].in:])
			}
			c.step()
		}
		y := m.headBias
		for j, h := range last.xh[last.in:] {
			y += m.head[j] * h
		}
		out[t] = y
	}
}

func (c *lstmCell) step() {
	cols := c.in + c.hidden
	xh := c.xh[:cols]
	for r := range c.gates {
		row := c.w[r*cols : (r+1)*cols : (r+1)*cols]
		s := c.b[r]
		for j, v := range xh {
			s += row[j] * v
		}
		c.gates[r] = s
	}
	h := c.hidden
	g := c.gates[:4*h]
	for j := range h {
		c.c[j] = fastSigmoid(g[h+j])*c.c[j] + fastSigmoid(g[j])*fastTanh(g[2*h+j])
	}
	for j := range h {
		xh[c.in+j] = fastSigmoid(g[3*h+j]) * fastTanh(c.c[j])
	}
}

func (m *lstm) reset() {
	for i := range m.cells {
		c := &m.cells[i]
		clear(c.xh[:c.in])
		copy(c.xh[c.in:], c.h0)
		copy(c.c, c.c0)
	}
}

// prewarm is half a second, as in NAM since 0.2.
func (m *lstm) prewarm(sampleRate float64) int { return int(0.5 * sampleRate) }
