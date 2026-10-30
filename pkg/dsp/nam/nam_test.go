package nam

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readF32(t testing.TB, path string) []float32 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v := make([]float32, len(data)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
	}
	return v
}

// coreCases are models with NeuralAmpModelerCore's output for
// testdata/input.f32 in blocks of 64 frames, with fast tanh as the NAM
// plugin runs it. wavenet.f32 and lstm.f32 are from NAM core v0.1.0; the
// rest are from its main branch (0.7 files, with NAM_ENABLE_A2_FAST), made
// by testdata/ref/render.cpp:
//
//	render model.nam testdata/input.f32 out.f32 64 [size]
//
// The models are NAM core's example models (MIT) and the gen_ models of
// TestGenerateModels, which cover the rest of the format: ConvNet,
// Sequential, multichannel Linear and LSTM, every activation, gated and
// blended layers, bottlenecks, grouped convolutions, layer and head 1x1s,
// all eight FiLMs, condition models, head kernels and post-stack heads,
// and slimming.
var coreCases = []struct {
	model, ref string
	size       float64 // -1: New
}{
	{"wavenet.nam", "wavenet.f32", -1},
	{"lstm.nam", "lstm.f32", -1},
	{"ref/A2.nam", "ref/A2.f32", -1},
	{"ref/A2.nam", "ref/A2@0.3.f32", 0.3},
	{"ref/slimmable_container.nam", "ref/slimmable_container.f32", -1},
	{"ref/slimmable_container.nam", "ref/slimmable_container@0.2.f32", 0.2},
	{"ref/slimmable_container.nam", "ref/slimmable_container@0.5.f32", 0.5},
	{"ref/slimmable_wavenet.nam", "ref/slimmable_wavenet.f32", -1},
	{"ref/slimmable_wavenet.nam", "ref/slimmable_wavenet@0.1.f32", 0.1},
	{"ref/slimmable_wavenet.nam", "ref/slimmable_wavenet@0.5.f32", 0.5},
	{"ref/wavenet_a2_feature_test.nam", "ref/wavenet_a2_feature_test.f32", -1},
	{"ref/wavenet_condition_dsp.nam", "ref/wavenet_condition_dsp.f32", -1},
	{"ref/gen_convnet.nam", "ref/gen_convnet.f32", -1},
	{"ref/gen_convnet_batchnorm.nam", "ref/gen_convnet_batchnorm.f32", -1},
	{"ref/gen_wavenet_features.nam", "ref/gen_wavenet_features.f32", -1},
	{"ref/gen_wavenet_activations.nam", "ref/gen_wavenet_activations.f32", -1},
	{"ref/gen_sequential.nam", "ref/gen_sequential.f32", -1},
	{"ref/gen_sequential_lstm.nam", "ref/gen_sequential_lstm.f32", -1},
	{"ref/gen_slimmable.nam", "ref/gen_slimmable.f32", -1},
	{"ref/gen_slimmable.nam", "ref/gen_slimmable@0.1.f32", 0.1},
	{"ref/gen_slimmable.nam", "ref/gen_slimmable@0.4.f32", 0.4},
	{"ref/gen_slimmable.nam", "ref/gen_slimmable@0.6.f32", 0.6},
}

