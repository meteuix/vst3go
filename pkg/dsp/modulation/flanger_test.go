package modulation

import (
	"math"
	"testing"
)

func TestFlangerCreation(t *testing.T) {
	sampleRate := 48000.0
	flanger := NewFlanger(sampleRate)

	if flanger == nil {
		t.Fatal("Failed to create flanger")
	}

	if flanger.sampleRate != sampleRate {
		t.Errorf("Sample rate mismatch: got %f, want %f", flanger.sampleRate, sampleRate)
	}

	// Check defaults
	if flanger.rate != 0.5 {
		t.Errorf("Default rate incorrect: got %f, want 0.5", flanger.rate)
	}

	if flanger.delay != 5.0 {
		t.Errorf("Default delay incorrect: got %f, want 5.0", flanger.delay)
	}

	if flanger.feedback != 0.5 {
		t.Errorf("Default feedback incorrect: got %f, want 0.5", flanger.feedback)
	}
}

func TestFlangerDrySignal(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(0.0) // Completely dry

	input := float32(0.5)
	output := flanger.Process(input)

	// Should be unchanged
	if math.Abs(float64(output-input)) > 0.001 {
		t.Errorf("Dry signal altered: input %f, output %f", input, output)
	}
}

func TestFlangerDelayTime(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)      // Completely wet
	flanger.SetDelay(5.0)    // 5ms delay
	flanger.SetDepth(0.0)    // No modulation
	flanger.SetFeedback(0.0) // No feedback

	// Send impulse
	impulse := float32(1.0)
	output1 := flanger.Process(impulse)

	// First output should be near zero (signal is delayed)
	if math.Abs(float64(output1)) > 0.1 {
		t.Errorf("Wet signal not delayed: %f", output1)
	}

	// Process silence for delay time
	delaySamples := int(5.0 * 48000.0 / 1000.0)
	var output float32

	for i := 0; i < delaySamples-1; i++ {
		output = flanger.Process(0.0)
	}

	// Should now hear the delayed impulse
	output = flanger.Process(0.0)
	if output < 0.5 {
		t.Errorf("Delayed signal not appearing: %f", output)
	}
}

func TestFlangerModulation(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)
	flanger.SetRate(2.0)     // 2Hz modulation
	flanger.SetDepth(3.0)    // 3ms depth
	flanger.SetDelay(5.0)    // 5ms center delay
	flanger.SetFeedback(0.0) // No feedback for cleaner test

	// Process a constant signal
	samples := 24000 // 0.5 second (one full LFO cycle at 2Hz)
	minDelay := 1000.0
	maxDelay := 0.0

	// Skip initial samples to let delay line fill
	for i := 0; i < 1000; i++ {
		flanger.Process(0.5)
	}

	// Measure delay variations by sending impulses
	for i := 0; i < samples; i++ {
		// Send impulse every 1000 samples
		if i%1000 == 0 {
			flanger.Process(1.0)

			// Find where impulse appears
			for j := 0; j < 500; j++ {
				output := flanger.Process(0.0)
				if output > 0.3 {
					// Found delayed impulse
					delayMs := float64(j) * 1000.0 / 48000.0
					if delayMs < minDelay {
						minDelay = delayMs
					}
					if delayMs > maxDelay {
						maxDelay = delayMs
					}
					break
				}
			}
		} else {
			flanger.Process(0.0)
		}
	}

	// Should see delay variation around center +/- depth
	expectedMin := 5.0 - 3.0 // 2ms
	expectedMax := 5.0 + 3.0 // 8ms

	if minDelay > expectedMin+1.0 || maxDelay < expectedMax-1.0 {
		t.Errorf("Modulation range incorrect: min=%f, max=%f (expected ~%f to ~%f)",
			minDelay, maxDelay, expectedMin, expectedMax)
	}
}

