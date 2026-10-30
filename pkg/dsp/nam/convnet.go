package nam

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// convNet is NAM's ConvNet: blocks of a causal two-tap dilated
// convolution, an optional batch norm and an activation, then a linear
// head.
type convNet struct {
	in, out int
	blocks  []convBlock
	head    *conv
	warm    int
}

type convBlock struct {
	conv       *conv
	scale, loc []float32 // the batch norm, nil without one
	act        activation
	out        matrix
}

func newConvNet(s spec) (*convNet, error) {
	var o object
	if err := json.Unmarshal(s.config, &o); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var channels int
	var dilations []int
	var batchnorm bool
	in, out, groups := 1, 1, 1
	if err := errors.Join(o.get("channels", &channels), o.get("dilations", &dilations), o.get("batchnorm", &batchnorm),
		o.opt("groups", &groups), o.opt("in_channels", &in), o.opt("out_channels", &out)); err != nil {
		return nil, err
	}
	if !o.has("activation") {
		return nil, errors.New(`no "activation"`)
	}
	act, err := parseActivation(o["activation"])
	if err != nil {
		return nil, err
	}
	switch {
	case len(dilations) == 0:
		return nil, errors.New("no blocks")
	case in < 1 || out < 1 || channels < 1:
		return nil, fmt.Errorf("%d to %d channels through %d", in, out, channels)
	}
	if err := act.check(channels); err != nil {
		return nil, err
	}
	r := &reader{w: s.weights}
	m := &convNet{in: in, out: out, warm: 1}
	for i, d := range dilations {
		cin := channels
		if i == 0 {
			cin = in
		}
		c, err := newConv(r, cin, channels, 2, d, groups, !batchnorm)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		b := convBlock{conv: c, act: act, out: newBlock(channels)}
		if batchnorm {
			mean, variance, weight, bias := r.take(channels), r.take(channels), r.take(channels), r.take(channels)
			eps := r.take(1)[0]
			b.scale, b.loc = make([]float32, channels), make([]float32, channels)
			for j := range channels {
				b.scale[j] = weight[j] / float32(math.Sqrt(float64(eps+variance[j])))
				b.loc[j] = bias[j] - b.scale[j]*mean[j]
			}
		}
		m.blocks = append(m.blocks, b)
		m.warm += d
	}
	if m.head, err = newConv(r, channels, out, 1, 1, 1, true); err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *convNet) channels() (int, int) { return m.in, m.out }

func (m *convNet) process(in, out matrix, n int) {
	x := in
	for i := range m.blocks {
		b := &m.blocks[i]
		b.conv.apply(b.out, x, n)
		for r := range b.conv.out {
			y := b.out.row(r, 0, n)
			if b.scale != nil {
				s, l := b.scale[r], b.loc[r]
				for t := range y {
					y[t] = y[t]*s + l
				}
			}
			b.act.apply(r, y)
		}
		x = b.out
	}
	m.head.apply(out, x, n)
}

func (m *convNet) reset() {
	for _, b := range m.blocks {
		b.conv.reset()
	}
}

func (m *convNet) prewarm() int { return m.warm }
