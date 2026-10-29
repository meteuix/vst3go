package distortion

import (
	"math"
	"testing"
)

func TestBitcrusher(t *testing.T) {
	sampleRate := 44100.0
	bc := NewBitcrusher(sampleRate)

	t.Run("BitDepth", func(t *testing.T) {
		bc.Reset()
		bc.SetMix(1.0)
		bc.SetSampleRateReduction(1.0) // No sample rate reduction
		bc.SetDither(DitherNone)

		// Test with 1-bit (extreme crushing)
		bc.SetBitDepth(1.0)
		result := bc.Process(0.5)

		// With 1-bit, output should be either -1 or 1
		if math.Abs(result) < 0.9 {
			t.Errorf("1-bit crushing should produce extreme quantization, got %f", result)
		}

		// Test with 16-bit (minimal crushing)
		bc.SetBitDepth(16.0)
		input := 0.12345
		result = bc.Process(input)

		// Should be close to input but quantized
		if math.Abs(result-input) > 0.001 {
			t.Errorf("16-bit should have minimal quantization, got %f from %f", result, input)
		}
	})

	t.Run("SampleRateReduction", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(32.0)           // No bit reduction
		bc.SetSampleRateReduction(4.0) // Reduce by factor of 4
		bc.SetAntiAlias(false)

		// Process several samples
		results := make([]float64, 8)
		for i := range results {
			results[i] = bc.Process(float64(i) * 0.1)
		}

		// Check that sample and hold is working
		// Every 4 samples should be the same
		if results[0] != results[1] || results[0] != results[2] || results[0] != results[3] {
			t.Errorf("Sample rate reduction should hold samples, got %v", results[:4])
		}

		// But sample 4 should be different from sample 0
		if results[4] == results[0] {
			t.Errorf("Sample rate reduction should update after hold period")
		}
	})

	t.Run("AntiAlias", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(32.0)
		bc.SetSampleRateReduction(10.0)

		// Process several samples to let filter settle
		// Without anti-aliasing
		bc.SetAntiAlias(false)
		var withoutAA float64
		for i := 0; i < 10; i++ {
			withoutAA = bc.Process(0.9)
		}

		// With anti-aliasing
		bc.Reset()
		bc.SetAntiAlias(true)
		var withAA float64
		for i := 0; i < 10; i++ {
			withAA = bc.Process(0.9)
		}

		// Anti-aliasing filter should have an effect
		if math.Abs(withAA-withoutAA) < 0.01 {
			t.Errorf("Anti-aliasing should affect the output, got %f vs %f", withAA, withoutAA)
		}
	})

	t.Run("Dither", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(8.0) // Low bit depth to make dither effect visible
		bc.SetSampleRateReduction(1.0)

		// A level a third of the way between two 8-bit steps.
		step := 2.0 / 256
		input := 0.5 + step/3

		// No dither: always the nearest step.
		bc.SetDither(DitherNone)
		for range 10 {
			if out := bc.Process(input); out != 0.5 {
				t.Fatalf("without dither %v quantized to %v, want 0.5", input, out)
			}
		}

		// Dither turns the error into noise: on average the output is the
		// input, and it never strays further than the dither's width.
		for _, d := range []struct {
			dither DitherType
			width  float64 // in steps
		}{{DitherWhite, 1}, {DitherTriangular, 2}} {
			bc.SetDither(d.dither)
			sum := 0.0
			n := 100000
			for range n {
				out := bc.Process(input)
				if math.Abs(out-input) > (d.width/2+0.5)*step {
					t.Fatalf("dither %d: output %v is too far from %v", d.dither, out, input)
				}
				sum += out
			}
			if mean := sum / float64(n); math.Abs(mean-input) > step/50 {
				t.Errorf("dither %d: mean output %v, want %v", d.dither, mean, input)
			}
		}
		bc.SetDither(DitherNone)
	})

	t.Run("Mix", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(4.0) // Heavy crushing
		bc.SetSampleRateReduction(1.0)

		input := 0.654321

		// Full dry
		bc.SetMix(0.0)
		dry := bc.Process(input)

		if math.Abs(dry-input) > 1e-9 {
			t.Errorf("With mix=0, output should equal input, got %f from %f", dry, input)
		}

		// Full wet
		bc.SetMix(1.0)
		wet := bc.Process(input)

		// 50% mix
		bc.SetMix(0.5)
		mixed := bc.Process(input)

		// Should be between dry and wet
		if mixed <= math.Min(dry, wet)-0.01 || mixed >= math.Max(dry, wet)+0.01 {
			t.Errorf("50%% mix should be between dry (%f) and wet (%f) signals, got %f", dry, wet, mixed)
		}
	})

	t.Run("ProcessBlock", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(8.0)
		bc.SetSampleRateReduction(2.0)

		input := []float64{-0.8, -0.4, 0.0, 0.4, 0.8}
		output := make([]float64, len(input))

		bc.ProcessBlock(input, output)

		// Verify processing occurred
		processed := false
		for i, v := range output {
			if v != input[i] {
				processed = true
				break
			}
		}

		if !processed {
			t.Errorf("ProcessBlock should modify the input")
		}
	})

	t.Run("Stereo", func(t *testing.T) {
		bc.Reset()
		bc.SetBitDepth(6.0)
		bc.SetSampleRateReduction(3.0)
		bc.SetDither(DitherWhite)

		// Same input for both channels
		input := []float64{0.5, 0.5, 0.5, 0.5, 0.5}
		outputL := make([]float64, len(input))
		outputR := make([]float64, len(input))

		bc.ProcessStereo(input, input, outputL, outputR)

		// With dither, channels should differ slightly
		different := false
		for i := range outputL {
			if outputL[i] != outputR[i] {
				different = true
				break
			}
		}

		if !different {
			t.Errorf("With dither, stereo channels should differ")
		}
	})
}

