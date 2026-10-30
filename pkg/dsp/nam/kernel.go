package nam

import (
	"encoding/json"
	"fmt"
	"math"
	"unsafe"
)

// Activation kinds, as NAM names them.
const (
	actTanh = iota
	actFasttanh
	actHardtanh
	actLeakyHardtanh
	actReLU
	actLeakyReLU
	actPReLU
	actSigmoid
	actSiLU
	actHardswish
	actSoftsign
)

var activationKinds = map[string]int{
	"Tanh":          actTanh,
	"Fasttanh":      actFasttanh,
	"Hardtanh":      actHardtanh,
	"LeakyHardtanh": actLeakyHardtanh,
	"LeakyHardTanh": actLeakyHardtanh,
	"ReLU":          actReLU,
	"LeakyReLU":     actLeakyReLU,
	"PReLU":         actPReLU,
	"Sigmoid":       actSigmoid,
	"SiLU":          actSiLU,
	"Hardswish":     actHardswish,
	"Softsign":      actSoftsign,
}

// activation is one of NAM's nonlinearities with its parameters.
type activation struct {
	kind   int
	slope  float32   // LeakyReLU
	slopes []float32 // PReLU: channel c's slope is slopes[c%len(slopes)]
	// LeakyHardtanh: the linear range and the slopes outside it.
	lo, hi, loSlope, hiSlope float32
}