func newModel(t testing.TB, f *File, size float64) *Model {
	t.Helper()
	var m *Model
	var err error
	if size < 0 {
		m, err = f.New()
	} else {
		m, err = f.NewSized(size)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMatchesNAMCore(t *testing.T) {
	in := readF32(t, "testdata/input.f32")
	for _, c := range coreCases {
		t.Run(c.ref, func(t *testing.T) {
			f, err := Load("testdata/" + c.model)
			if err != nil {
				t.Fatal(err)
			}
			if f.SampleRate != 48000 {
				t.Fatalf("sample rate %v", f.SampleRate)
			}
			m := newModel(t, f, c.size)
			want := readF32(t, "testdata/"+c.ref)
			got := make([]float32, len(in))
			for i := 0; i < len(in); i += 64 {
				m.Process(in[i:min(i+64, len(in))], got[i:min(i+64, len(in))])
			}
			worst, peak := 0.0, 0.0
			for i := range want {
				worst = max(worst, math.Abs(float64(got[i]-want[i])))
				peak = max(peak, math.Abs(float64(want[i])))
			}
			if peak < 1e-3 {
				t.Fatalf("the reference peaks at %g", peak)
			}
			t.Logf("peak %.3g, worst difference %.3g", peak, worst)
			if worst > 1e-5*max(1, peak) {
				t.Fatalf("differs from NAM by %g (peak %g)", worst, peak)
			}
		})
	}
}

func TestSlimmableSizes(t *testing.T) {
	for _, c := range []struct {
		model string
		sizes []float64
	}{
		{"ref/A2.nam", []float64{0.5}},
		{"ref/slimmable_container.nam", []float64{0.33, 0.66}},
		{"ref/slimmable_wavenet.nam", []float64{1.0 / 3, 2.0 / 3}},
		{"ref/gen_slimmable.nam", []float64{1.0 / 3, 0.5, 2.0 / 3}},
		{"wavenet.nam", nil},
	} {
		f, err := Load("testdata/" + c.model)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(f.Sizes) != fmt.Sprint(c.sizes) {
			t.Errorf("%s: sizes %v, want %v", c.model, f.Sizes, c.sizes)
		}
	}
	// Sizes past the ends clamp to them.
	f, err := Load("testdata/ref/gen_slimmable.nam")
	if err != nil {
		t.Fatal(err)
	}
	x := testSignal(500, rand.New(rand.NewPCG(1, 2)))
	for _, pair := range [][2]float64{{-1, 0}, {2, 1}} {
		a, err := f.NewSized(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		b := newModel(t, f, pair[1])
		ya, yb := make([]float32, len(x)), make([]float32, len(x))
		a.Process(x, ya)
		b.Process(x, yb)
		if fmt.Sprint(ya) != fmt.Sprint(yb) {
			t.Errorf("size %v differs from size %v", pair[0], pair[1])
		}
	}
}

func testSignal(n int, rng *rand.Rand) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.5*math.Sin(float64(i)*0.05) + 0.2*(rng.Float64()-0.5))
	}
	return x
}

func TestProcessInPlaceResetAndAllocations(t *testing.T) {
	for _, c := range coreCases {
		f, err := Load("testdata/" + c.model)
		if err != nil {
			t.Fatal(err)
		}
		a, b := newModel(t, f, c.size), newModel(t, f, c.size)
		// More than the chunk at once, and long enough that the
		// histories move back.
		x := testSignal(5000, rand.New(rand.NewPCG(7, 8)))
		want := make([]float32, len(x))
		a.Process(x, want)
		got := append([]float32(nil), x...)
		b.Process(got, got)
		for i := range x {
			if got[i] != want[i] {
				t.Fatalf("%s: in place frame %d: %v, want %v", c.ref, i, got[i], want[i])
			}
		}
		b.Reset()
		again := make([]float32, len(x))
		b.Process(x, again)
		if fmt.Sprint(again) != fmt.Sprint(want) {
			t.Fatalf("%s: differs after Reset", c.ref)
		}
		for _, rate := range []float64{48000, 44100} {
			r, err := NewRunner(a, rate)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]float32, 512)
			if allocs := testing.AllocsPerRun(20, func() { r.Process(buf, buf) }); allocs != 0 {
				t.Fatalf("%s at %v Hz: %v allocations per block", c.ref, rate, allocs)
			}
			if allocs := testing.AllocsPerRun(2, r.Reset); allocs != 0 {
				t.Fatalf("%s at %v Hz: %v allocations per Reset", c.ref, rate, allocs)
			}
		}
	}
}

// model is a .nam file's JSON.
type model struct {
	Version      string    `json:"version"`
	Architecture string    `json:"architecture"`
	Config       any       `json:"config"`
	Weights      []float32 `json:"weights"`
	SampleRate   float64   `json:"sample_rate,omitempty"`
}

