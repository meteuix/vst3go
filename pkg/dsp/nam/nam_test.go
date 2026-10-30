package nam

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

func readF32(t *testing.T, path string) []float32 {
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

// TestMatchesNAMCore compares with NeuralAmpModelerCore v0.1.0 (fast tanh,
// blocks of 64, LSTM prewarmed for half a second as in later versions)
// on NAM's example models, from github.com/sdatkinson/NeuralAmpModelerCore
// (MIT).
func TestMatchesNAMCore(t *testing.T) {
	in := readF32(t, "testdata/input.f32")
	for _, name := range []string{"wavenet", "lstm"} {
		t.Run(name, func(t *testing.T) {
			f, err := Load("testdata/" + name + ".nam")
			if err != nil {
				t.Fatal(err)
			}
			if f.SampleRate != 48000 || f.Metadata.Loudness == nil {
				t.Fatalf("sample rate %v, loudness %v", f.SampleRate, f.Metadata.Loudness)
			}
			m, err := f.New()
			if err != nil {
				t.Fatal(err)
			}
			want := readF32(t, "testdata/"+name+".f32")
			got := make([]float32, len(in))
			for i := 0; i < len(in); i += 64 {
				m.Process(in[i:min(i+64, len(in))], got[i:min(i+64, len(in))])
			}
			worst := 0.0
			for i := range want {
				worst = max(worst, math.Abs(float64(got[i]-want[i])))
			}
			if worst > 1e-6 {
				t.Fatalf("differs from NAM by %g", worst)
			}
		})
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

func randomWeights(rng *rand.Rand, n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		w[i] = float32(rng.Float64()*0.6 - 0.3)
	}
	return w
}

func waveNetWeights(arrays []layerArrayConfig) int {
	n := 1
	for _, a := range arrays {
		zc := a.Channels
		if a.Gated {
			zc *= 2
		}
		n += a.InputSize * a.Channels
		n += len(a.Dilations) * (zc*a.Channels*a.KernelSize + zc + zc + a.Channels*a.Channels + a.Channels)
		n += a.Channels * a.HeadSize
		if a.HeadBias {
			n += a.HeadSize
		}
	}
	return n
}

func refTanh(x float64) float64 {
	ax := math.Abs(x)
	x2 := x * x
	return x * (2.45550750702956 + 2.45550750702956*ax + (0.893229853513558+0.821226666969744*ax)*x2) /
		(2.44506634652299 + (2.44506634652299+x2)*math.Abs(x+0.814642734961073*x*ax))
}

func refActivation(name string, x float64) float64 {
	switch name {
	case "Tanh", "Fasttanh":
		return refTanh(x)
	case "Hardtanh":
		return max(-1, min(1, x))
	case "ReLU":
		return max(0, x)
	case "Sigmoid":
		return 1 / (1 + math.Exp(-x))
	}
	panic(name)
}

// refWaveNet computes a WaveNet one array and layer at a time over the
// whole signal from the flat weights, preceded by warm frames of silence.
func refWaveNet(arrays []layerArrayConfig, weights []float32, x []float64, warm int) []float64 {
	x = append(make([]float64, warm), x...)
	n := len(x)
	pos := 0
	next := func() float64 { pos++; return float64(weights[pos-1]) }
	grid := func(rows int) [][]float64 {
		g := make([][]float64, rows)
		for i := range g {
			g[i] = make([]float64, n)
		}
		return g
	}
	input := [][]float64{x}
	head := grid(arrays[0].Channels)
	for _, a := range arrays {
		ch := a.Channels
		zc := ch
		if a.Gated {
			zc *= 2
		}
		cur := grid(ch)
		for c := range ch {
			for i := range a.InputSize {
				w := next()
				for t := range n {
					cur[c][t] += w * input[i][t]
				}
			}
		}
		for _, d := range a.Dilations {
			conv := make([][][]float64, zc) // [out][in][tap]
			for o := range conv {
				conv[o] = make([][]float64, ch)
				for i := range conv[o] {
					conv[o][i] = make([]float64, a.KernelSize)
					for k := range conv[o][i] {
						conv[o][i][k] = next()
					}
				}
			}
			bias := make([]float64, zc)
			for o := range bias {
				bias[o] = next()
			}
			mixin := make([]float64, zc)
			for o := range mixin {
				mixin[o] = next()
			}
			mix := make([][]float64, ch)
			for c := range mix {
				mix[c] = make([]float64, ch)
				for j := range mix[c] {
					mix[c][j] = next()
				}
			}
			mixBias := make([]float64, ch)
			for c := range mixBias {
				mixBias[c] = next()
			}
			out := grid(ch)
			z := make([]float64, zc)
			for t := range n {
				for o := range zc {
					s := bias[o] + mixin[o]*x[t]
					for i := range ch {
						for k := range a.KernelSize {
							if tt := t - (a.KernelSize-1-k)*d; tt >= 0 {
								s += conv[o][i][k] * cur[i][tt]
							}
						}
					}
					z[o] = s
				}
				for o := range ch {
					z[o] = refActivation(a.Activation, z[o])
					if a.Gated {
						z[o] *= refActivation("Sigmoid", z[ch+o])
					}
					head[o][t] += z[o]
				}
				for c := range ch {
					s := cur[c][t] + mixBias[c]
					for j := range ch {
						s += mix[c][j] * z[j]
					}
					out[c][t] = s
				}
			}
			cur = out
		}
		hw := make([][]float64, a.HeadSize)
		for h := range hw {
			hw[h] = make([]float64, ch)
			for c := range hw[h] {
				hw[h][c] = next()
			}
		}
		next := head
		head = grid(a.HeadSize)
		for h := range a.HeadSize {
			b := 0.0
			if a.HeadBias {
				b = float64(weights[pos+h])
			}
			for t := range n {
				s := b
				for c := range ch {
					s += hw[h][c] * next[c][t]
				}
				head[h][t] = s
			}
		}
		if a.HeadBias {
			pos += a.HeadSize
		}
		input = cur
	}
	scale := next()
	y := make([]float64, n-warm)
	for t := range y {
		y[t] = scale * head[0][warm+t]
	}
	return y
}

func testSignal(n int, rng *rand.Rand) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.6*math.Sin(float64(i)*0.031)*math.Sin(float64(i)*0.0007) + 0.1*(rng.Float64()*2-1))
	}
	return x
}

