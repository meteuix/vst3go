package nam

import (
	"encoding/json"
	"errors"
	"fmt"
)

type waveNetConfig struct {
	Layers    []layerArrayConfig `json:"layers"`
	Head      json.RawMessage    `json:"head"`
	HeadScale float32            `json:"head_scale"`
}

type layerArrayConfig struct {
	InputSize     int    `json:"input_size"`
	ConditionSize int    `json:"condition_size"`
	HeadSize      int    `json:"head_size"`
	Channels      int    `json:"channels"`
	KernelSize    int    `json:"kernel_size"`
	Dilations     []int  `json:"dilations"`
	Activation    string `json:"activation"`
	Gated         bool   `json:"gated"`
	HeadBias      bool   `json:"head_bias"`
}

// waveNet is NAM's WaveNet: layer arrays of gated or plain dilated
// convolutions, each adding its activations into a head that the next
// array continues, conditioned on the input signal.
type waveNet struct {
	arrays []*layerArray
	cond   matrix   // 1×chunk: the input
	heads  []matrix // heads[0] is zero; heads[i+1] is array i's head output
	scale  float32
	warm   int
}

// layerArray is a stack of layers sharing a history position.
type layerArray struct {
	channels  int
	rechannel dense // input → channels
	layers    []waveNetLayer
	head      dense // channels → head size
	// bufs[i] is layer i's input, channels × cols: hist columns of
	// history before pos, then room for new frames.
	bufs            []matrix
	hist, cols, pos int
	z               matrix // the layers' pre-activations, zc × chunk
	out             matrix // the last layer's output, channels × chunk
}

type waveNetLayer struct {
	kernel, dilation int
	// in is the dilated convolution and the condition's mix-in as one
	// zc × (kernel·channels + 1) matrix: tap k's inputs, oldest tap
	// first, then the condition.
	in     weights
	inBias []float32
	rows   []*float32 // scratch: the inputs' rows for in
	mix    dense      // channels → channels
	act    activation
	gated  bool
}

// span is the frames a layer array's buffers hold past their history;
// each time they fill, the history moves back to the start.
const span = 8 * chunk

func newWaveNet(raw json.RawMessage, params []float32) (*waveNet, error) {
	var c waveNetConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if len(c.Head) > 0 && string(c.Head) != "null" {
		return nil, errors.New("a head network is not supported")
	}
	if len(c.Layers) == 0 {
		return nil, errors.New("no layer arrays")
	}
	r := &reader{w: params}
	w := &waveNet{cond: matrix{make([]float32, chunk), chunk}, warm: 1}
	for i, a := range c.Layers {
		wantIn := 1
		if i > 0 {
			wantIn = c.Layers[i-1].Channels
			if a.Channels != c.Layers[i-1].HeadSize {
				return nil, fmt.Errorf("layer array %d has %d channels, the head before it %d", i, a.Channels, c.Layers[i-1].HeadSize)
			}
		}
		switch {
		case a.InputSize != wantIn:
			return nil, fmt.Errorf("layer array %d has input size %d, want %d", i, a.InputSize, wantIn)
		case a.ConditionSize != 1:
			return nil, fmt.Errorf("layer array %d has condition size %d, want 1", i, a.ConditionSize)
		case a.Channels < 1 || a.HeadSize < 1 || a.KernelSize < 1 || len(a.Dilations) == 0:
			return nil, fmt.Errorf("layer array %d has an empty dimension", i)
		}
		la, err := newLayerArray(a, r)
		if err != nil {
			return nil, fmt.Errorf("layer array %d: %w", i, err)
		}
		if i == 0 {
			w.heads = append(w.heads, matrix{make([]float32, a.Channels*chunk), chunk})
		}
		w.heads = append(w.heads, matrix{make([]float32, a.HeadSize*chunk), chunk})
		w.arrays = append(w.arrays, la)
		for _, l := range la.layers {
			w.warm += l.dilation * (l.kernel - 1)
		}
	}
	if last := c.Layers[len(c.Layers)-1].HeadSize; last != 1 {
		return nil, fmt.Errorf("the last head has size %d, want 1", last)
	}
	// NAM stores the head scale as the last weight, which overrides the
	// configuration's.
	w.scale = r.take(1)[0]
	if err := r.done(); err != nil {
		return nil, err
	}
	return w, nil
}