func (m model) parse(t *testing.T) *File {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLinearLongFilter(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	for _, bias := range []bool{false, true} {
		const rf = 3000 // longer than span, so the history moves back
		weights := make([]float32, rf+1)
		for i := range weights {
			weights[i] = float32(rng.Float64() - 0.5)
		}
		if !bias {
			weights = weights[:rf]
		}
		f := model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": rf, "bias": bias}, Weights: weights}.parse(t)
		m, err := f.New()
		if err != nil {
			t.Fatal(err)
		}
		x := testSignal(7000, rng)
		got := make([]float32, len(x))
		for i := 0; i < len(x); i += 500 {
			m.Process(x[i:min(i+500, len(x))], got[i:min(i+500, len(x))])
		}
		for i := range x {
			want := 0.0
			if bias {
				want = float64(weights[rf])
			}
			for k := 0; k < rf && k <= i; k++ {
				want += float64(weights[k]) * float64(x[i-k])
			}
			if math.Abs(float64(got[i])-want) > 1e-4 {
				t.Fatalf("bias %v frame %d: %v, want %v", bias, i, got[i], want)
			}
		}
	}
}

func TestRunnerResamples(t *testing.T) {
	// An identity model: the Runner's output is its input, delayed.
	f := model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false}, Weights: []float32{1}, SampleRate: 48000}.parse(t)
	for _, rate := range []float64{44100, 48000, 96000, 22050} {
		m, _ := f.New()
		r, err := NewRunner(m, rate)
		if err != nil {
			t.Fatal(err)
		}
		if (r.Latency() == 0) != (rate == 48000) {
			t.Fatalf("%v Hz: latency %d", rate, r.Latency())
		}
		x := make([]float32, 20000)
		for i := range x {
			x[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/rate))
		}
		y := make([]float32, len(x))
		for i, b := 0, 1; i < len(x); i, b = i+b, b*7%997+1 {
			r.Process(x[i:min(i+b, len(x))], y[i:min(i+b, len(x))])
		}
		worst := 0.0
		for i := r.Latency() + 200; i < len(x); i++ {
			worst = max(worst, math.Abs(float64(y[i]-x[i-r.Latency()])))
		}
		if worst > 1e-3 {
			t.Fatalf("%v Hz: output differs from the delayed input by %g", rate, worst)
		}
	}
}