func compare(t *testing.T, got []float32, want []float64, tol float64) {
	t.Helper()
	worst, at := 0.0, 0
	for i, w := range want {
		if d := math.Abs(float64(got[i]) - w); d > worst {
			worst, at = d, i
		}
	}
	if worst > tol {
		t.Fatalf("frame %d: got %v, want %v (error %g)", at, got[at], want[at], worst)
	}
}

func TestWaveNetMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	cases := map[string][]layerArrayConfig{
		"standard shape": {
			{InputSize: 1, ConditionSize: 1, HeadSize: 4, Channels: 6, KernelSize: 3, Dilations: []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}, Activation: "Tanh"},
			{InputSize: 6, ConditionSize: 1, HeadSize: 1, Channels: 4, KernelSize: 3, Dilations: []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}, Activation: "Tanh", HeadBias: true},
		},
		"gated": {
			{InputSize: 1, ConditionSize: 1, HeadSize: 3, Channels: 5, KernelSize: 2, Dilations: []int{1, 3, 9}, Activation: "Tanh", Gated: true, HeadBias: true},
			{InputSize: 5, ConditionSize: 1, HeadSize: 1, Channels: 3, KernelSize: 4, Dilations: []int{2, 7}, Activation: "ReLU", Gated: true},
		},
		"long history": {
			{InputSize: 1, ConditionSize: 1, HeadSize: 1, Channels: 2, KernelSize: 3, Dilations: []int{1500, 1}, Activation: "Hardtanh", HeadBias: true},
		},
		"pointwise": {
			{InputSize: 1, ConditionSize: 1, HeadSize: 1, Channels: 3, KernelSize: 1, Dilations: []int{1}, Activation: "Sigmoid"},
		},
	}
	for name, arrays := range cases {
		t.Run(name, func(t *testing.T) {
			weights := randomWeights(rng, waveNetWeights(arrays))
			f := model{Version: "0.5.4", Architecture: "WaveNet", Config: map[string]any{"layers": arrays, "head": nil, "head_scale": 0.02}, Weights: weights}.parse(t)
			m, err := f.New()
			if err != nil {
				t.Fatal(err)
			}
			x := testSignal(9000, rng)
			x64 := make([]float64, len(x))
			for i, v := range x {
				x64[i] = float64(v)
			}
			want := refWaveNet(arrays, weights, x64, m.net.prewarm(0))
			got := make([]float32, len(x))
			// Uneven blocks, some longer than chunk.
			for i, b := 0, 1; i < len(x); i, b = i+b, b*3%1001+1 {
				m.Process(x[i:min(i+b, len(x))], got[i:min(i+b, len(x))])
			}
			compare(t, got, want, 2e-5)
		})
	}
}

func TestLSTMMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	const hidden, layers = 5, 2
	n := 0
	for i := range layers {
		in := hidden
		if i == 0 {
			in = 1
		}
		n += 4*hidden*(in+hidden) + 4*hidden + 2*hidden
	}
	weights := randomWeights(rng, n+hidden+1)
	f := model{Version: "0.5.4", Architecture: "LSTM", Config: map[string]any{"input_size": 1, "hidden_size": hidden, "num_layers": layers}, Weights: weights, SampleRate: 2000}.parse(t)
	m, err := f.New()
	if err != nil {
		t.Fatal(err)
	}
	x := testSignal(3000, rng)

	// The reference: PyTorch's LSTM from the trained initial state after
	// 1000 frames (half a second at 2 kHz) of silence.
	pos := 0
	next := func(k int) []float64 {
		v := make([]float64, k)
		for i := range v {
			v[i] = float64(weights[pos+i])
		}
		pos += k
		return v
	}
	type cell struct{ w, b, h, c []float64 }
	cells := make([]cell, layers)
	for i := range cells {
		in := hidden
		if i == 0 {
			in = 1
		}
		cells[i] = cell{next(4 * hidden * (in + hidden)), next(4 * hidden), next(hidden), next(hidden)}
	}
	headW, headB := next(hidden), next(1)[0]
	sig := func(v float64) float64 { return 0.5 * (refTanh(0.5*v) + 1) }
	step := func(v float64) float64 {
		in := []float64{v}
		for i := range cells {
			c := &cells[i]
			xh := append(append([]float64{}, in...), c.h...)
			g := make([]float64, 4*hidden)
			for r := range g {
				g[r] = c.b[r]
				for j, u := range xh {
					g[r] += c.w[r*len(xh)+j] * u
				}
			}
			for j := range hidden {
				c.c[j] = sig(g[hidden+j])*c.c[j] + sig(g[j])*refTanh(g[2*hidden+j])
				c.h[j] = sig(g[3*hidden+j]) * refTanh(c.c[j])
			}
			in = c.h
		}
		y := headB
		for j, h := range in {
			y += headW[j] * h
		}
		return y
	}
	for range 1000 {
		step(0)
	}
	want := make([]float64, len(x))
	for i, v := range x {
		want[i] = step(float64(v))
	}
	got := make([]float32, len(x))
	m.Process(x, got)
	compare(t, got, want, 2e-5)

	// Reset returns to the same state.
	m.Reset()
	again := make([]float32, len(x))
	m.Process(x, again)
	if again[len(x)-1] != got[len(x)-1] {
		t.Fatalf("after Reset: %v, want %v", again[len(x)-1], got[len(x)-1])
	}
}

func TestLinearMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	for _, bias := range []bool{false, true} {
		const rf = 3000 // longer than span, so the history moves back
		n := rf
		if bias {
			n++
		}
		weights := randomWeights(rng, n)
		f := model{Version: "0.5.4", Architecture: "Linear", Config: map[string]any{"receptive_field": rf, "bias": bias}, Weights: weights}.parse(t)
		m, err := f.New()
		if err != nil {
			t.Fatal(err)
		}
		x := testSignal(7000, rng)
		want := make([]float64, len(x))
		for i := range x {
			if bias {
				want[i] = float64(weights[rf])
			}
			for k := 0; k < rf && k <= i; k++ {
				want[i] += float64(weights[k]) * float64(x[i-k])
			}
		}
		got := make([]float32, len(x))
		for i := 0; i < len(x); i += 500 {
			m.Process(x[i:min(i+500, len(x))], got[i:min(i+500, len(x))])
		}
		compare(t, got, want, 1e-4)
	}
}

