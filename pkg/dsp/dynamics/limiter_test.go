package dynamics

import (
	"math"
	"math/rand"
	"testing"
)

func TestLimiterCreation(t *testing.T) {
	sampleRate := 48000.0
	l := NewLimiter(sampleRate)

	if l == nil {
		t.Fatal("Failed to create limiter")
	}

	if l.sampleRate != sampleRate {
		t.Errorf("Sample rate mismatch: got %f, want %f", l.sampleRate, sampleRate)
	}

	// Check defaults
	if l.threshold != -0.3 {
		t.Errorf("Default threshold incorrect: got %f, want -0.3", l.threshold)
	}

	if !l.truePeak {
		t.Error("True peak detection should be enabled by default")
	}
}

func TestLimiterBrickWall(t *testing.T) {
	sampleRate := 48000.0
	l := NewLimiter(sampleRate)
	l.SetThreshold(-3.0) // -3dB ceiling
	l.SetLookahead(0.0)  // No lookahead for simple test
	l.SetTruePeak(false) // Disable true peak for predictable behavior

	// Test signals at various levels
	testCases := []struct {
		inputDB    float64
		expectedDB float64
		tolerance  float64
	}{
		{-10.0, -10.0, 0.1}, // Below threshold, no limiting
		{-3.0, -3.0, 0.1},   // At threshold
		{0.0, -3.0, 0.5},    // Above threshold, should be limited to -3dB
		{6.0, -3.0, 0.5},    // Well above threshold
	}

	for _, tc := range testCases {
		// Convert dB to linear
		input := float32(math.Pow(10.0, tc.inputDB/20.0))

		// Process several samples to let envelope settle
		var output float32
		for i := 0; i < 100; i++ {
			output = l.Process(input)
		}

		// Convert output to dB
		outputDB := 20.0 * math.Log10(math.Abs(float64(output)))

		if math.Abs(outputDB-tc.expectedDB) > tc.tolerance {
			t.Errorf("Limiter failed at %f dB input: got %f dB, expected %f dB",
				tc.inputDB, outputDB, tc.expectedDB)
		}
	}
}

func TestLimiterTruePeak(t *testing.T) {
	sampleRate := 48000.0
	l := NewLimiter(sampleRate)
	l.SetThreshold(-1.0)
	l.SetTruePeak(true)

	// Create a signal that has inter-sample peaks
	// Two samples at 0.8 can have a peak of ~1.0 between them
	signal := []float32{0.8, 0.8, -0.8, -0.8}
	output := make([]float32, len(signal))

	l.ProcessBuffer(signal, output)

	// Check that output doesn't exceed threshold
	for i, out := range output {
		outDB := 20.0 * math.Log10(math.Abs(float64(out))+1e-10)
		if outDB > -0.5 { // Allow small margin
			t.Errorf("True peak limiting failed at sample %d: %f dB", i, outDB)
		}
	}
}

func TestLimiterLookahead(t *testing.T) {
	sampleRate := 48000.0
	l := NewLimiter(sampleRate)
	l.SetThreshold(-6.0)
	l.SetLookahead(0.005) // 5ms lookahead
	l.SetTruePeak(false)
	l.SetRelease(0.010) // Fast release

	// Create a signal with a transient
	numSamples := 1000
	input := make([]float32, numSamples)
	output := make([]float32, numSamples)

	// Create a moderate level signal with a spike
	for i := 0; i < numSamples; i++ {
		input[i] = 0.1 // -20 dB baseline
	}

	// Add a loud transient
	transientStart := 500
	transientLength := 10
	for i := transientStart; i < transientStart+transientLength; i++ {
		input[i] = 1.0 // 0 dB spike
	}

	// Process
	l.ProcessBuffer(input, output)

	// With lookahead, the output at the spike should be limited
	// Check that the spike is properly limited
	for i := transientStart; i < transientStart+transientLength; i++ {
		outputDB := 20.0 * math.Log10(math.Abs(float64(output[i]))+1e-10)
		if outputDB > -5.5 { // Allow 0.5dB margin
			t.Errorf("Lookahead limiting failed at sample %d: %f dB", i, outputDB)
		}
	}

	// Also verify that lookahead doesn't affect signal too much before transient
	preTransientIndex := transientStart - int(0.020*sampleRate) // Well before lookahead window
	if preTransientIndex >= 0 {
		// Should be close to input level (allow for some processing variance)
		ratio := output[preTransientIndex] / input[preTransientIndex]
		if ratio < 0.95 || ratio > 1.05 {
			t.Errorf("Lookahead affecting signal too early: ratio %f", ratio)
		}
	}
}