func TestFlangerFeedback(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)
	flanger.SetFeedback(0.8) // High positive feedback
	flanger.SetDelay(2.0)    // Short delay
	flanger.SetDepth(0.0)    // No modulation

	// Send impulse
	flanger.Process(1.0)

	// Collect output energy over time
	delaySamples := int(2.0 * 48000.0 / 1000.0)
	totalEnergy := float64(0)

	for i := 0; i < delaySamples*20; i++ {
		output := flanger.Process(0.0)
		totalEnergy += math.Abs(float64(output))
	}

	// Reset and test with no feedback
	flanger.Reset()
	flanger.SetFeedback(0.0)

	flanger.Process(1.0)
	totalEnergyNoFeedback := float64(0)

	for i := 0; i < delaySamples*20; i++ {
		output := flanger.Process(0.0)
		totalEnergyNoFeedback += math.Abs(float64(output))
	}

	// With feedback should have much more energy
	if totalEnergy < totalEnergyNoFeedback*2 {
		t.Error("Feedback not increasing output energy sufficiently")
	}
}

func TestFlangerNegativeFeedback(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)
	flanger.SetDelay(1.0) // Very short for comb filtering
	flanger.SetDepth(0.0) // No modulation

	// Test positive feedback
	flanger.SetFeedback(0.9)

	// Generate test tone
	freq := 1000.0 // 1kHz
	samples := 480 // 10ms
	outputPos := make([]float32, samples)

	for i := 0; i < samples; i++ {
		input := float32(math.Sin(2.0 * math.Pi * freq * float64(i) / 48000.0))
		outputPos[i] = flanger.Process(input)
	}

	// Reset and test negative feedback
	flanger.Reset()
	flanger.SetFeedback(-0.9)

	outputNeg := make([]float32, samples)
	for i := 0; i < samples; i++ {
		input := float32(math.Sin(2.0 * math.Pi * freq * float64(i) / 48000.0))
		outputNeg[i] = flanger.Process(input)
	}

	// The spectral characteristics should be different
	// Calculate RMS of both
	rmsPos := float64(0)
	rmsNeg := float64(0)

	for i := samples / 2; i < samples; i++ { // Skip transient
		rmsPos += float64(outputPos[i] * outputPos[i])
		rmsNeg += float64(outputNeg[i] * outputNeg[i])
	}

	rmsPos = math.Sqrt(rmsPos / float64(samples/2))
	rmsNeg = math.Sqrt(rmsNeg / float64(samples/2))

	// They should be different (due to different comb filter response)
	if math.Abs(rmsPos-rmsNeg) < 0.01 {
		t.Error("Positive and negative feedback producing identical results")
	}
}

func TestFlangerManualMode(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)
	flanger.SetManualMode(true)
	flanger.SetDelay(5.0)
	flanger.SetDepth(3.0)
	flanger.SetFeedback(0.0)

	// Test manual at minimum (0.0)
	flanger.SetManual(0.0)

	// Send impulse and measure delay
	flanger.Process(1.0)

	// Count samples until impulse appears
	delayCount := 0
	for i := 0; i < 1000; i++ {
		output := flanger.Process(0.0)
		if output > 0.5 {
			delayCount = i
			break
		}
	}

	// Should be close to minimum delay (5-3=2ms)
	expectedSamples := int(2.0 * 48000.0 / 1000.0)
	if math.Abs(float64(delayCount-expectedSamples)) > 10 {
		t.Errorf("Manual mode minimum delay incorrect: %d samples (expected ~%d)",
			delayCount, expectedSamples)
	}

	// Reset and test maximum
	flanger.Reset()
	flanger.SetManual(1.0)

	flanger.Process(1.0)
	delayCount = 0
	for i := 0; i < 1000; i++ {
		output := flanger.Process(0.0)
		if output > 0.5 {
			delayCount = i
			break
		}
	}

	// Should be close to maximum delay (5+3=8ms)
	expectedSamples = int(8.0 * 48000.0 / 1000.0)
	if math.Abs(float64(delayCount-expectedSamples)) > 10 {
		t.Errorf("Manual mode maximum delay incorrect: %d samples (expected ~%d)",
			delayCount, expectedSamples)
	}
}

