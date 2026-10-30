package nam

import (
	"encoding/json"
	"errors"
	"fmt"
)

// lstm is NAM's LSTM: stacked cells run one frame at a time and a linear
// head on the last hidden state.
type lstm struct {
	in, out  int
	cells    []lstmCell
	head     []float32 // out × hidden, row-major
	headBias []float32
	hidden   int
	warm     int
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

func newLSTM(s spec) (*lstm, error) {
	var o object
	if err := json.Unmarshal(s.config, &o); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var layers, inputSize, hidden int
	in, out := 1, 1
	if err := errors.Join(o.get("num_layers", &layers), o.get("input_size", &inputSize), o.get("hidden_size", &hidden),
		o.opt("in_channels", &in), o.opt("out_channels", &out)); err != nil {
		return nil, err
	}
	switch {
	case in < 1 || out < 1 || hidden < 1 || layers < 0:
		return nil, fmt.Errorf("%d to %d channels, hidden size %d and %d layers", in, out, hidden, layers)
	case inputSize != in:
		return nil, fmt.Errorf("input size %d for %d input channels", inputSize, in)
	}
	r := &reader{w: s.weights}
	m := &lstm{in: in, out: out, hidden: hidden}
	for i := range layers {
		cin := hidden
		if i == 0 {
			cin = inputSize
		}
		m.cells = append(m.cells, lstmCell{
			in: cin, hidden: hidden,
			w: r.take(4 * hidden * (cin + hidden)), b: r.take(4 * hidden),
			h0: r.take(hidden), c0: r.take(hidden),
			xh: make([]float32, cin+hidden), c: make([]float32, hidden), gates: make([]float32, 4*hidden),
		})
	}
	m.head = r.take(out * hidden)
	m.headBias = r.take(out)
	if err := r.done(); err != nil {
		return nil, err
	}
	// Half a second, as in NAM; a frame when the rate is unknown.
	m.warm = max(1, int(0.5*s.rate))
	return m, nil
}

func (m *lstm) channels() (int, int) { return m.in, m.out }

func (m *lstm) process(in, out matrix, n int) {
	if len(m.cells) == 0 {
		for r := range m.out {
			y := out.row(r, 0, n)
			if r < m.in {
				copy(y, in.row(r, 0, n))
			} else {
				clear(y)
			}
		}
		return
	}
	first, last := &m.cells[0], &m.cells[len(m.cells)-1]
	for t := range n {
		for ch := range m.in {
			first.xh[ch] = in.data[ch*in.stride+t]
		}
		for i := range m.cells {
			c := &m.cells[i]
			if i > 0 {
				copy(c.xh[:c.in], m.cells[i-1].xh[m.cells[i-1].in:])
			}
			c.step()
		}
		h := last.xh[last.in:]
		for o := range m.out {
			row := m.head[o*m.hidden : (o+1)*m.hidden]
			var y float32
			for j, v := range h {
				y += row[j] * v
			}
			out.data[o*out.stride+t] = y + m.headBias[o]
		}
	}
}

func (c *lstmCell) step() {
	cols := c.in + c.hidden
	xh := c.xh[:cols]
	for r := range c.gates {
		row := c.w[r*cols : (r+1)*cols : (r+1)*cols]
		var s float32
		for j, v := range xh {
			s += row[j] * v
		}
		c.gates[r] = s + c.b[r]
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

func (m *lstm) prewarm() int { return m.warm }