func TestLimiterStereo(t *testing.T) {
	sampleRate := 48000.0
	l := NewLimiter(sampleRate)
	l.SetThreshold(-6.0)
	l.SetTruePeak(false)

	// Create test signals
	inputL := []float32{0.5, 0.1, 0.1}
	inputR := []float32{0.1, 0.5, 0.1}
	outputL := make([]float32, 3)
	outputR := make([]float32, 3)

	l.ProcessStereo(inputL, inputR, outputL, outputR)

	// Both channels should be limited when either exceeds
	for i := range outputL {
		if outputL[i] > 0.5 || outputR[i] > 0.5 {
			t.Errorf("Stereo limiting failed at sample %d", i)
		}
	}
}

func TestLimiterGainReduction(t *testing.T) {
	l := NewLimiter(48000.0)
	l.SetThreshold(-6.0)
	l.SetTruePeak(false)

	// Process a loud signal. The reduction ramps in over the lookahead
	// window and reports the gain applied to the outgoing sample, so it is
	// fully in once the loud signal leaves the delay line.
	input := float32(1.0) // 0 dB
	var out float32
	for i := 0; i < l.Latency()+100; i++ {
		out = l.Process(input)
	}
	if math.Abs(float64(out)-math.Pow(10, -6.0/20)) > 1e-3 {
		t.Errorf("limited output %f, want %f", out, math.Pow(10, -6.0/20))
	}

	// Should report ~6dB gain reduction
	gr := l.GetGainReduction()
	if gr < 5.5 || gr > 6.5 {
		t.Errorf("Incorrect gain reduction: got %f dB, expected ~6 dB", gr)
	}
}

func TestLimiterReset(t *testing.T) {
	l := NewLimiter(48000.0)

	l.SetThreshold(-6)

	for i := 0; i < 1000; i++ {
		l.Process(1.0)
	}
	l.Reset()

	if l.GetGainReduction() != 0 {
		t.Errorf("Gain reduction not reset: %f", l.GetGainReduction())
	}

	// Neither the delay line, the gain state nor the true peak history may
	// survive: a quiet signal after Reset passes through unchanged once the
	// lookahead delay is flushed.
	latency := l.Latency()
	for i := 0; i < latency; i++ {
		if out := l.Process(0.1); out != 0 {
			t.Fatalf("sample %d: delay line not cleared, got %f", i, out)
		}
	}
	for i := 0; i < 100; i++ {
		if out := l.Process(0.1); math.Abs(float64(out)-0.1) > 1e-6 {
			t.Fatalf("sample %d after reset: got %f, want 0.1", i, out)
		}
	}
}

