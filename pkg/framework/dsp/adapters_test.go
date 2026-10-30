package dsp

import (
	"math"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/dsp/dynamics"
	"github.com/meteuix/vst3go/v2/pkg/dsp/utility"
)

func TestCompressorAdapter(t *testing.T) {
	sampleRate := 48000.0
	comp := dynamics.NewCompressor(sampleRate)
	comp.SetThreshold(-20)
	comp.SetRatio(4)
	comp.SetAttack(0.001)
	comp.SetRelease(0.050)
	comp.SetKnee(dynamics.KneeHard, 0)

	adapter := NewCompressorAdapter(comp)

	// A constant 0 dBFS signal settles at -20 + 20/4 = -15 dBFS.
	buffer := make([]float32, 4800)
	for i := range buffer {
		buffer[i] = 1
	}
	adapter.Process(buffer)
	if db := 20 * math.Log10(float64(buffer[len(buffer)-1])); math.Abs(db+15) > 0.5 {
		t.Fatalf("compressed level %.2f dB, want -15", db)
	}

	// Stereo: linked, so a quiet right channel gets the left channel's gain.
	adapter.Reset()
	left := make([]float32, 4800)
	right := make([]float32, 4800)
	for i := range left {
		left[i], right[i] = 1, 0.01
	}
	adapter.ProcessStereo(left, right)
	if ratio := right[4799] / left[4799]; math.Abs(float64(ratio)-0.01) > 1e-6 {
		t.Fatalf("stereo link: right/left = %v, want 0.01", ratio)
	}
}

func TestGateAdapter(t *testing.T) {
	sampleRate := 48000.0
	gate := dynamics.NewGate(sampleRate)
	gate.SetThreshold(-30)
	gate.SetRange(-60)

	adapter := NewGateAdapter(gate)

	// A quiet signal stays gated by the full range.
	buffer := make([]float32, 100)
	for i := range buffer {
		buffer[i] = 0.001
	}
	adapter.Process(buffer)
	if db := 20 * math.Log10(float64(buffer[99])/0.001); math.Abs(db+60) > 0.1 {
		t.Fatalf("gated level %.2f dB, want -60", db)
	}

	// A loud signal opens it.
	for i := range buffer {
		buffer[i] = 0.5
	}
	gate.SetAttack(0)
	adapter.Process(buffer)
	if buffer[99] != 0.5 {
		t.Fatalf("open gate output %v, want 0.5", buffer[99])
	}
}

func TestDCBlockerAdapter(t *testing.T) {
	sampleRate := 48000.0
	adapter := NewDCBlockerAdapter(sampleRate)

	// Test with DC offset
	// Process multiple buffers to allow settling
	buffer := make([]float32, 1000)
	for j := 0; j < 10; j++ { // Process 10 buffers
		for i := range buffer {
			buffer[i] = 0.5 // DC offset
		}
		adapter.Process(buffer)
	}

	// After processing many samples, DC should be mostly removed
	sum := float32(0)
	count := 0
	for _, v := range buffer[800:] { // Check last 200 samples
		sum += v
		count++
	}
	avg := sum / float32(count)

	// DC blocker is a high-pass filter, so some DC may remain
	// but it should be significantly reduced
	if math.Abs(float64(avg)) > 0.05 { // More lenient threshold
		t.Errorf("DC not sufficiently removed, average: %f", avg)
	}

	adapter.Reset()
}

func TestNoiseAdapter(t *testing.T) {
	adapter := NewNoiseAdapter(utility.WhiteNoise, 0.1)

	// Test with silence
	buffer := make([]float32, 1000)
	adapter.Process(buffer)

	// Should have added noise
	hasNoise := false
	for _, v := range buffer {
		if v != 0 {
			hasNoise = true
			break
		}
	}

	if !hasNoise {
		t.Error("No noise was added")
	}

	// Check noise level
	sum := float32(0)
	for _, v := range buffer {
		sum += v * v // RMS calculation
	}
	rms := float32(math.Sqrt(float64(sum / float32(len(buffer)))))

	// With mix of 0.1, RMS should be roughly 0.1 * 0.577 (RMS of uniform noise)
	expectedRMS := float32(0.1 * 0.577)
	if math.Abs(float64(rms-expectedRMS)) > 0.05 {
		t.Errorf("Unexpected noise level, RMS: %f, expected ~%f", rms, expectedRMS)
	}

	adapter.Reset()
}

func TestSimpleChain(t *testing.T) {
	sampleRate := 48000.0
	chain, err := CreateSimpleChain(sampleRate)

	if err != nil {
		t.Fatalf("Failed to create simple chain: %v", err)
	}

	if chain.Count() != 1 {
		t.Errorf("Expected 1 processor in simple chain, got %d", chain.Count())
	}

	// Test processing
	buffer := make([]float32, 512)
	for i := range buffer {
		buffer[i] = 0.5 // DC offset
	}

	chain.Process(buffer)
	if buffer[0] != 0.5 || buffer[511] >= 0.5 {
		t.Fatalf("DC blocker did not start removing the offset: %v .. %v", buffer[0], buffer[511])
	}
	chain.Reset()
}

func TestDCBlockerAdapterKeepsChannelsApart(t *testing.T) {
	stereo := NewDCBlockerAdapter(48000)
	mono := NewDCBlockerAdapter(48000)
	left := make([]float32, 256)
	right := make([]float32, 256)
	ref := make([]float32, 256)
	for i := range left {
		left[i], right[i], ref[i] = 0.5, 0, 0.5
	}
	stereo.ProcessStereo(left, right)
	mono.Process(ref)
	for i := range left {
		if left[i] != ref[i] || right[i] != 0 {
			t.Fatalf("sample %d: left %v (want %v), right %v (want 0)", i, left[i], ref[i], right[i])
		}
	}
}

func TestDynamicsChain(t *testing.T) {
	sampleRate := 48000.0
	chain, err := CreateDynamicsChain(sampleRate)

	if err != nil {
		t.Fatalf("Failed to create dynamics chain: %v", err)
	}

	if chain.Count() != 2 {
		t.Errorf("Expected 2 processors in dynamics chain, got %d", chain.Count())
	}

	// Test processing
	buffer := make([]float32, 512)
	for i := range buffer {
		buffer[i] = float32(i%100)/100.0 - 0.5
	}

	chain.Process(buffer)

	// Below the -40 dB gate threshold the signal is gated by 60 dB.
	quiet := make([]float32, 512)
	for i := range quiet {
		quiet[i] = 0.001
	}
	chain.Reset()
	chain.Process(quiet)
	if db := 20 * math.Log10(float64(quiet[511])/0.001); math.Abs(db+60) > 0.5 {
		t.Fatalf("quiet signal at %.2f dB, want -60", db)
	}
}