func TestParseErrors(t *testing.T) {
	layer := map[string]any{"input_size": 1, "condition_size": 1, "head_size": 1, "head_bias": false, "channels": 2, "kernel_size": 3, "dilations": []int{1}, "activation": "Tanh"}
	with := func(base map[string]any, kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	wavenet := func(arrays ...map[string]any) map[string]any {
		if arrays == nil {
			arrays = []map[string]any{}
		}
		return map[string]any{"layers": arrays, "head": nil, "head_scale": 0.02}
	}
	weights := 2*1 + (2*2*3 + 2 + 2 + 2*2 + 2) + 2*1 + 1
	lstmCfg := map[string]any{"input_size": 1, "hidden_size": 2, "num_layers": 1}
	cases := map[string]struct {
		m    model
		want string
	}{
		"architecture":   {model{Version: "0.5.4", Architecture: "Transformer", Config: map[string]any{}}, `architecture "Transformer" is not supported`},
		"old version":    {model{Version: "0.4.0", Architecture: "LSTM", Config: lstmCfg}, "version 0.4.0 is not supported"},
		"new version":    {model{Version: "0.8.0", Architecture: "LSTM", Config: lstmCfg}, "version 0.8.0 is not supported"},
		"no version":     {model{Architecture: "LSTM", Config: lstmCfg}, `version "" is not supported`},
		"few weights":    {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(layer), Weights: make([]float32, weights-1)}, fmt.Sprintf("needs %d", weights)},
		"many weights":   {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(layer), Weights: make([]float32, weights+1)}, fmt.Sprintf("needs %d", weights)},
		"head mismatch":  {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(layer, with(layer, "input_size", 2, "channels", 3))}, "the head before it 1"},
		"activation":     {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "activation", "ELU"))}, `activation "ELU"`},
		"prelu slopes":   {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "activation", map[string]any{"type": "PReLU", "negative_slopes": []float32{1, 2, 3}}))}, "3 slopes for 2 channels"},
		"gating mode":    {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "gating_mode", "fancy"))}, `gating mode "fancy"`},
		"groups":         {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "groups_input", 3))}, "3 groups do not divide"},
		"bottleneck":     {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "bottleneck", 1, "layer1x1", map[string]any{"active": false, "groups": 1}))}, "without layer1x1"},
		"film":           {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "head1x1_post_film", map[string]any{}))}, "head1x1_post_film without head1x1"},
		"condition size": {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "condition_size", 2))}, "condition size 2, want 1"},
		"slim method":    {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "slimmable", map[string]any{"method": "prune"}))}, `slimmable method "prune"`},
		"slim channels":  {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet(with(layer, "slimmable", map[string]any{"method": "slice_channels_uniform", "kwargs": map[string]any{"allowed_channels": []int{2, 1}}}))}, "not ascending"},
		"lstm weights":   {model{Version: "0.5.4", Architecture: "LSTM", Config: lstmCfg, Weights: make([]float32, 3)}, "needs 39"},
		"stereo":         {model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false, "out_channels": 2}, Weights: []float32{1, 1}}, "1 inputs and 2 outputs"},
		"sample rate":    {model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false}, Weights: []float32{1}, SampleRate: -1}, "sample rate"},
		"no layer array": {model{Version: "0.5.4", Architecture: "WaveNet", Config: wavenet()}, "no layer arrays"},
		"sequential":     {model{Version: "0.7.0", Architecture: "Sequential", Config: map[string]any{"models": []any{}}}, "no models"},
		"container":      {model{Version: "0.7.0", Architecture: "SlimmableContainer", Config: map[string]any{"submodels": []any{map[string]any{"max_value": 0.5, "model": model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false}, Weights: []float32{1}}}}}}, "below 1"},
	}
	for name, c := range cases {
		data, _ := json.Marshal(c.m)
		_, err := Parse(data)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
	}
	if f := (model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false}, Weights: []float32{1}}).parse(t); f.SampleRate != 48000 {
		t.Errorf("default sample rate %v", f.SampleRate)
	}
	// A newer patch version is read.
	(model{Version: "0.7.9", Architecture: "Linear", Config: map[string]any{"receptive_field": 1, "bias": false}, Weights: []float32{1}}).parse(t)
}

// generated builds a model of a configuration with random weights.
// positive draws them from (0.05, 0.5), as batch norm variances must be.
func generated(t testing.TB, rng *rand.Rand, arch string, config map[string]any, rate float64, positive bool) model {
	t.Helper()
	m := model{Version: "0.7.0", Architecture: arch, Config: config, Weights: []float32{}, SampleRate: rate}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseSpec(data)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = build(s, 1)
	if err == nil {
		return m // a model of models, without weights of its own
	}
	var we *weightsError
	if !errors.As(err, &we) {
		t.Fatalf("%s: %v", arch, err)
	}
	m.Weights = make([]float32, we.need)
	for i := range m.Weights {
		if positive {
			m.Weights[i] = float32(0.05 + 0.45*rng.Float64())
		} else {
			m.Weights[i] = float32(rng.Float64()*0.8 - 0.4)
		}
	}
	return m
}

