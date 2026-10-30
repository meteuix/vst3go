package nam

import (
	"fmt"
	"math"
	"unsafe"
)

// activation applies a nonlinearity in place.
type activation func(x []float32)

func activationNamed(name string) (activation, error) {
	switch name {
	case "Tanh", "Fasttanh":
		return tanhAll, nil
	case "Hardtanh":
		return hardTanhAll, nil
	case "ReLU":
		return reluAll, nil
	case "Sigmoid":
		return sigmoidAll, nil
	}
	return nil, fmt.Errorf("activation %q is not supported", name)
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

func tanhAll(x []float32) {
	for i, v := range x {
		x[i] = fastTanh(v)
	}
}

func hardTanhAll(x []float32) {
	for i, v := range x {
		x[i] = max(-1, min(1, v))
	}
}

func reluAll(x []float32) {
	for i, v := range x {
		x[i] = max(0, v)
	}
}

func sigmoidAll(x []float32) {
	for i, v := range x {
		x[i] = sigmoid(v)
	}
}

// matrix is a row-major block of rows×n values with a row stride.
type matrix struct {
	data   []float32
	stride int
}

func (m matrix) row(i, from, n int) []float32 {
	start := i*m.stride + from
	return m.data[start : start+n : start+n]
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

// dense is a weight matrix with an optional bias.
type dense struct {
	weights
	bias []float32  // nil without bias
	rows []*float32 // scratch for apply
}

func (d *dense) load(r *reader, in, out int, bias bool) {
	d.weights = newWeights(r.take(in*out), in, out)
	if bias {
		d.bias = r.take(out)
	}
	d.rows = make([]*float32, in)
}

// apply sets dst rows [0,out) at columns [0,n) to W·src columns
// [from,from+n) plus the bias.
func (d *dense) apply(dst, src matrix, from, n int) {
	for o := 0; o < d.out; o++ {
		y := dst.row(o, 0, n)
		b := float32(0)
		if d.bias != nil {
			b = d.bias[o]
		}
		for t := range y {
			y[t] = b
		}
	}
	for i := range d.rows {
		d.rows[i] = &src.row(i, from, n)[0]
	}
	mac(dst, d.rows, d.weights, n)
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

// reader hands out a file's weights in order.
type reader struct {
	w    []float32
	next int
	err  error
}

func (r *reader) take(n int) []float32 {
	if r.next+n > len(r.w) {
		r.err = fmt.Errorf("the file has %d weights, the configuration needs more", len(r.w))
		r.next = len(r.w)
		return make([]float32, n)
	}
	s := r.w[r.next : r.next+n : r.next+n]
	r.next += n
	return s
}

// done reports a shortage or surplus of weights.
func (r *reader) done() error {
	if r.err == nil && r.next != len(r.w) {
		return fmt.Errorf("the file has %d weights, the configuration uses %d", len(r.w), r.next)
	}
	return r.err
}