// parseActivation reads an activation as NAM writes it: a name, or an
// object with a "type" and its parameters.
func parseActivation(raw json.RawMessage) (activation, error) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		kind, ok := activationKinds[name]
		if !ok {
			return activation{}, fmt.Errorf("activation %q is not supported", name)
		}
		return activation{kind: kind, slope: 0.01, slopes: []float32{0.01}, lo: -1, hi: 1, loSlope: 0.01, hiSlope: 0.01}, nil
	}
	var o struct {
		Type           string    `json:"type"`
		NegativeSlope  *float32  `json:"negative_slope"`
		NegativeSlopes []float32 `json:"negative_slopes"`
		MinVal         *float32  `json:"min_val"`
		MaxVal         *float32  `json:"max_val"`
		MinSlope       *float32  `json:"min_slope"`
		MaxSlope       *float32  `json:"max_slope"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return activation{}, fmt.Errorf("activation: %w", err)
	}
	a, err := parseActivation(json.RawMessage(fmt.Sprintf("%q", o.Type)))
	if err != nil {
		return a, err
	}
	set := func(dst *float32, v *float32) {
		if v != nil {
			*dst = *v
		}
	}
	switch a.kind {
	case actLeakyReLU:
		set(&a.slope, o.NegativeSlope)
	case actPReLU:
		if o.NegativeSlope != nil {
			a.slopes = []float32{*o.NegativeSlope}
		} else if o.NegativeSlopes != nil {
			if len(o.NegativeSlopes) == 0 {
				return a, fmt.Errorf("PReLU has no slopes")
			}
			a.slopes = o.NegativeSlopes
		}
	case actLeakyHardtanh:
		set(&a.lo, o.MinVal)
		set(&a.hi, o.MaxVal)
		set(&a.loSlope, o.MinSlope)
		set(&a.hiSlope, o.MaxSlope)
	}
	return a, nil
}

// check reports whether the activation fits rows channels.
func (a *activation) check(rows int) error {
	if a.kind == actPReLU && rows%len(a.slopes) != 0 {
		return fmt.Errorf("PReLU has %d slopes for %d channels", len(a.slopes), rows)
	}
	return nil
}

// apply runs the activation in place on channel ch's values.
func (a *activation) apply(ch int, x []float32) {
	switch a.kind {
	case actTanh, actFasttanh:
		for i, v := range x {
			x[i] = fastTanh(v)
		}
	case actHardtanh:
		for i, v := range x {
			x[i] = max(-1, min(1, v))
		}
	case actLeakyHardtanh:
		for i, v := range x {
			switch {
			case v < a.lo:
				x[i] = (v-a.lo)*a.loSlope + a.lo
			case v > a.hi:
				x[i] = (v-a.hi)*a.hiSlope + a.hi
			}
		}
	case actReLU:
		for i, v := range x {
			x[i] = max(0, v)
		}
	case actLeakyReLU:
		leakyReLU(x, a.slope)
	case actPReLU:
		leakyReLU(x, a.slopes[ch%len(a.slopes)])
	case actSigmoid:
		for i, v := range x {
			x[i] = sigmoid(v)
		}
	case actSiLU:
		for i, v := range x {
			x[i] = v * sigmoid(v)
		}
	case actHardswish:
		for i, v := range x {
			x[i] = v * max(0, min(6, v+3)) * (1.0 / 6)
		}
	case actSoftsign:
		for i, v := range x {
			x[i] = v / (1 + abs(v))
		}
	}
}

func leakyReLU(x []float32, slope float32) {
	for i, v := range x {
		if v <= 0 {
			x[i] = slope * v
		}
	}
}

// fastTanh is NAM's rational tanh approximation, which the NAM plugin
// uses for every "Tanh" activation. Its error is below 5e-4.
func fastTanh(x float32) float32 {
	ax := abs(x)
	x2 := x * x
	return x * (2.45550750702956 + 2.45550750702956*ax + (0.893229853513558+0.821226666969744*ax)*x2) /
		(2.44506634652299 + (2.44506634652299+x2)*abs(x+0.814642734961073*x*ax))
}

// fastSigmoid is NAM's sigmoid for LSTMs, built on fastTanh.
func fastSigmoid(x float32) float32 { return 0.5 * (fastTanh(0.5*x) + 1) }

func sigmoid(x float32) float32 { return float32(1 / (1 + math.Exp(-float64(x)))) }

func abs(x float32) float32 { return math.Float32frombits(math.Float32bits(x) &^ (1 << 31)) }

// matrix is a row-major block of rows×n values with a row stride.
type matrix struct {
	data   []float32
	stride int
}

// newBlock returns a rows×chunk matrix.
func newBlock(rows int) matrix { return matrix{make([]float32, rows*chunk), chunk} }

func (m matrix) row(i, from, n int) []float32 {
	start := i*m.stride + from
	return m.data[start : start+n : start+n]
}

// below returns m starting at row i.
func (m matrix) below(i int) matrix { return matrix{m.data[i*m.stride:], m.stride} }

// add adds rows of src to dst at columns [0,n).
func add(dst, src matrix, rows, n int) {
	for r := range rows {
		d, s := dst.row(r, 0, n), src.row(r, 0, n)
		for t := range d {
			d[t] += s[t]
		}
	}
}

// copyRows copies rows of src to dst at columns [0,n).
func copyRows(dst, src matrix, rows, n int) {
	for r := range rows {
		copy(dst.row(r, 0, n), src.row(r, 0, n))
	}
}

// weights is an out×in matrix, kept row-major and, for mac's 4×4 tiles,
// with each run of four rows interleaved column by column.
type weights struct {
	in, out int
	w       []float32
	packed  []float32
}

func newWeights(w []float32, in, out int) weights {
	m := weights{in: in, out: out, w: w, packed: make([]float32, out/4*4*in)}
	for o := 0; o+4 <= out; o += 4 {
		for i := 0; i < in; i++ {
			for j := range 4 {
				m.packed[o*in+i*4+j] = w[(o+j)*in+i]
			}
		}
	}
	return m
}

// mac adds W·x to dst rows [0,out) at columns [0,n), input i's n values
// starting at rows[i]; each must have n values there. Each output sums
// its products in input order, however it is tiled.
func mac(dst matrix, rows []*float32, w weights, n int) {
	in := w.in
	rows = rows[:in]
	o := 0
	for ; o+4 <= w.out; o += 4 {
		p := unsafe.Pointer(&w.packed[o*in : (o+4)*in][0])
		y0, y1, y2, y3 := dst.row(o, 0, n), dst.row(o+1, 0, n), dst.row(o+2, 0, n), dst.row(o+3, 0, n)
		t := 0
		for ; t+4 <= n; t += 4 {
			var a00, a01, a02, a03, a10, a11, a12, a13, a20, a21, a22, a23, a30, a31, a32, a33 float32
			for i, r := range rows {
				x := (*[4]float32)(unsafe.Add(unsafe.Pointer(r), 4*t))
				c := (*[4]float32)(unsafe.Add(p, 16*i))
				a00 += c[0] * x[0]
				a01 += c[0] * x[1]
				a02 += c[0] * x[2]
				a03 += c[0] * x[3]
				a10 += c[1] * x[0]
				a11 += c[1] * x[1]
				a12 += c[1] * x[2]
				a13 += c[1] * x[3]
				a20 += c[2] * x[0]
				a21 += c[2] * x[1]
				a22 += c[2] * x[2]
				a23 += c[2] * x[3]
				a30 += c[3] * x[0]
				a31 += c[3] * x[1]
				a32 += c[3] * x[2]
				a33 += c[3] * x[3]
			}
			y0[t], y0[t+1], y0[t+2], y0[t+3] = y0[t]+a00, y0[t+1]+a01, y0[t+2]+a02, y0[t+3]+a03
			y1[t], y1[t+1], y1[t+2], y1[t+3] = y1[t]+a10, y1[t+1]+a11, y1[t+2]+a12, y1[t+3]+a13
			y2[t], y2[t+1], y2[t+2], y2[t+3] = y2[t]+a20, y2[t+1]+a21, y2[t+2]+a22, y2[t+3]+a23
			y3[t], y3[t+1], y3[t+2], y3[t+3] = y3[t]+a30, y3[t+1]+a31, y3[t+2]+a32, y3[t+3]+a33
		}
		for ; t < n; t++ {
			var a0, a1, a2, a3 float32
			for i, r := range rows {
				x := *(*float32)(unsafe.Add(unsafe.Pointer(r), 4*t))
				c := (*[4]float32)(unsafe.Add(p, 16*i))
				a0 += c[0] * x
				a1 += c[1] * x
				a2 += c[2] * x
				a3 += c[3] * x
			}
			y0[t] += a0
			y1[t] += a1
			y2[t] += a2
			y3[t] += a3
		}
	}
	for ; o < w.out; o++ {
		y, row := dst.row(o, 0, n), w.w[o*in:(o+1)*in]
		for t := range y {
			var a float32
			for i, r := range rows {
				a += row[i] * *(*float32)(unsafe.Add(unsafe.Pointer(r), 4*t))
			}
			y[t] += a
		}
	}
}

// conv is NAM's Conv1D, and with one tap its Conv1x1: a causal dilated
// convolution from in to out channels, in groups that each connect
// in/groups inputs to out/groups outputs, with an optional bias.
type conv struct {
	in, out, kernel, dilation, groups int
	// w[g] is group g's weights, out/groups × kernel·in/groups: tap k's
	// inputs, oldest tap first.
	w    []weights
	rows [][]*float32 // per group: scratch for mac
	bias []float32    // nil without bias
	// hist holds the inputs of the last h frames before pos, then room
	// for new frames, when the convolution looks back (h > 0).
	hist   matrix
	h, pos int
}

// newConv reads a convolution's weights in NAM's order: for each group,
// output, input and tap, then the biases.
func newConv(r *reader, in, out, kernel, dilation, groups int, bias bool) (*conv, error) {
	switch {
	case in < 1 || out < 1 || kernel < 1 || dilation < 1 || groups < 1:
		return nil, fmt.Errorf("convolution of %d to %d channels, kernel %d, dilation %d, %d groups", in, out, kernel, dilation, groups)
	case in%groups != 0 || out%groups != 0:
		return nil, fmt.Errorf("%d groups do not divide %d to %d channels", groups, in, out)
	}
	c := &conv{in: in, out: out, kernel: kernel, dilation: dilation, groups: groups, h: dilation * (kernel - 1)}
	ipg, opg := in/groups, out/groups
	flat := r.take(in / groups * out * kernel)
	cols := kernel * ipg
	for g := range groups {
		w := make([]float32, opg*cols)
		for o := range opg {
			for j := range ipg {
				for k := range kernel {
					w[o*cols+k*ipg+j] = flat[((g*opg+o)*ipg+j)*kernel+k]
				}
			}
		}
		c.w = append(c.w, newWeights(w, cols, opg))
		c.rows = append(c.rows, make([]*float32, cols))
	}
	if bias {
		c.bias = r.take(out)
	}
	if c.h > 0 {
		c.hist = matrix{make([]float32, in*(c.h+span)), c.h + span}
	}
	c.reset()
	return c, nil
}

// apply sets dst rows [0,out) at columns [0,n) to the convolution of
// src's rows [0,in) at columns [0,n), continuing the inputs before them.
// dst must not overlap src.
func (c *conv) apply(dst, src matrix, n int) { c.run(dst, src, n, true) }

// accumulate is apply adding to dst instead of setting it.
func (c *conv) accumulate(dst, src matrix, n int) { c.run(dst, src, n, false) }

func (c *conv) run(dst, src matrix, n int, set bool) {
	x, from := c.stage(src, n)
	for o := range c.out {
		y := dst.row(o, 0, n)
		switch {
		case c.bias != nil && set:
			b := c.bias[o]
			for t := range y {
				y[t] = b
			}
		case c.bias != nil:
			b := c.bias[o]
			for t := range y {
				y[t] += b
			}
		case set:
			clear(y)
		}
	}
	opg := c.out / c.groups
	for g, w := range c.w {
		c.taps(c.rows[g], x, from, g, n)
		mac(dst.below(g*opg), c.rows[g], w, n)
	}
}

// stage adds src's n frames to the history and returns the matrix and
// column the convolution reads the current frames at.
func (c *conv) stage(src matrix, n int) (matrix, int) {
	if c.h == 0 {
		return src, 0
	}
	if c.pos+n > c.hist.stride {
		for ch := range c.in {
			copy(c.hist.row(ch, 0, c.h), c.hist.row(ch, c.pos-c.h, c.h))
		}
		c.pos = c.h
	}
	for ch := range c.in {
		copy(c.hist.row(ch, c.pos, n), src.row(ch, 0, n))
	}
	from := c.pos
	c.pos += n
	return c.hist, from
}

// taps points rows at group g's inputs for each tap, oldest first, as
// its weights order them.
func (c *conv) taps(rows []*float32, x matrix, from, g, n int) {
	ipg := c.in / c.groups
	for k := range c.kernel {
		at := from - (c.kernel-1-k)*c.dilation
		for j := range ipg {
			rows[k*ipg+j] = &x.row(g*ipg+j, at, n)[0]
		}
	}
}

// reset clears the past inputs.
func (c *conv) reset() {
	clear(c.hist.data)
	c.pos = c.h
}

// reader hands out a file's weights in order.
type reader struct {
	w    []float32
	next int
}

// take returns the next n weights; past the end, zeros, so that done can
// tell how many the configuration needs.
func (r *reader) take(n int) []float32 {
	start := r.next
	r.next += n
	if r.next > len(r.w) {
		return make([]float32, n)
	}
	return r.w[start:r.next:r.next]
}

// done reports a shortage or surplus of weights.
func (r *reader) done() error {
	if r.next != len(r.w) {
		return &weightsError{have: len(r.w), need: r.next}
	}
	return nil
}

// weightsError is a file whose weights do not fit its configuration.
type weightsError struct{ have, need int }

func (e *weightsError) Error() string {
	return fmt.Sprintf("the file has %d weights, the configuration needs %d", e.have, e.need)
}
