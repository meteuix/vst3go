package nam

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
)

// Gating modes of a WaveNet layer.
const (
	gateNone = iota
	gateGated
	gateBlended
)

// The FiLMs of a WaveNet layer, in weight order.
const (
	filmConvPre = iota
	filmConvPost
	filmMixinPre
	filmMixinPost
	filmActPre
	filmActPost
	filmLayer1x1Post
	filmHead1x1Post
	numFiLMs
)

var filmKeys = [numFiLMs]string{
	"conv_pre_film", "conv_post_film", "input_mixin_pre_film", "input_mixin_post_film",
	"activation_pre_film", "activation_post_film", "layer1x1_post_film", "head1x1_post_film",
}

type filmParams struct {
	active, shift bool
	groups        int
}

// layerArrayConfig is one layer array of a WaveNet configuration.
type layerArrayConfig struct {
	inputSize, conditionSize           int
	headSize, headKernel, headDilation int
	headBias                           bool
	channels, bottleneck               int
	kernels, dilations                 []int
	acts, gates                        []activation // per layer; gates are the secondary activations
	modes                              []int
	groupsInput, groupsMixin           int
	layer1x1                           bool
	layer1x1Groups                     int
	head1x1                            bool
	head1x1Out, head1x1Groups          int
	films                              [numFiLMs]filmParams
	// slimMethod is the layer's "slimmable" method, "" without one;
	// allowed are its channel counts when it is slimmable.
	slimmable  bool
	slimMethod string
	allowed    []int
}

// headOut is the channels each layer adds to the array's head.
func (a *layerArrayConfig) headOut() int {
	if a.head1x1 {
		return a.head1x1Out
	}
	return a.bottleneck
}

type headConfig struct {
	channels, out int
	kernels       []int
	act           activation
}

type waveNetConfig struct {
	inChannels int
	arrays     []layerArrayConfig
	head       *headConfig
	cond       *spec
	slimmable  bool
}

// object is a JSON object with its members undecoded.
type object map[string]json.RawMessage

func (o object) has(key string) bool {
	v, ok := o[key]
	return ok && string(v) != "null"
}