func TestLimiterNeverExceedsCeiling(t *testing.T) {
	for _, lookahead := range []float64{0, 0.001, 0.005, 0.010} {
		l := NewLimiter(48000.0)
		l.SetThreshold(-1.0)
		l.SetLookahead(lookahead)
		l.SetRelease(0.020)
		ceiling := math.Pow(10, -1.0/20)

		rng := rand.New(rand.NewSource(1))
		n := 48000
		inL := make([]float32, n)
		inR := make([]float32, n)
		for i := range inL {
			// Bursts up to +12 dB over a quiet bed.
			amp := 0.05
			if (i/2000)%3 == 1 {
				amp = 4
			}
			inL[i] = float32((rng.Float64()*2 - 1) * amp)
			inR[i] = float32((rng.Float64()*2 - 1) * amp)
		}
		outL := make([]float32, n)
		outR := make([]float32, n)
		l.ProcessStereo(inL, inR, outL, outR)

		for i := range outL {
			if math.Abs(float64(outL[i])) > ceiling+1e-6 || math.Abs(float64(outR[i])) > ceiling+1e-6 {
				t.Fatalf("lookahead %g: sample %d exceeds ceiling: %f %f", lookahead, i, outL[i], outR[i])
			}
		}

		// The output is the input delayed by Latency() and scaled by a gain
		// in (0, 1], shared by both channels.
		d := l.Latency()
		if want := int(math.Round(lookahead * 48000)); d != want {
			t.Fatalf("latency %d, want %d", d, want)
		}
		for i := d; i < n; i++ {
			gL := float64(outL[i]) / float64(inL[i-d])
			gR := float64(outR[i]) / float64(inR[i-d])
			if gL <= 0 || gL > 1+1e-6 || math.Abs(gL-gR) > 1e-3 {
				t.Fatalf("lookahead %g: sample %d gains %f/%f", lookahead, i, gL, gR)
			}
		}
	}
}

func TestLimiterLookaheadRampsBeforePeak(t *testing.T) {
	l := NewLimiter(48000.0)
	l.SetThreshold(-6.0)
	l.SetLookahead(0.005)
	l.SetTruePeak(false)
	d := l.Latency()

	in := make([]float32, 2000)
	for i := range in {
		in[i] = 0.1
	}
	in[1000] = 1.0
	out := make([]float32, len(in))
	l.ProcessBuffer(in, out)

	// The single peak comes out d samples later, limited to the ceiling,
	// and the gain is already reduced on the samples just before it.
	peak := out[1000+d]
	if math.Abs(float64(peak)-math.Pow(10, -6.0/20)) > 0.01 {
		t.Fatalf("limited peak %f, want %f", peak, math.Pow(10, -6.0/20))
	}
	if out[1000+d-1] >= 0.1*0.99 {
		t.Fatalf("no gain ramp ahead of the peak: %f", out[1000+d-1])
	}
	if math.Abs(float64(out[999])-0.1) > 1e-6 {
		t.Fatalf("signal attenuated before the lookahead window: %f", out[999])
	}
}

func TestLimiterTruePeakCatchesIntersamplePeaks(t *testing.T) {
	// A sine at fs/4 sampled at 45 degrees: every sample is 0.8 but the
	// waveform peaks at 0.8*sqrt(2).
	l := NewLimiter(48000.0)
	l.SetThreshold(-1.0)
	l.SetLookahead(0.001)
	in := make([]float32, 4000)
	for i := range in {
		in[i] = float32(1.1314 * math.Sin(math.Pi/2*float64(i)+math.Pi/4))
	}
	out := make([]float32, len(in))
	l.ProcessBuffer(in, out)

	// Reconstruct the steady-state waveform amplitude from the output
	// samples: a 45-degree-sampled sine has peak = |sample| * sqrt(2).
	amp := math.Abs(float64(out[3000])) * math.Sqrt2
	if amp > math.Pow(10, -1.0/20)*1.05 {
		t.Fatalf("true peak amplitude %f exceeds the -1 dB ceiling", amp)
	}
}

func TestLimiterDoesNotAllocate(t *testing.T) {
	l := NewLimiter(48000.0)
	in := make([]float32, 256)
	for i := range in {
		in[i] = float32(math.Sin(float64(i) * 0.1))
	}
	out := make([]float32, 256)
	out2 := make([]float32, 256)
	if n := testing.AllocsPerRun(50, func() {
		l.ProcessBuffer(in, out)
		l.ProcessStereo(in, in, out, out2)
		l.SetLookahead(0.002)
		l.SetLookahead(0.008)
	}); n != 0 {
		t.Fatalf("%v allocations", n)
	}
}

// Benchmark limiter
func BenchmarkLimiter(b *testing.B) {
	l := NewLimiter(48000.0)
	input := float32(0.8)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Process(input)
	}
}

func BenchmarkLimiterTruePeak(b *testing.B) {
	l := NewLimiter(48000.0)
	l.SetTruePeak(true)
	input := float32(0.8)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Process(input)
	}
}
