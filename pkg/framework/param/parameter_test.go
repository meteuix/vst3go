package param

import (
	"math"
	"sync"
	"testing"
)

func TestNormalizedAccessors(t *testing.T) {
	p := &Parameter{Min: -12, Max: 12}

	p.SetNormalized(0.75)
	if got := p.GetNormalized(); got != 0.75 {
		t.Fatalf("GetNormalized() = %f, want 0.75", got)
	}

	if got := p.GetValue(); got != 0.75 {
		t.Fatalf("GetValue() = %f, want 0.75", got)
	}
}

func TestPlainAccessors(t *testing.T) {
	p := &Parameter{Min: -12, Max: 12}

	p.SetPlain(6)
	if got := p.GetPlain(); got != 6 {
		t.Fatalf("GetPlain() = %f, want 6", got)
	}

	if got := p.GetPlainValue(); got != 6 {
		t.Fatalf("GetPlainValue() = %f, want 6", got)
	}
}

func TestParameterConcurrentAccess(t *testing.T) {
	p := &Parameter{Min: -12, Max: 12}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed float64) {
			defer wg.Done()

			for j := 0; j < 1_000; j++ {
				value := seed + float64(j%10)/10.0
				p.SetNormalized(value / 10.0)
				_ = p.GetNormalized()
				p.SetPlain(value)
				_ = p.GetPlain()
			}
		}(float64(i))
	}

	wg.Wait()
}

func TestDefaultParseAcceptsFormattedValues(t *testing.T) {
	p := New(1, "Value").Range(-60, 12).Build()
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"0", 0},
		{"-6.0 dB", -6},
		{"Q: 0.71", 0.71},
		{"Low-Cut 20 Hz", 20},
		{"1e1", 10},
		{"-∞ dB", -60},
		{"+inf", 12},
	} {
		got, err := p.ParseValue(tc.in)
		if err != nil {
			t.Fatalf("ParseValue(%q) failed: %v", tc.in, err)
		}
		if want := p.Normalize(tc.want); math.Abs(got-want) > 1e-12 {
			t.Fatalf("ParseValue(%q) = %v, want %v", tc.in, got, want)
		}
	}
	if _, err := p.ParseValue("off"); err == nil {
		t.Fatal("ParseValue(\"off\") should fail")
	}
}

// Time text switches units where the rounded number does, so a value just
// under a boundary reads like the boundary and parses back to it.
func TestTimeTextSwitchesUnitsWhereItRounds(t *testing.T) {
	p := TimeParameter(1, "Release", 1, 2000, 100).Build()
	for _, tc := range []struct {
		ms   float64
		want string
	}{
		{999.94, "999.9 ms"},
		{999.96, "1.00 s"},
		{1000, "1.00 s"},
		{1500, "1.50 s"},
	} {
		if got := p.FormatValue(p.Normalize(tc.ms)); got != tc.want {
			t.Fatalf("%v ms reads %q, want %q", tc.ms, got, tc.want)
		}
	}
	for _, tc := range []struct {
		ms   float64
		want string
	}{
		{0.00999, "9.99 µs"},
		{0.999996, "1.0 ms"},
		{999.96, "1.00 s"},
	} {
		if got := TimeFormatter(tc.ms); got != tc.want {
			t.Fatalf("TimeFormatter(%v) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestBypassParameterFlags(t *testing.T) {
	p := BypassParameter(1, "Bypass").Build()
	if p.Flags&IsBypass == 0 || p.Flags&CanAutomate == 0 || p.StepCount != 1 {
		t.Fatalf("bypass flags = %#x steps = %d", p.Flags, p.StepCount)
	}
	meter := OutputLevelMeter(2, "Level").Build()
	if v, err := meter.ParseValue(meter.FormatValue(0)); err != nil || v != 0 {
		t.Fatalf("meter string round trip = %v, %v", v, err)
	}
}

func TestParameterScales(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*max(1, math.Abs(b)) }

	freq := FrequencyParameter(1, "Cutoff", 20, 20000, 1000).Build()
	if !near(freq.Denormalize(0.5), math.Sqrt(20*20000)) {
		t.Errorf("log midpoint %v, want the geometric mean", freq.Denormalize(0.5))
	}
	// Each decade gets the same travel.
	if d1, d2 := freq.Normalize(200)-freq.Normalize(20), freq.Normalize(20000)-freq.Normalize(2000); !near(d1, d2) || !near(d1, 1.0/3) {
		t.Errorf("decades take %v and %v of the range", d1, d2)
	}
	if !near(freq.GetPlain(), 1000) {
		t.Errorf("default %v Hz, want 1000", freq.GetPlain())
	}
	freq.SetPlain(440)
	if !near(freq.GetPlain(), 440) {
		t.Errorf("SetPlain(440) reads back %v", freq.GetPlain())
	}
	if freq.Normalize(5) != 0 || freq.Normalize(1e6) != 1 || freq.Normalize(math.NaN()) != 0 {
		t.Error("out-of-range values are not clamped")
	}

	pre := TimeParameter(2, "Pre-Delay", 0, 400, 20).Build()
	if pre.Scale != ScalePower || !near(pre.Denormalize(0.5), 50) {
		t.Errorf("zero-based time midpoint %v, want an eighth of the range", pre.Denormalize(0.5))
	}
	for _, v := range []float64{0, 1, 20, 50, 399, 400} {
		if got := pre.Denormalize(pre.Normalize(v)); !near(got, v) {
			t.Errorf("%v ms round-trips to %v", v, got)
		}
	}
	if attack := TimeParameter(3, "Attack", 0.1, 100, 10).Build(); attack.Scale != ScaleLog {
		t.Error("positive time range is not logarithmic")
	}

	// The default is converted with the final scale whatever the order.
	a := New(4, "A").Default(100).Range(10, 1000).Logarithmic().Build()
	b := New(5, "B").Range(10, 1000).Logarithmic().Default(100).Build()
	if !near(a.DefaultValue, 0.5) || a.DefaultValue != b.DefaultValue {
		t.Errorf("defaults %v and %v, want 0.5", a.DefaultValue, b.DefaultValue)
	}

	// Steps stay linear and snap.
	semis := New(6, "Semis").Range(-12, 12).Steps(24).Logarithmic().Build()
	if semis.Denormalize(0.51) != 0 || semis.GetPlain() != -12 {
		t.Errorf("stepped values %v, %v", semis.Denormalize(0.51), semis.GetPlain())
	}
}

func TestParameterScalePanicsOnInvalidRanges(t *testing.T) {
	for name, build := range map[string]func(){
		"log from zero":  func() { New(1, "x").Range(0, 10).Logarithmic().Build() },
		"center outside": func() { New(1, "x").Range(0, 10).Center(12).Build() },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			build()
		}()
	}
}