func TestProcessInPlaceAndAllocations(t *testing.T) {
	for _, name := range []string{"wavenet", "lstm"} {
		f, err := Load("testdata/" + name + ".nam")
		if err != nil {
			t.Fatal(err)
		}
		a, _ := f.New()
		b, _ := f.New()
		x := testSignal(1000, rand.New(rand.NewPCG(7, 8)))
		want := make([]float32, len(x))
		a.Process(x, want)
		b.Process(x, x)
		for i := range x {
			if x[i] != want[i] {
				t.Fatalf("%s: in place frame %d: %v, want %v", name, i, x[i], want[i])
			}
		}
		for _, rate := range []float64{48000, 44100} {
			r, err := NewRunner(a, rate)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]float32, 512)
			if allocs := testing.AllocsPerRun(20, func() { r.Process(buf, buf) }); allocs != 0 {
				t.Fatalf("%s at %v Hz: %v allocations per block", name, rate, allocs)
			}
			if allocs := testing.AllocsPerRun(5, r.Reset); allocs != 0 {
				t.Fatalf("%s at %v Hz: %v allocations per Reset", name, rate, allocs)
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
	layer := layerArrayConfig{InputSize: 1, ConditionSize: 1, HeadSize: 1, Channels: 2, KernelSize: 3, Dilations: []int{1}, Activation: "Tanh"}
	wavenet := func(arrays ...layerArrayConfig) map[string]any {
		return map[string]any{"layers": arrays, "head": nil, "head_scale": 0.02}
	}
	second := layer
	second.InputSize, second.Channels = 2, 3
	elu := layer
	elu.Activation = "ELU"
	cases := map[string]struct {
		m    model
		want string
	}{
		"architecture":   {model{Architecture: "ConvNet", Config: map[string]any{}}, `architecture "ConvNet" is not supported`},
		"few weights":    {model{Architecture: "WaveNet", Config: wavenet(layer), Weights: make([]float32, waveNetWeights([]layerArrayConfig{layer})-1)}, "needs more"},
		"many weights":   {model{Architecture: "WaveNet", Config: wavenet(layer), Weights: make([]float32, waveNetWeights([]layerArrayConfig{layer})+1)}, "uses"},
		"head mismatch":  {model{Architecture: "WaveNet", Config: wavenet(layer, second)}, "the head before it 1"},
		"activation":     {model{Architecture: "WaveNet", Config: wavenet(elu)}, `activation "ELU"`},
		"head network":   {model{Architecture: "WaveNet", Config: map[string]any{"layers": []layerArrayConfig{layer}, "head": map[string]any{}, "head_scale": 1}}, "head network"},
		"lstm weights":   {model{Architecture: "LSTM", Config: map[string]any{"input_size": 1, "hidden_size": 2, "num_layers": 1}, Weights: make([]float32, 3)}, "needs more"},
		"sample rate":    {model{Architecture: "Linear", Config: map[string]any{"receptive_field": 1}, Weights: []float32{1}, SampleRate: -1}, "sample rate"},
		"no layer array": {model{Architecture: "WaveNet", Config: wavenet()}, "no layer arrays"},
	}
	for name, c := range cases {
		data, _ := json.Marshal(c.m)
		_, err := Parse(data)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
	}
	if f := (model{Architecture: "Linear", Config: map[string]any{"receptive_field": 1}, Weights: []float32{1}}).parse(t); f.SampleRate != 48000 {
		t.Errorf("default sample rate %v", f.SampleRate)
	}
}

// BenchmarkWaveNetStandard runs the shape of NAM's standard WaveNet
// (16 and 8 channels, ten layers each) on blocks of 128 frames.
func BenchmarkWaveNetStandard(b *testing.B) {
	dilations := []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}
	arrays := []layerArrayConfig{
		{InputSize: 1, ConditionSize: 1, HeadSize: 8, Channels: 16, KernelSize: 3, Dilations: dilations, Activation: "Tanh"},
		{InputSize: 16, ConditionSize: 1, HeadSize: 1, Channels: 8, KernelSize: 3, Dilations: dilations, Activation: "Tanh", HeadBias: true},
	}
	rng := rand.New(rand.NewPCG(9, 10))
	data, _ := json.Marshal(model{Version: "0.5.4", Architecture: "WaveNet", Config: map[string]any{"layers": arrays, "head": nil, "head_scale": 0.02}, Weights: randomWeights(rng, waveNetWeights(arrays))})
	f, err := Parse(data)
	if err != nil {
		b.Fatal(err)
	}
	m, _ := f.New()
	buf := testSignal(128, rng)
	b.SetBytes(int64(len(buf) * 4))
	for b.Loop() {
		m.Process(buf, buf)
	}
}