func TestFlangerStereo(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetMix(1.0)
	flanger.SetDelay(5.0)
	flanger.SetDepth(4.0)
	flanger.SetFeedback(0.0)
	flanger.SetManualMode(true)
	flanger.SetManual(1.0) // 5 + 4 = 9 ms

	firstL, firstR := -1, -1
	for i := range 1000 {
		in := float32(0)
		if i == 0 {
			in = 1
		}
		l, r := flanger.ProcessStereo(in, in)
		if firstL < 0 && l > 0.1 {
			firstL = i
		}
		if firstR < 0 && r > 0.1 {
			firstR = i
		}
	}
	// Manual mode keeps both channels at the manual position.
	if firstL != 432 || firstR != 432 {
		t.Fatalf("manual mode: left at %d, right at %d, want 9 ms = 432", firstL, firstR)
	}

	flanger.Reset()
	flanger.SetManualMode(false)
	flanger.SetRate(1)
	var diff float64
	for i := range 48000 {
		x := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		l, r := flanger.ProcessStereo(x, x)
		diff += math.Abs(float64(l - r))
	}
	if diff/48000 < 0.05 {
		t.Errorf("inverted sweep left the channels nearly identical: mean difference %f", diff/48000)
	}

	// Each channel flanges its own input.
	flanger.Reset()
	for range 1000 {
		if l, r := flanger.ProcessStereo(0, 0.5); l != 0 || r < 0 {
			t.Fatalf("silent left input produced %v (right %v)", l, r)
		}
	}
}

func TestFlangerSettersDoNotAllocate(t *testing.T) {
	flanger := NewFlanger(48000.0)
	allocs := testing.AllocsPerRun(100, func() {
		flanger.SetDelay(8)
		flanger.SetDepth(9)
		flanger.ProcessStereo(0.5, 0.5)
		flanger.SetDelay(2)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestFlangerReset(t *testing.T) {
	flanger := NewFlanger(48000.0)
	flanger.SetFeedback(0.9) // High feedback

	// Process signal to build up feedback
	for i := 0; i < 1000; i++ {
		flanger.Process(0.5)
	}

	// Reset
	flanger.Reset()

	// Output should be clean
	flanger.SetMix(1.0)
	output := flanger.Process(0.0)

	if math.Abs(float64(output)) > 0.001 {
		t.Errorf("Flanger not silent after reset: %f", output)
	}
}

func TestFlangerParameterLimits(t *testing.T) {
	flanger := NewFlanger(48000.0)

	// Test rate limits
	flanger.SetRate(-1.0)
	if flanger.rate < 0.01 {
		t.Errorf("Rate below minimum: %f", flanger.rate)
	}

	flanger.SetRate(100.0)
	if flanger.rate > 20.0 {
		t.Errorf("Rate above maximum: %f", flanger.rate)
	}

	// Test feedback limits
	flanger.SetFeedback(2.0)
	if flanger.feedback > 0.99 {
		t.Errorf("Feedback above maximum: %f", flanger.feedback)
	}

	flanger.SetFeedback(-2.0)
	if flanger.feedback < -0.99 {
		t.Errorf("Feedback below minimum: %f", flanger.feedback)
	}

	// Test delay limits
	flanger.SetDelay(0.01)
	if flanger.delay < 0.1 {
		t.Errorf("Delay below minimum: %f", flanger.delay)
	}

	flanger.SetDelay(50.0)
	if flanger.delay > 10.0 {
		t.Errorf("Delay above maximum: %f", flanger.delay)
	}
}

// Benchmark flanger
func BenchmarkFlanger(b *testing.B) {
	flanger := NewFlanger(48000.0)
	flanger.SetRate(0.5)
	flanger.SetFeedback(0.7)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = flanger.Process(0.5)
	}
}

func BenchmarkFlangerStereo(b *testing.B) {
	flanger := NewFlanger(48000.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		flanger.ProcessStereo(0.5, 0.5)
	}
}
