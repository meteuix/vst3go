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