func TestBitcrusherUtilityFunctions(t *testing.T) {
	t.Run("QuantizeToSteps", func(t *testing.T) {
		// Test 2 steps (binary)
		result := QuantizeToSteps(0.5, 2)
		if result != 1.0 {
			t.Errorf("QuantizeToSteps(0.5, 2) should be 1.0, got %f", result)
		}

		result = QuantizeToSteps(-0.5, 2)
		if result != -1.0 {
			t.Errorf("QuantizeToSteps(-0.5, 2) should be -1.0, got %f", result)
		}

		// Test 4 steps
		result = QuantizeToSteps(0.0, 4)
		if math.Abs(result) > 0.4 {
			t.Errorf("QuantizeToSteps(0.0, 4) should be near 0, got %f", result)
		}
	})

	t.Run("FoldbackDistortion", func(t *testing.T) {
		threshold := 0.5

		// Within threshold - no change
		result := FoldbackDistortion(0.3, threshold)
		if result != 0.3 {
			t.Errorf("Within threshold should not change, got %f", result)
		}

		// Above threshold - should fold back
		result = FoldbackDistortion(0.7, threshold)
		if result >= threshold || result <= -threshold {
			t.Errorf("Above threshold should fold back, got %f", result)
		}
	})

	t.Run("GateEffect", func(t *testing.T) {
		threshold := 0.1

		// Below threshold - should gate to zero
		result := GateEffect(0.05, threshold)
		if result != 0.0 {
			t.Errorf("Below threshold should gate to zero, got %f", result)
		}

		// Above threshold - should pass through
		result = GateEffect(0.5, threshold)
		if result != 0.5 {
			t.Errorf("Above threshold should pass through, got %f", result)
		}
	})
}

func BenchmarkBitcrusher(b *testing.B) {
	bc := NewBitcrusher(44100.0)
	bc.SetBitDepth(8.0)
	bc.SetSampleRateReduction(4.0)
	bc.SetAntiAlias(true)
	bc.SetDither(DitherTriangular)

	input := make([]float64, 512)
	output := make([]float64, 512)

	for i := range input {
		input[i] = math.Sin(2.0 * math.Pi * float64(i) / 512.0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bc.ProcessBlock(input, output)
	}
}

func TestBitcrusherSampleRateReductionPerChannel(t *testing.T) {
	bc := NewBitcrusher(48000)
	bc.SetBitDepth(32)
	bc.SetAntiAlias(false)
	bc.SetSampleRateReduction(2.5)

	n := 20
	inL, inR := make([]float64, n), make([]float64, n)
	for i := range inL {
		inL[i], inR[i] = float64(i), -float64(i)
	}
	outL, outR := make([]float64, n), make([]float64, n)
	bc.ProcessStereo(inL, inR, outL, outR)

	// A 2.5x reduction takes samples 0, 3, 5, 8, 10, ... (every 2.5 on average).
	want := []float64{0, 0, 0, 3, 3, 5, 5, 5, 8, 8, 10, 10, 10, 13, 13, 15, 15, 15, 18, 18}
	for i := range want {
		if outL[i] != want[i] || outR[i] != -want[i] {
			t.Fatalf("sample %d: L %v R %v, want %v and %v", i, outL[i], outR[i], want[i], -want[i])
		}
	}
}
