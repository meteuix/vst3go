package envelope

import (
	"math"
	"testing"
)

func TestDetectorCreation(t *testing.T) {
	sampleRate := 48000.0

	// Test creating detectors with different modes
	modes := []DetectorMode{ModePeak, ModeRMS, ModePeakHold}
	for _, mode := range modes {
		d := NewDetector(sampleRate, mode)
		if d == nil {
			t.Errorf("Failed to create detector with mode %d", mode)
		}
		if d.sampleRate != sampleRate {
			t.Errorf("Sample rate mismatch: got %f, want %f", d.sampleRate, sampleRate)
		}
		if d.mode != mode {
			t.Errorf("Mode mismatch: got %d, want %d", d.mode, mode)
		}
	}
}

func TestDetectorPeakMode(t *testing.T) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModePeak)
	d.SetAttack(0.0001) // 0.1 ms: 4.8 samples
	d.SetRelease(0.010) // 10 ms

	// A 1 ms burst from sample 100.
	signal := make([]float32, 1000)
	output := make([]float32, 1000)
	for i := 100; i < 148; i++ {
		signal[i] = 1.0
	}
	d.Process(signal, output)

	// The one-pole attack reaches 90% after ln(10) = 2.3 time constants.
	if output[100] >= 0.9 || output[100+12] < 0.9 {
		t.Errorf("attack: %f at onset, %f after 12 samples; want 2.3 time constants to 90%%", output[100], output[112])
	}
	// e^(-852/480) = 0.17 left after the release.
	if output[999] > 0.2 {
		t.Errorf("Peak detector envelope did not decay properly: %f", output[999])
	}
}

func TestDetectorPeakModeHonorsSlowAttack(t *testing.T) {
	d := NewDetector(48000, ModePeak)
	d.SetAttack(0.010) // 480 samples

	// A transient far above the envelope must not bypass the attack time:
	// a slow attack is what lets a compressor pass transients through.
	if env := d.Detect(1.0); env > 0.01 {
		t.Fatalf("a single full-scale sample pushed the envelope to %f", env)
	}
	for range 479 {
		d.Detect(1.0)
	}
	if env := d.GetEnvelope(); math.Abs(float64(env)-(1-math.Exp(-1))) > 0.01 {
		t.Fatalf("after one attack time the envelope is %f, want 1-1/e", env)
	}
}

func TestDetectorRMSMode(t *testing.T) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModeRMS)
	d.SetRMSWindow(3.0) // 3ms window

	// Test with a sine wave
	freq := 1000.0
	duration := 0.01 // 10ms
	numSamples := int(sampleRate * duration)

	signal := make([]float32, numSamples)
	output := make([]float32, numSamples)

	// Generate sine wave
	for i := 0; i < numSamples; i++ {
		signal[i] = float32(math.Sin(2.0 * math.Pi * freq * float64(i) / sampleRate))
	}

	// Process the signal
	d.Process(signal, output)

	// RMS of a sine wave should be approximately 0.707
	expectedRMS := float32(0.707)
	tolerance := float32(0.1)

	// Check last few samples (after RMS window fills)
	avgRMS := float32(0)
	count := 0
	for i := numSamples - 100; i < numSamples; i++ {
		avgRMS += output[i]
		count++
	}
	avgRMS /= float32(count)

	if math.Abs(float64(avgRMS-expectedRMS)) > float64(tolerance) {
		t.Errorf("RMS detector error: got %f, expected %f (±%f)", avgRMS, expectedRMS, tolerance)
	}
}

func TestDetectorTypes(t *testing.T) {
	sampleRate := 48000.0
	types := []DetectorType{TypeLinear, TypeLogarithmic, TypeAnalog}

	for _, detType := range types {
		d := NewDetector(sampleRate, ModePeak)
		d.SetType(detType)
		d.SetAttack(0.001)
		d.SetRelease(0.010)

		// Simple impulse test
		output := d.Detect(1.0)
		if output <= 0 {
			t.Errorf("Detector type %d failed to respond to impulse", detType)
		}

		// Check that envelope decays over time
		// Process 2000 samples (about 40ms at 48kHz) with 10ms release
		for i := 0; i < 2000; i++ {
			output = d.Detect(0.0)
		}

		// After 40ms with 10ms release, should be well decayed
		if output > 0.05 {
			t.Errorf("Detector type %d did not decay properly: %f", detType, output)
		}
	}
}