func newLayerArray(c layerArrayConfig, r *reader) (*layerArray, error) {
	act, err := activationNamed(c.Activation)
	if err != nil {
		return nil, err
	}
	ch, zc := c.Channels, c.Channels
	if c.Gated {
		zc *= 2
	}
	a := &layerArray{channels: ch}
	a.rechannel.load(r, c.InputSize, ch, false)
	for _, d := range c.Dilations {
		if d < 1 {
			return nil, fmt.Errorf("dilation %d", d)
		}
		l := waveNetLayer{kernel: c.KernelSize, dilation: d, act: act, gated: c.Gated}
		// NAM flattens the convolution as [out][in][tap].
		flat := r.take(zc * ch * c.KernelSize)
		l.inBias = r.take(zc)
		mixin := r.take(zc * c.ConditionSize)
		cols := c.KernelSize*ch + c.ConditionSize
		w := make([]float32, zc*cols)
		for o := 0; o < zc; o++ {
			for i := 0; i < ch; i++ {
				for k := 0; k < c.KernelSize; k++ {
					w[o*cols+k*ch+i] = flat[(o*ch+i)*c.KernelSize+k]
				}
			}
			copy(w[o*cols+c.KernelSize*ch:(o+1)*cols], mixin[o*c.ConditionSize:])
		}
		l.in = newWeights(w, cols, zc)
		l.rows = make([]*float32, cols)
		l.mix.load(r, ch, ch, true)
		a.layers = append(a.layers, l)
		a.hist = max(a.hist, d*(c.KernelSize-1))
	}
	a.head.load(r, ch, c.HeadSize, c.HeadBias)
	a.cols = a.hist + span
	for range a.layers {
		a.bufs = append(a.bufs, matrix{make([]float32, ch*a.cols), a.cols})
	}
	a.z = matrix{make([]float32, zc*chunk), chunk}
	a.out = matrix{make([]float32, ch*chunk), chunk}
	a.pos = a.hist
	return a, nil
}

func (w *waveNet) process(in, out []float32) {
	n := len(in)
	copy(w.cond.data, in)
	clear(w.heads[0].data)
	for i, a := range w.arrays {
		src := w.cond
		if i > 0 {
			src = w.arrays[i-1].out
		}
		a.process(src, w.cond, w.heads[i], w.heads[i+1], n)
	}
	head := w.heads[len(w.heads)-1].data[:n]
	for t, v := range head {
		out[t] = w.scale * v
	}
}

func (w *waveNet) reset() {
	for _, a := range w.arrays {
		for _, b := range a.bufs {
			clear(b.data)
		}
		a.pos = a.hist
	}
}

func (w *waveNet) prewarm(float64) int { return w.warm }

// at returns m starting at column col.
func (m matrix) at(col int) matrix { return matrix{m.data[col:], m.stride} }

// process runs n frames of in (columns [0,n)) through the array, adding
// the layers' activations to headIn and writing the head's output to
// headOut and the last layer's to a.out.
func (a *layerArray) process(in, cond, headIn, headOut matrix, n int) {
	if a.pos+n > a.cols {
		for _, b := range a.bufs {
			for c := 0; c < a.channels; c++ {
				copy(b.row(c, 0, a.hist), b.row(c, a.pos-a.hist, a.hist))
			}
		}
		a.pos = a.hist
	}
	a.rechannel.apply(a.bufs[0].at(a.pos), in, 0, n)
	ch := a.channels
	for i := range a.layers {
		l := &a.layers[i]
		src := a.bufs[i]
		for o, b := range l.inBias {
			y := a.z.row(o, 0, n)
			for t := range y {
				y[t] = b
			}
		}
		for k := 0; k < l.kernel; k++ {
			from := a.pos - (l.kernel-1-k)*l.dilation
			for c := 0; c < ch; c++ {
				l.rows[k*ch+c] = &src.row(c, from, n)[0]
			}
		}
		for c := range l.in.in - l.kernel*ch {
			l.rows[l.kernel*ch+c] = &cond.row(c, 0, n)[0]
		}
		mac(a.z, l.rows, l.in, n)
		if !l.gated {
			for o := 0; o < ch; o++ {
				l.act(a.z.row(o, 0, n))
			}
		} else {
			for o := 0; o < ch; o++ {
				y, g := a.z.row(o, 0, n), a.z.row(ch+o, 0, n)
				l.act(y)
				sigmoidAll(g)
				for t := range y {
					y[t] *= g[t]
				}
			}
		}
		dst := a.out
		if i+1 < len(a.layers) {
			dst = a.bufs[i+1].at(a.pos)
		}
		// The head sums the activations; the next layer gets this one's
		// input plus their mix.
		for o := 0; o < ch; o++ {
			h, z, y, x, b := headIn.row(o, 0, n), a.z.row(o, 0, n), dst.row(o, 0, n), src.row(o, a.pos, n), l.mix.bias[o]
			for t := range z {
				h[t] += z[t]
				y[t] = x[t] + b
			}
			l.mix.rows[o] = &z[0]
		}
		mac(dst, l.mix.rows, l.mix.weights, n)
	}
	a.head.apply(headOut, headIn, 0, n)
	a.pos += n
}