func (o object) get(key string, dst any) error {
	if !o.has(key) {
		return fmt.Errorf("no %q", key)
	}
	if err := json.Unmarshal(o[key], dst); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func (o object) opt(key string, dst any) error {
	if !o.has(key) {
		return nil
	}
	return o.get(key, dst)
}

func isArray(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '['
}

// parseActivations reads one activation for every layer, or n.
func parseActivations(raw json.RawMessage, n int) ([]activation, error) {
	if !isArray(raw) {
		a, err := parseActivation(raw)
		if err != nil {
			return nil, err
		}
		return slices.Repeat([]activation{a}, n), nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	if len(list) != n {
		return nil, fmt.Errorf("%d activations for %d layers", len(list), n)
	}
	acts := make([]activation, n)
	for i, r := range list {
		var err error
		if acts[i], err = parseActivation(r); err != nil {
			return nil, err
		}
	}
	return acts, nil
}

func parseGatingMode(s string) (int, error) {
	switch s {
	case "none":
		return gateNone, nil
	case "gated":
		return gateGated, nil
	case "blended":
		return gateBlended, nil
	}
	return 0, fmt.Errorf("gating mode %q is not supported", s)
}

var sigmoidActivation = activation{kind: actSigmoid}

func parseLayerArray(o object) (layerArrayConfig, error) {
	a := layerArrayConfig{groupsInput: 1, groupsMixin: 1, layer1x1: true, layer1x1Groups: 1, headKernel: 1, headDilation: 1}
	if err := errors.Join(
		o.get("channels", &a.channels),
		o.get("input_size", &a.inputSize),
		o.get("condition_size", &a.conditionSize),
		o.get("dilations", &a.dilations),
		o.opt("groups_input", &a.groupsInput),
		o.opt("groups_input_mixin", &a.groupsMixin),
	); err != nil {
		return a, err
	}
	a.bottleneck = a.channels
	if err := o.opt("bottleneck", &a.bottleneck); err != nil {
		return a, err
	}
	if o.has("layer1x1") {
		var l object
		if err := o.get("layer1x1", &l); err != nil {
			return a, err
		}
		if err := errors.Join(l.get("active", &a.layer1x1), l.get("groups", &a.layer1x1Groups)); err != nil {
			return a, fmt.Errorf("layer1x1: %w", err)
		}
	}
	switch {
	case o.has("head"):
		var h object
		if err := o.get("head", &h); err != nil {
			return a, err
		}
		if err := errors.Join(h.get("out_channels", &a.headSize), h.get("kernel_size", &a.headKernel),
			h.get("bias", &a.headBias), h.opt("head_dilation", &a.headDilation)); err != nil {
			return a, fmt.Errorf("head: %w", err)
		}
	case o.has("head_size"):
		if err := errors.Join(o.get("head_size", &a.headSize), o.get("head_bias", &a.headBias)); err != nil {
			return a, err
		}
	default:
		return a, errors.New("no head or head_size")
	}
	n := len(a.dilations)
	switch {
	case o.has("kernel_size") && o.has("kernel_sizes"):
		return a, errors.New("both kernel_size and kernel_sizes")
	case o.has("kernel_sizes"):
		if err := o.get("kernel_sizes", &a.kernels); err != nil {
			return a, err
		}
		if len(a.kernels) != n {
			return a, fmt.Errorf("%d kernel sizes for %d layers", len(a.kernels), n)
		}
	case o.has("kernel_size"):
		var k int
		if err := o.get("kernel_size", &k); err != nil {
			return a, err
		}
		a.kernels = slices.Repeat([]int{k}, n)
	default:
		return a, errors.New("no kernel_size or kernel_sizes")
	}
	if !o.has("activation") {
		return a, errors.New(`no "activation"`)
	}
	var err error
	if a.acts, err = parseActivations(o["activation"], n); err != nil {
		return a, fmt.Errorf("activation: %w", err)
	}
	if err := parseGating(o, &a); err != nil {
		return a, err
	}
	a.head1x1Out = a.channels
	a.head1x1Groups = 1
	if o.has("head1x1") {
		var h object
		if err := o.get("head1x1", &h); err != nil {
			return a, err
		}
		if err := errors.Join(h.get("active", &a.head1x1), h.get("out_channels", &a.head1x1Out),
			h.get("groups", &a.head1x1Groups)); err != nil {
			return a, fmt.Errorf("head1x1: %w", err)
		}
	}
	for i, key := range filmKeys {
		if !o.has(key) || string(bytes.TrimSpace(o[key])) == "false" {
			continue
		}
		var f object
		if err := o.get(key, &f); err != nil {
			return a, err
		}
		p := filmParams{active: true, shift: true, groups: 1}
		if err := errors.Join(f.opt("active", &p.active), f.opt("shift", &p.shift), f.opt("groups", &p.groups)); err != nil {
			return a, fmt.Errorf("%s: %w", key, err)
		}
		a.films[i] = p
	}
	if a.films[filmLayer1x1Post].active && !a.layer1x1 {
		return a, errors.New("layer1x1_post_film without layer1x1")
	}
	if a.films[filmHead1x1Post].active && !a.head1x1 {
		return a, errors.New("head1x1_post_film without head1x1")
	}
	if o.has("slimmable") {
		var s object
		if err := o.get("slimmable", &s); err == nil {
			a.slimmable = true
			if err := s.opt("method", &a.slimMethod); err != nil {
				return a, fmt.Errorf("slimmable: %w", err)
			}
			var kw object
			if err := s.opt("kwargs", &kw); err != nil {
				return a, fmt.Errorf("slimmable: %w", err)
			}
			if kw.has("allowed_channels") {
				if err := kw.get("allowed_channels", &a.allowed); err != nil {
					return a, fmt.Errorf("slimmable: %w", err)
				}
			} else {
				for c := 1; c <= a.channels; c++ {
					a.allowed = append(a.allowed, c)
				}
			}
		}
	}
	return a, nil
}

// parseGating reads a layer array's gating modes and secondary
// activations, with NAM's defaults: no gating, gates through a sigmoid.
func parseGating(o object, a *layerArrayConfig) error {
	n := len(a.dilations)
	a.modes = make([]int, n)
	a.gates = slices.Repeat([]activation{sigmoidActivation}, n)
	switch {
	case o.has("gating_mode") && isArray(o["gating_mode"]):
		var names []string
		if err := o.get("gating_mode", &names); err != nil {
			return err
		}
		if len(names) != n {
			return fmt.Errorf("%d gating modes for %d layers", len(names), n)
		}
		for i, s := range names {
			var err error
			if a.modes[i], err = parseGatingMode(s); err != nil {
				return err
			}
		}
		if !o.has("secondary_activation") {
			break
		}
		// One for every layer or one per layer; ungated layers' are unused.
		raw := o["secondary_activation"]
		var list []json.RawMessage
		if !isArray(raw) {
			list = slices.Repeat([]json.RawMessage{raw}, n)
		} else {
			if err := json.Unmarshal(raw, &list); err != nil {
				return fmt.Errorf("secondary_activation: %w", err)
			}
			if len(list) != n {
				return fmt.Errorf("%d secondary activations for %d layers", len(list), n)
			}
		}
		for i, mode := range a.modes {
			if mode == gateNone {
				continue
			}
			g, err := parseActivation(list[i])
			if err != nil {
				return fmt.Errorf("secondary_activation: %w", err)
			}
			a.gates[i] = g
		}
	case o.has("gating_mode"):
		var name string
		if err := o.get("gating_mode", &name); err != nil {
			return err
		}
		mode, err := parseGatingMode(name)
		if err != nil {
			return err
		}
		for i := range a.modes {
			a.modes[i] = mode
		}
		if mode != gateNone && o.has("secondary_activation") {
			g, err := parseActivation(o["secondary_activation"])
			if err != nil {
				return fmt.Errorf("secondary_activation: %w", err)
			}
			a.gates = slices.Repeat([]activation{g}, n)
		}
	case o.has("gated"):
		var gated bool
		if err := o.get("gated", &gated); err != nil {
			return err
		}
		if gated {
			for i := range a.modes {
				a.modes[i] = gateGated
			}
		}
	}
	return nil
}

func parseWaveNet(s spec) (*waveNetConfig, error) {
	var o object
	if err := json.Unmarshal(s.config, &o); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c := &waveNetConfig{inChannels: 1}
	if err := o.opt("in_channels", &c.inChannels); err != nil {
		return nil, err
	}
	if o.has("condition_dsp") {
		cs, err := parseSpec(o["condition_dsp"])
		if err != nil {
			return nil, fmt.Errorf("condition_dsp: %w", err)
		}
		c.cond = &cs
	}
	var layers []object
	if err := o.get("layers", &layers); err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		return nil, errors.New("no layer arrays")
	}
	unnamed := false
	for i, l := range layers {
		a, err := parseLayerArray(l)
		if err != nil {
			return nil, fmt.Errorf("layer array %d: %w", i, err)
		}
		switch {
		case a.slimMethod == "slice_channels_uniform":
			c.slimmable = true
		case a.slimMethod != "":
			return nil, fmt.Errorf("layer array %d: slimmable method %q is not supported", i, a.slimMethod)
		case a.slimmable:
			unnamed = true
			a.slimmable, a.allowed = false, nil
		}
		c.arrays = append(c.arrays, a)
	}
	if c.slimmable && unnamed {
		return nil, errors.New(`a slimmable layer array has no method`)
	}
	if o.has("head") {
		var h object
		if err := o.get("head", &h); err != nil {
			return nil, err
		}
		hc := &headConfig{}
		if err := errors.Join(h.get("channels", &hc.channels), h.get("out_channels", &hc.out),
			h.get("kernel_sizes", &hc.kernels)); err != nil {
			return nil, fmt.Errorf("head: %w", err)
		}
		if !h.has("activation") {
			return nil, errors.New(`head: no "activation"`)
		}
		var err error
		if hc.act, err = parseActivation(h["activation"]); err != nil {
			return nil, fmt.Errorf("head: %w", err)
		}
		if h.has("in_channels") {
			var in int
			if err := h.get("in_channels", &in); err != nil {
				return nil, fmt.Errorf("head: %w", err)
			}
			if last := c.arrays[len(c.arrays)-1].headSize; in != last {
				return nil, fmt.Errorf("head: %d input channels, the last head has %d", in, last)
			}
		}
		if len(hc.kernels) == 0 {
			return nil, errors.New("head: no kernel sizes")
		}
		c.head = hc
	}
	return c, nil
}

func newWaveNet(s spec, size float64) (network, error) {
	c, err := parseWaveNet(s)
	if err != nil {
		return nil, err
	}
	if !c.slimmable {
		return buildWaveNet(c, s, nil)
	}
	if c.head != nil {
		return nil, errors.New("a slimmable WaveNet with a post-stack head is not supported")
	}
	for i, a := range c.arrays {
		if !a.slimmable {
			continue
		}
		if len(a.allowed) == 0 || a.allowed[0] < 1 {
			return nil, fmt.Errorf("layer array %d: allowed channels %v", i, a.allowed)
		}
		for j := 1; j < len(a.allowed); j++ {
			if a.allowed[j] <= a.allowed[j-1] {
				return nil, fmt.Errorf("layer array %d: allowed channels %v are not ascending", i, a.allowed)
			}
		}
		if a.allowed[len(a.allowed)-1] != a.channels {
			return nil, fmt.Errorf("layer array %d: allowed channels %v do not end at its %d channels", i, a.allowed, a.channels)
		}
	}
	// NAM picks allowed[min(floor(size·len), len-1)] for every array.
	target := make([]int, len(c.arrays))
	full := true
	for i, a := range c.arrays {
		target[i] = a.channels
		if a.slimmable {
			target[i] = a.allowed[min(int(math.Floor(size*float64(len(a.allowed)))), len(a.allowed)-1)]
		}
		full = full && target[i] == a.channels
	}
	if full {
		target = nil
	}
	return buildWaveNet(c, s, target)
}

// waveNetSizes returns where a slimmable WaveNet changes size and checks
// that it builds at each.
func waveNetSizes(s spec) ([]float64, error) {
	c, err := parseWaveNet(s)
	if err != nil || !c.slimmable {
		return nil, err
	}
	var sizes []float64
	for _, a := range c.arrays {
		for i := 1; i < len(a.allowed); i++ {
			sizes = append(sizes, float64(i)/float64(len(a.allowed)))
		}
	}
	slices.Sort(sizes)
	sizes = slices.Compact(sizes)
	for _, size := range sizes {
		if _, err := newWaveNet(s, size); err != nil {
			return nil, fmt.Errorf("WaveNet at size %g: %w", size, err)
		}
	}
	return sizes, nil
}

// loader reads convolutions stored at full size and keeps the first
// inputs and outputs of each, as NAM slims a WaveNet.
type loader struct{ r *reader }

func (ld loader) conv(fullIn, fullOut, in, out, kernel, dilation, groups int, bias bool) (*conv, error) {
	if fullIn == in && fullOut == out {
		return newConv(ld.r, in, out, kernel, dilation, groups, bias)
	}
	if groups != 1 {
		return nil, fmt.Errorf("slimming a convolution of %d groups is not supported", groups)
	}
	if in < 1 || out < 1 || in > fullIn || out > fullOut {
		return nil, fmt.Errorf("slimming %d to %d channels down to %d to %d", fullIn, fullOut, in, out)
	}
	full := ld.r.take(fullIn * fullOut * kernel)
	w := make([]float32, 0, in*out*kernel+out)
	for o := range out {
		for i := range in {
			at := (o*fullIn + i) * kernel
			w = append(w, full[at:at+kernel]...)
		}
	}
	if bias {
		w = append(w, ld.r.take(fullOut)[:out]...)
	}
	return newConv(&reader{w: w}, in, out, kernel, dilation, groups, bias)
}

func (ld loader) film(p filmParams, cond, fullDim, dim int) (*film, error) {
	if !p.active {
		return nil, nil
	}
	k := 1
	if p.shift {
		k = 2
	}
	c, err := ld.conv(cond, k*fullDim, cond, k*dim, 1, 1, p.groups, true)
	if err != nil {
		return nil, err
	}
	return &film{conv: c, dim: dim, shift: p.shift, ss: newBlock(k * dim)}, nil
}

// film is NAM's FiLM: it scales (and shifts) its input by a 1x1
// convolution of the condition.
type film struct {
	conv  *conv
	dim   int
	shift bool
	ss    matrix
}

// apply modulates x's rows [0,dim) in place.
func (f *film) apply(x, cond matrix, n int) {
	f.conv.apply(f.ss, cond, n)
	for r := range f.dim {
		y, s := x.row(r, 0, n), f.ss.row(r, 0, n)
		if f.shift {
			b := f.ss.row(f.dim+r, 0, n)
			for t := range y {
				y[t] = y[t]*s[t] + b[t]
			}
		} else {
			for t := range y {
				y[t] *= s[t]
			}
		}
	}
}

// waveNet is NAM's WaveNet: layer arrays of dilated convolutions, each
// adding into a head that the next array continues, conditioned on the
// input or a condition model's output, then an optional post-stack head.
type waveNet struct {
	in, out int
	cond    network // nil: the input conditions the layers
	condOut matrix
	arrays  []*wnArray
	head    []headStage
	scale   float32
	scaled  matrix
	warm    int
}

type wnArray struct {
	rechannel     *conv
	layers        []*wnLayer
	headRechannel *conv
	a, b, out     matrix // the layers' inputs and outputs; out is the last's
	headIn        matrix
	headOut       matrix
	headRows      int
}

type wnLayer struct {
	channels, bottleneck, zc, condRows, headRows int
	mode                                         int
	conv, mixin, layer1x1, head1x1               *conv
	act, gate                                    activation
	films                                        [numFiLMs]*film
	pre, mixPre, z, mix, blend, l1, head         matrix
	// fused is the convolution and the input mixin as one matrix, tap
	// inputs then the condition, when nothing comes between them.
	fused     weights
	fusedRows []*float32
}

type headStage struct {
	act  activation
	conv *conv
	out  matrix
}

// buildWaveNet builds c from s's weights, with array i at target[i]
// channels when target is not nil.
func buildWaveNet(c *waveNetConfig, s spec, target []int) (*waveNet, error) {
	w := &waveNet{in: c.inChannels, warm: 1}
	if w.in < 1 {
		return nil, fmt.Errorf("%d input channels", w.in)
	}
	condRows := w.in
	if c.cond != nil {
		cond, rate, err := build(*c.cond, 1)
		if err != nil {
			return nil, fmt.Errorf("condition_dsp: %w", err)
		}
		if rate != s.rate {
			return nil, fmt.Errorf("condition_dsp runs at %v Hz, the WaveNet at %v", rate, s.rate)
		}
		in, out := cond.channels()
		if in != w.in {
			return nil, fmt.Errorf("condition_dsp has %d inputs, the WaveNet %d", in, w.in)
		}
		w.cond, condRows = cond, out
		w.condOut = newBlock(out)
		w.warm = cond.prewarm()
	}
	ld := loader{&reader{w: s.weights}}
	prevChannels, prevHead := w.in, 0
	for i := range c.arrays {
		a := &c.arrays[i]
		// The array's dimensions, full and as built.
		ch, input, headSize := a.channels, a.inputSize, a.headSize
		if target != nil {
			ch = target[i]
			if i > 0 {
				input = target[i-1]
			}
			if i+1 < len(c.arrays) {
				headSize = target[i+1]
			}
		}
		bn := ch
		if a.layer1x1 {
			bn = max(1, a.bottleneck*ch/a.channels)
		}
		headOut := bn
		if a.head1x1 {
			headOut = a.head1x1Out
		}
		switch {
		case ch < 1 || bn < 1 || headSize < 1 || len(a.dilations) == 0:
			return nil, fmt.Errorf("layer array %d has %d channels, bottleneck %d, head size %d and %d layers", i, ch, bn, headSize, len(a.dilations))
		case !a.layer1x1 && a.bottleneck != a.channels:
			return nil, fmt.Errorf("layer array %d: bottleneck %d differs from %d channels without layer1x1", i, a.bottleneck, a.channels)
		case input != prevChannels:
			return nil, fmt.Errorf("layer array %d has input size %d, want %d", i, input, prevChannels)
		case a.conditionSize != condRows:
			return nil, fmt.Errorf("layer array %d has condition size %d, want %d", i, a.conditionSize, condRows)
		case i > 0 && ch != prevHead:
			return nil, fmt.Errorf("layer array %d has %d channels, the head before it %d", i, ch, prevHead)
		case i > 0 && headOut != prevHead:
			return nil, fmt.Errorf("layer array %d adds %d head channels to the %d before it", i, headOut, prevHead)
		}
		arr, err := ld.array(a, ch, input, headSize, bn, headOut)
		if err != nil {
			return nil, fmt.Errorf("layer array %d: %w", i, err)
		}
		w.arrays = append(w.arrays, arr)
		for j, d := range a.dilations {
			w.warm += d * (a.kernels[j] - 1)
		}
		w.warm += a.headDilation * (a.headKernel - 1)
		prevChannels, prevHead = ch, headSize
	}
	w.out = prevHead
	if h := c.head; h != nil {
		in := prevHead
		w.scaled = newBlock(in)
		for i, k := range h.kernels {
			out := h.channels
			if i+1 == len(h.kernels) {
				out = h.out
			}
			if err := h.act.check(in); err != nil {
				return nil, fmt.Errorf("head: %w", err)
			}
			cv, err := newConv(ld.r, in, out, k, 1, 1, true)
			if err != nil {
				return nil, fmt.Errorf("head: %w", err)
			}
			w.head = append(w.head, headStage{act: h.act, conv: cv, out: newBlock(out)})
			w.warm += k - 1
			in = out
		}
		w.out = in
	}
	w.scale = ld.r.take(1)[0]
	if err := ld.r.done(); err != nil {
		return nil, err
	}
	return w, nil
}

func (ld loader) array(a *layerArrayConfig, ch, input, headSize, bn, headOut int) (*wnArray, error) {
	fullBn := a.bottleneck
	arr := &wnArray{a: newBlock(ch), b: newBlock(ch), headIn: newBlock(headOut), headOut: newBlock(headSize), headRows: headOut}
	var err error
	if arr.rechannel, err = ld.conv(a.inputSize, a.channels, input, ch, 1, 1, 1, false); err != nil {
		return nil, fmt.Errorf("rechannel: %w", err)
	}
	for j, d := range a.dilations {
		mode := a.modes[j]
		zc, fullZc := bn, fullBn
		if mode != gateNone {
			zc, fullZc = 2*bn, 2*fullBn
		}
		l := &wnLayer{channels: ch, bottleneck: bn, zc: zc, condRows: a.conditionSize, headRows: headOut,
			mode: mode, act: a.acts[j], gate: a.gates[j], z: newBlock(zc)}
		if err := errors.Join(l.act.check(bn), l.gate.check(bn)); err != nil {
			return nil, fmt.Errorf("layer %d: %w", j, err)
		}
		if mode == gateBlended {
			l.blend = newBlock(bn)
		}
		if l.conv, err = ld.conv(a.channels, fullZc, ch, zc, a.kernels[j], d, a.groupsInput, true); err != nil {
			return nil, fmt.Errorf("layer %d: conv: %w", j, err)
		}
		if l.mixin, err = ld.conv(a.conditionSize, fullZc, a.conditionSize, zc, 1, 1, a.groupsMixin, false); err != nil {
			return nil, fmt.Errorf("layer %d: input mixin: %w", j, err)
		}
		if a.layer1x1 {
			if l.layer1x1, err = ld.conv(fullBn, a.channels, bn, ch, 1, 1, a.layer1x1Groups, true); err != nil {
				return nil, fmt.Errorf("layer %d: layer1x1: %w", j, err)
			}
		}
		if a.head1x1 {
			if l.head1x1, err = ld.conv(fullBn, a.head1x1Out, bn, a.head1x1Out, 1, 1, a.head1x1Groups, true); err != nil {
				return nil, fmt.Errorf("layer %d: head1x1: %w", j, err)
			}
		}
		dims := [numFiLMs][2]int{
			filmConvPre:      {a.channels, ch},
			filmConvPost:     {fullZc, zc},
			filmMixinPre:     {a.conditionSize, a.conditionSize},
			filmMixinPost:    {fullZc, zc},
			filmActPre:       {fullZc, zc},
			filmActPost:      {fullBn, bn},
			filmLayer1x1Post: {a.channels, ch},
			filmHead1x1Post:  {a.head1x1Out, a.head1x1Out},
		}
		for k, p := range a.films {
			if l.films[k], err = ld.film(p, a.conditionSize, dims[k][0], dims[k][1]); err != nil {
				return nil, fmt.Errorf("layer %d: %s: %w", j, filmKeys[k], err)
			}
		}
		if l.films[filmConvPre] != nil {
			l.pre = newBlock(ch)
		}
		if a.groupsInput == 1 && a.groupsMixin == 1 &&
			l.films[filmConvPost] == nil && l.films[filmMixinPre] == nil && l.films[filmMixinPost] == nil {
			kc, cond := l.conv.w[0].in, a.conditionSize
			cols := kc + cond
			w := make([]float32, zc*cols)
			for o := range zc {
				copy(w[o*cols:o*cols+kc], l.conv.w[0].w[o*kc:(o+1)*kc])
				copy(w[o*cols+kc:(o+1)*cols], l.mixin.w[0].w[o*cond:(o+1)*cond])
			}
			l.fused, l.fusedRows = newWeights(w, cols, zc), make([]*float32, cols)
		} else {
			l.mix = newBlock(zc)
		}
		if l.films[filmLayer1x1Post] != nil {
			l.l1 = newBlock(ch)
		}
		if l.films[filmHead1x1Post] != nil {
			l.head = newBlock(a.head1x1Out)
		}
		if l.films[filmMixinPre] != nil {
			l.mixPre = newBlock(a.conditionSize)
		}
		arr.layers = append(arr.layers, l)
	}
	if arr.headRechannel, err = ld.conv(a.headOut(), a.headSize, headOut, headSize, a.headKernel, a.headDilation, 1, a.headBias); err != nil {
		return nil, fmt.Errorf("head rechannel: %w", err)
	}
	return arr, nil
}

func (w *waveNet) channels() (int, int) { return w.in, w.out }

func (w *waveNet) process(in, out matrix, n int) {
	cond := in
	if w.cond != nil {
		w.cond.process(in, w.condOut, n)
		cond = w.condOut
	}
	for i, a := range w.arrays {
		if i == 0 {
			for r := range a.headRows {
				clear(a.headIn.row(r, 0, n))
			}
			a.process(in, cond, n)
		} else {
			prev := w.arrays[i-1]
			copyRows(a.headIn, prev.headOut, a.headRows, n)
			a.process(prev.out, cond, n)
		}
	}
	final := w.arrays[len(w.arrays)-1].headOut
	if w.head == nil {
		for r := range w.out {
			y, x := out.row(r, 0, n), final.row(r, 0, n)
			for t := range y {
				y[t] = w.scale * x[t]
			}
		}
		return
	}
	x := w.scaled
	for r := range w.head[0].conv.in {
		y, f := x.row(r, 0, n), final.row(r, 0, n)
		for t := range y {
			y[t] = w.scale * f[t]
		}
	}
	for i, st := range w.head {
		for r := range st.conv.in {
			st.act.apply(r, x.row(r, 0, n))
		}
		dst := st.out
		if i+1 == len(w.head) {
			dst = out
		}
		st.conv.apply(dst, x, n)
		x = st.out
	}
}

func (a *wnArray) process(in, cond matrix, n int) {
	a.rechannel.apply(a.a, in, n)
	x, y := a.a, a.b
	for _, l := range a.layers {
		l.process(x, cond, y, a.headIn, n)
		x, y = y, x
	}
	a.out = x
	a.headRechannel.apply(a.headOut, a.headIn, n)
}

// process runs the layer on in, adding its head output to headAcc and
// writing its residual output to out.
func (l *wnLayer) process(in, cond, out, headAcc matrix, n int) {
	src := in
	if f := l.films[filmConvPre]; f != nil {
		copyRows(l.pre, in, l.channels, n)
		f.apply(l.pre, cond, n)
		src = l.pre
	}
	if l.fusedRows != nil {
		x, from := l.conv.stage(src, n)
		l.conv.taps(l.fusedRows, x, from, 0, n)
		kc := len(l.fusedRows) - l.condRows
		for r := range l.condRows {
			l.fusedRows[kc+r] = &cond.row(r, 0, n)[0]
		}
		for o, b := range l.conv.bias {
			y := l.z.row(o, 0, n)
			for t := range y {
				y[t] = b
			}
		}
		mac(l.z, l.fusedRows, l.fused, n)
	} else {
		l.conv.apply(l.z, src, n)
		if f := l.films[filmConvPost]; f != nil {
			f.apply(l.z, cond, n)
		}
		msrc := cond
		if f := l.films[filmMixinPre]; f != nil {
			copyRows(l.mixPre, cond, l.condRows, n)
			f.apply(l.mixPre, cond, n)
			msrc = l.mixPre
		}
		l.mixin.apply(l.mix, msrc, n)
		if f := l.films[filmMixinPost]; f != nil {
			f.apply(l.mix, cond, n)
		}
		add(l.z, l.mix, l.zc, n)
	}
	if f := l.films[filmActPre]; f != nil {
		f.apply(l.z, cond, n)
	}
	b := l.bottleneck
	for r := range b {
		x := l.z.row(r, 0, n)
		switch l.mode {
		case gateNone:
			l.act.apply(r, x)
		case gateGated:
			g := l.z.row(b+r, 0, n)
			l.act.apply(r, x)
			l.gate.apply(r, g)
			for t := range x {
				x[t] *= g[t]
			}
		case gateBlended:
			g, p := l.z.row(b+r, 0, n), l.blend.row(r, 0, n)
			copy(p, x)
			l.act.apply(r, x)
			l.gate.apply(r, g)
			for t := range x {
				x[t] = g[t]*x[t] + (1-g[t])*p[t]
			}
		}
	}
	if f := l.films[filmActPost]; f != nil {
		f.apply(l.z, cond, n)
	}
	if f := l.films[filmHead1x1Post]; f != nil {
		l.head1x1.apply(l.head, l.z, n)
		f.apply(l.head, cond, n)
		add(headAcc, l.head, l.headRows, n)
	} else if l.head1x1 != nil {
		l.head1x1.accumulate(headAcc, l.z, n)
	} else {
		add(headAcc, l.z, b, n)
	}
	// The residual: the input plus the layer 1x1's output.
	if f := l.films[filmLayer1x1Post]; f != nil {
		l.layer1x1.apply(l.l1, l.z, n)
		f.apply(l.l1, cond, n)
		for r := range l.channels {
			y, x, v := out.row(r, 0, n), in.row(r, 0, n), l.l1.row(r, 0, n)
			for t := range y {
				y[t] = x[t] + v[t]
			}
		}
		return
	}
	copyRows(out, in, l.channels, n)
	if l.layer1x1 != nil {
		l.layer1x1.accumulate(out, l.z, n)
	}
}

func (w *waveNet) reset() {
	if w.cond != nil {
		w.cond.reset()
	}
	for _, a := range w.arrays {
		a.rechannel.reset()
		for _, l := range a.layers {
			l.conv.reset()
		}
		a.headRechannel.reset()
	}
	for _, st := range w.head {
		st.conv.reset()
	}
}

func (w *waveNet) prewarm() int { return w.warm }