func TestDetectorPeakHold(t *testing.T) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModePeakHold)
	d.SetHold(0.005) // 5ms hold time
	d.SetRelease(0.010)

	// Let the envelope reach the input level (1 ms attack).
	var output float32
	for range 480 {
		output = d.Detect(1.0)
	}

	// Check that peak is held
	holdSamples := int(0.005 * sampleRate)
	for i := 0; i < holdSamples; i++ {
		output = d.Detect(0.0)
		if output < 0.99 {
			t.Errorf("Peak hold failed at sample %d: got %f, expected ~1.0", i, output)
			break
		}
	}
}

func TestDetectorReset(t *testing.T) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModePeak)

	// Process some signal
	d.Detect(1.0)

	// Reset
	d.Reset()

	// Check that envelope is zero
	if d.GetEnvelope() != 0 {
		t.Errorf("Detector reset failed: envelope = %f, expected 0", d.GetEnvelope())
	}
}

func TestDetectorDB(t *testing.T) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModePeak)

	// Test some known values
	testCases := []struct {
		input      float32
		expectedDB float32
		tolerance  float32
	}{
		{1.0, 0.0, 0.1},   // 0 dB
		{0.5, -6.0, 0.5},  // -6 dB
		{0.1, -20.0, 0.5}, // -20 dB
		{0.0, -96.0, 0.1}, // Minimum
	}

	for _, tc := range testCases {
		d.envelope = float64(tc.input)
		db := d.GetEnvelopeDB()
		if math.Abs(float64(db-tc.expectedDB)) > float64(tc.tolerance) {
			t.Errorf("DB conversion error for input %f: got %f dB, expected %f dB (±%f)",
				tc.input, db, tc.expectedDB, tc.tolerance)
		}
	}
}

// Benchmark envelope detection
func BenchmarkDetectorPeak(b *testing.B) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModePeak)
	signal := make([]float32, 1024)
	output := make([]float32, 1024)

	// Fill with noise
	for i := range signal {
		signal[i] = float32(math.Sin(float64(i) * 0.1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Process(signal, output)
	}
}

func BenchmarkDetectorRMS(b *testing.B) {
	sampleRate := 48000.0
	d := NewDetector(sampleRate, ModeRMS)
	signal := make([]float32, 1024)
	output := make([]float32, 1024)

	// Fill with noise
	for i := range signal {
		signal[i] = float32(math.Sin(float64(i) * 0.1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Process(signal, output)
	}
}

func TestDetectorSettersDoNotAllocate(t *testing.T) {
	d := NewDetector(48000, ModePeak)
	allocs := testing.AllocsPerRun(50, func() {
		d.SetMode(ModeRMS)
		d.SetRMSWindow(50)
		d.Detect(0.5)
		d.SetRMSWindow(5)
		d.SetMode(ModePeak)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestDetectorRMSWindowMeasuresItsLength(t *testing.T) {
	d := NewDetector(48000, ModeRMS)
	d.SetAttack(0.0001)
	d.SetRMSWindow(10) // 480 samples
	// A square wave of ±0.5 has RMS 0.5 over any whole number of periods.
	var env float32
	for i := range 4800 {
		x := float32(0.5)
		if i/24%2 == 1 {
			x = -0.5
		}
		env = d.Detect(x)
	}
	if math.Abs(float64(env)-0.5) > 1e-3 {
		t.Fatalf("RMS %v, want 0.5", env)
	}
	// Longer than MaxRMSWindow is clamped, not grown.
	d.SetRMSWindow(5000)
	if d.rmsWindowLen != 48000 {
		t.Fatalf("window %d samples, want the 1 s maximum", d.rmsWindowLen)
	}
}