// TestGenerateModels writes the gen_ models of coreCases when
// NAM_GENERATE is set; render.cpp makes their references.
func TestGenerateModels(t *testing.T) {
	if os.Getenv("NAM_GENERATE") == "" {
		t.Skip("set NAM_GENERATE to write testdata/ref/gen_*.nam")
	}
	rng := rand.New(rand.NewPCG(11, 12))
	gen := func(arch string, config map[string]any, rate float64, positive bool) model {
		return generated(t, rng, arch, config, rate, positive)
	}
	models := map[string]model{}

	models["gen_convnet"] = gen("ConvNet", map[string]any{
		"channels": 4, "dilations": []int{1, 3, 9}, "batchnorm": false,
		"activation": map[string]any{"type": "PReLU", "negative_slopes": []float32{0.1, 0.2, 0.3, 0.4}},
	}, 48000, false)
	models["gen_convnet_batchnorm"] = gen("ConvNet", map[string]any{
		"channels": 3, "dilations": []int{1, 2, 4, 8}, "batchnorm": true, "activation": "Tanh",
	}, 48000, true)

	condition := gen("WaveNet", map[string]any{"layers": []any{map[string]any{
		"input_size": 1, "condition_size": 1, "head_size": 2, "head_bias": true, "channels": 2,
		"kernel_size": 2, "dilations": []int{1, 2}, "activation": "Tanh", "gated": false,
	}}, "head": nil, "head_scale": 1}, 48000, false)
	models["gen_wavenet_features"] = gen("WaveNet", map[string]any{
		"condition_dsp": condition,
		"layers": []any{
			map[string]any{
				"input_size": 1, "condition_size": 2, "channels": 4, "bottleneck": 2,
				"kernel_sizes": []int{3, 2, 4}, "dilations": []int{1, 2, 5},
				"activation": []any{
					map[string]any{"type": "LeakyReLU", "negative_slope": 0.05},
					"SiLU",
					map[string]any{"type": "LeakyHardtanh", "min_val": -0.5, "max_val": 0.7, "min_slope": 0.1, "max_slope": 0.2},
				},
				"gating_mode":          []string{"gated", "blended", "none"},
				"secondary_activation": []any{"Sigmoid", "Hardswish", "Sigmoid"},
				"groups_input":         2, "groups_input_mixin": 2,
				"layer1x1":              map[string]any{"active": true, "groups": 2},
				"head1x1":               map[string]any{"active": true, "out_channels": 4, "groups": 2},
				"head":                  map[string]any{"out_channels": 3, "kernel_size": 3, "bias": true, "head_dilation": 2},
				"conv_pre_film":         map[string]any{"shift": true, "groups": 2},
				"conv_post_film":        map[string]any{"shift": false},
				"input_mixin_pre_film":  map[string]any{"groups": 2},
				"input_mixin_post_film": map[string]any{"shift": false, "groups": 2},
				"activation_pre_film":   map[string]any{},
				"activation_post_film":  map[string]any{"active": true},
				"layer1x1_post_film":    map[string]any{"groups": 2},
				"head1x1_post_film":     map[string]any{"shift": false},
			},
			map[string]any{
				"input_size": 4, "condition_size": 2, "channels": 3, "head_size": 2, "head_bias": true,
				"kernel_size": 2, "dilations": []int{1, 4}, "activation": "Softsign", "gated": false,
				"layer1x1": map[string]any{"active": false, "groups": 1}, "activation_pre_film": false,
			},
		},
		"head":       map[string]any{"channels": 3, "out_channels": 1, "kernel_sizes": []int{2, 1, 3}, "activation": "PReLU"},
		"head_scale": 0.5,
	}, 48000, false)
	models["gen_wavenet_activations"] = gen("WaveNet", map[string]any{"layers": []any{map[string]any{
		"input_size": 1, "condition_size": 1, "head_size": 1, "head_bias": false, "channels": 3,
		"kernel_size": 3, "dilations": []int{1, 2, 4, 8, 16},
		"activation":           []any{"ReLU", "Hardtanh", "Fasttanh", "Sigmoid", "Tanh"},
		"gating_mode":          "blended",
		"secondary_activation": map[string]any{"type": "PReLU", "negative_slopes": []float32{0.2, 0.1, 0.3}},
	}}, "head": nil, "head_scale": 0.3}, 48000, false)

	models["gen_sequential"] = gen("Sequential", map[string]any{"models": []any{
		gen("Linear", map[string]any{"receptive_field": 5, "bias": true, "out_channels": 2}, 48000, false),
		gen("WaveNet", map[string]any{"in_channels": 2, "layers": []any{map[string]any{
			"input_size": 2, "condition_size": 2, "head_size": 1, "head_bias": true, "channels": 3,
			"kernel_size": 3, "dilations": []int{1, 3}, "activation": "Tanh", "gated": true,
		}}, "head": nil, "head_scale": 0.5}, 0, false),
		gen("ConvNet", map[string]any{"channels": 2, "dilations": []int{1, 2}, "batchnorm": false, "activation": "ReLU"}, 48000, false),
		gen("Linear", map[string]any{"receptive_field": 3, "bias": false}, 0, false),
	}}, 48000, false)
	models["gen_sequential_lstm"] = gen("Sequential", map[string]any{"models": []any{
		gen("Linear", map[string]any{"receptive_field": 4, "bias": false, "out_channels": 2}, 0, false),
		gen("LSTM", map[string]any{"input_size": 2, "hidden_size": 3, "num_layers": 2, "in_channels": 2, "out_channels": 2}, 48000, false),
		gen("Linear", map[string]any{"receptive_field": 3, "bias": true, "in_channels": 2}, 0, false),
	}}, 48000, false)

	models["gen_slimmable"] = gen("WaveNet", map[string]any{"layers": []any{
		map[string]any{
			"input_size": 1, "condition_size": 1, "head_size": 3, "head_bias": true, "channels": 4, "bottleneck": 2,
			"kernel_size": 3, "dilations": []int{1, 2, 4}, "activation": "Tanh", "gating_mode": "gated",
			"layer1x1": map[string]any{"active": true, "groups": 1}, "conv_post_film": map[string]any{},
			"activation_post_film": map[string]any{"shift": false},
			"slimmable":            map[string]any{"method": "slice_channels_uniform", "kwargs": map[string]any{"allowed_channels": []int{2, 4}}},
		},
		map[string]any{
			"input_size": 4, "condition_size": 1, "head_size": 1, "head_bias": true, "channels": 3,
			"kernel_size": 2, "dilations": []int{1, 2}, "activation": "ReLU", "gated": false,
			"slimmable": map[string]any{"method": "slice_channels_uniform"},
		},
	}, "head": nil, "head_scale": 0.2}, 48000, false)

	for name, m := range models {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("testdata", "ref", name+".nam"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// BenchmarkWaveNetStandard runs the shape of NAM's standard WaveNet (16
// and 8 channels, ten layers each) on blocks of 128 frames.
func BenchmarkWaveNetStandard(b *testing.B) {
	dilations := []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}
	array := func(input, head, channels int, bias bool) map[string]any {
		return map[string]any{"input_size": input, "condition_size": 1, "head_size": head, "head_bias": bias,
			"channels": channels, "kernel_size": 3, "dilations": dilations, "activation": "Tanh", "gated": false}
	}
	m := generated(b, rand.New(rand.NewPCG(9, 10)), "WaveNet", map[string]any{
		"layers": []any{array(1, 8, 16, false), array(16, 1, 8, true)}, "head": nil, "head_scale": 0.02,
	}, 48000, false)
	data, err := json.Marshal(m)
	if err != nil {
		b.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		b.Fatal(err)
	}
	benchmark(b, f)
}

// BenchmarkA2 runs NAM's A2 model at full size on blocks of 128 frames.
func BenchmarkA2(b *testing.B) {
	f, err := Load("testdata/ref/A2.nam")
	if err != nil {
		b.Fatal(err)
	}
	benchmark(b, f)
}

func benchmark(b *testing.B, f *File) {
	m := newModel(b, f, -1)
	buf := testSignal(128, rand.New(rand.NewPCG(9, 10)))
	b.SetBytes(int64(len(buf) * 4))
	for b.Loop() {
		m.Process(buf, buf)
	}
}
