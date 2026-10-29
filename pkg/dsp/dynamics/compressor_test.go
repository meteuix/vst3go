package dynamics

import (
	"math"
	"testing"
)

func TestCompressorCreation(t *testing.T) {
	sampleRate := 48000.0
	c := NewCompressor(sampleRate)

	if c == nil {
		t.Fatal("Failed to create compressor")
	}

	if c.sampleRate != sampleRate {
		t.Errorf("Sample rate mismatch: got %f, want %f", c.sampleRate, sampleRate)
	}

	// Check default values
	if c.threshold != -20.0 {
		t.Errorf("Default threshold incorrect: got %f, want -20.0", c.threshold)
	}

	if c.ratio != 4.0 {
		t.Errorf("Default ratio incorrect: got %f, want 4.0", c.ratio)
	}
}

func TestCompressorGainComputation(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-20.0)
	c.SetRatio(4.0)
	c.SetKnee(KneeHard, 0.0)

	testCases := []struct {
		inputDB    float64
		expectedGR float64 // Expected gain reduction
		tolerance  float64
	}{
		{-30.0, 0.0, 0.001}, // Below threshold
		{-20.0, 0.0, 0.001}, // At threshold
		{-10.0, 7.5, 0.001}, // 10dB over threshold, 4:1 ratio -> 7.5dB reduction
		{0.0, 15.0, 0.001},  // 20dB over threshold -> 15dB reduction
	}

	for _, tc := range testCases {
		gr := c.computeGain(tc.inputDB)
		if math.Abs(gr-tc.expectedGR) > tc.tolerance {
			t.Errorf("Gain computation error at %f dB: got %f dB reduction, expected %f dB",
				tc.inputDB, gr, tc.expectedGR)
		}
	}
}

func TestCompressorSoftKnee(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-20.0)
	c.SetRatio(4.0)
	c.SetKnee(KneeSoft, 6.0) // 6dB soft knee

	// The soft knee is the quadratic tangent to both hard-knee segments:
	// GR = (1-1/R) * (x-T+W/2)^2 / (2W). It starts compressing below the
	// threshold and, because the static curve bends smoothly under the hard
	// corner, reduces slightly more than the hard knee inside the knee.
	inputDB := -18.0 // 2dB above threshold, inside the 6dB knee
	gr := c.computeGain(inputDB)
	if want := 0.75 * 5 * 5 / 12; math.Abs(gr-want) > 1e-9 {
		t.Errorf("Soft knee at %f dB: got %f dB reduction, want %f", inputDB, gr, want)
	}

	// Below the threshold but inside the knee compression has already begun
	if gr := c.computeGain(-21.0); gr <= 0 || gr >= 0.75*1 {
		t.Errorf("Soft knee below threshold: got %f dB reduction", gr)
	}

	// Bottom of the knee: no reduction
	if gr := c.computeGain(-23.0); gr != 0 {
		t.Errorf("Soft knee bottom: got %f dB reduction, want 0", gr)
	}

	// Test at knee boundary
	inputDB = -17.0 // At top of knee region (threshold + knee/2)
	gr = c.computeGain(inputDB)
	expectedGR := 3.0 * (1.0 - 1.0/4.0) // Should match hard knee at boundary
	if math.Abs(gr-expectedGR) > 0.1 {
		t.Errorf("Soft knee boundary incorrect: got %f dB reduction, expected %f", gr, expectedGR)
	}
}

func TestCompressorProcessing(t *testing.T) {
	sampleRate := 48000.0
	c := NewCompressor(sampleRate)
	c.SetThreshold(-20.0)
	c.SetRatio(4.0)
	c.SetAttack(0.001)  // 1ms
	c.SetRelease(0.010) // 10ms

	// Generate test signal: sine wave that exceeds threshold
	duration := 0.1 // 100ms
	numSamples := int(sampleRate * duration)
	input := make([]float32, numSamples)
	output := make([]float32, numSamples)

	// Generate loud sine wave (0 dB peak)
	freq := 1000.0
	for i := 0; i < numSamples; i++ {
		input[i] = float32(math.Sin(2.0 * math.Pi * freq * float64(i) / sampleRate))
	}

	// Process
	c.ProcessBuffer(input, output)

	// Check that output is compressed (lower than input)
	// After attack time, should see compression
	attackSamples := int(0.002 * sampleRate) // Check after 2ms

	inputRMS := float32(0.0)
	outputRMS := float32(0.0)
	count := 0

	// Calculate RMS after attack time
	for i := attackSamples; i < numSamples/2; i++ {
		inputRMS += input[i] * input[i]
		outputRMS += output[i] * output[i]
		count++
	}

	inputRMS = float32(math.Sqrt(float64(inputRMS / float32(count))))
	outputRMS = float32(math.Sqrt(float64(outputRMS / float32(count))))

	// Output should be compressed
	if outputRMS >= inputRMS {
		t.Errorf("Compression not applied: input RMS %f, output RMS %f", inputRMS, outputRMS)
	}

	// Check gain reduction is being calculated
	if c.GetGainReduction() <= 0 {
		t.Error("No gain reduction reported")
	}
}

func TestCompressorStereoLinking(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-20.0)
	c.SetRatio(4.0)

	// Only the left channel is loud; after the 10 ms attack both channels
	// carry the same gain reduction.
	n := 4800
	inputL := make([]float32, n)
	inputR := make([]float32, n)
	for i := range inputL {
		inputL[i], inputR[i] = 0.5, 0.05
	}
	outputL := make([]float32, n)
	outputR := make([]float32, n)
	c.ProcessStereo(inputL, inputR, outputL, outputR)

	gainL, gainR := outputL[n-1]/inputL[n-1], outputR[n-1]/inputR[n-1]
	if gainL >= 0.9 || math.Abs(float64(gainL-gainR)) > 1e-6 {
		t.Errorf("gains L %f, R %f: want equal and below unity", gainL, gainR)
	}
}

func TestCompressorSidechain(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-20.0)
	c.SetRatio(10.0) // High ratio for clear effect
	c.SetKnee(KneeHard, 0)

	// Input is quiet, sidechain is loud
	n := 4800
	input := make([]float32, n)
	sidechain := make([]float32, n)
	for i := range input {
		input[i], sidechain[i] = 0.1, 1.0
	}
	output := make([]float32, n)
	c.ProcessSidechain(input, sidechain, output)

	// 20 dB over at 10:1 is 18 dB of reduction once the attack settles.
	if db := 20 * math.Log10(float64(output[n-1]/input[n-1])); math.Abs(db+18) > 0.1 {
		t.Errorf("sidechain reduction %.2f dB, want 18", -db)
	}
}

func TestCompressorReset(t *testing.T) {
	c := NewCompressor(48000.0)

	// Process some loud signal
	c.Process(1.0)

	// Reset
	c.Reset()

	// Check state is cleared
	if c.GetGainReduction() != 0 {
		t.Errorf("Gain reduction not reset: %f", c.GetGainReduction())
	}
}

// Benchmark single sample processing
func BenchmarkCompressor(b *testing.B) {
	c := NewCompressor(48000.0)
	input := float32(0.5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Process(input)
	}
}

// Benchmark buffer processing
func BenchmarkCompressorBuffer(b *testing.B) {
	c := NewCompressor(48000.0)
	input := make([]float32, 1024)
	output := make([]float32, 1024)

	// Fill with test signal
	for i := range input {
		input[i] = float32(math.Sin(float64(i) * 0.1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessBuffer(input, output)
	}
}

func TestCompressorLookaheadDelaysEveryPath(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-60) // compress everything
	c.SetLookahead(0.002)
	d := c.Latency()
	if d != 96 {
		t.Fatalf("latency %d, want 96", d)
	}

	n := 400
	inL := make([]float32, n)
	inR := make([]float32, n)
	sc := make([]float32, n)
	inL[10], inR[20] = 1, 1
	for i := range sc {
		sc[i] = 0.5
	}
	outL := make([]float32, n)
	outR := make([]float32, n)
	c.ProcessStereo(inL, inR, outL, outR)
	for i := range outL {
		if (outL[i] != 0) != (i == 10+d) || (outR[i] != 0) != (i == 20+d) {
			t.Fatalf("stereo sample %d: L=%f R=%f, impulses should arrive at %d and %d", i, outL[i], outR[i], 10+d, 20+d)
		}
	}

	c.Reset()
	out := make([]float32, n)
	c.ProcessSidechain(inL, sc, out)
	for i := range out {
		if (out[i] != 0) != (i == 10+d) {
			t.Fatalf("sidechain sample %d: %f, impulse should arrive at %d", i, out[i], 10+d)
		}
	}
	if out[10+d] >= 1 {
		t.Fatalf("sidechain did not reduce the gain: %f", out[10+d])
	}
}

func TestCompressorDoesNotAllocate(t *testing.T) {
	c := NewCompressor(48000.0)
	buf := make([]float32, 256)
	for i := range buf {
		buf[i] = float32(i%17) / 17
	}
	out := make([]float32, 256)
	out2 := make([]float32, 256)
	if n := testing.AllocsPerRun(50, func() {
		c.SetLookahead(0.003)
		c.ProcessStereo(buf, buf, out, out2)
		c.ProcessSidechain(buf, buf, out)
		c.SetLookahead(0.007)
		c.ProcessBuffer(buf, out)
	}); n != 0 {
		t.Fatalf("%v allocations", n)
	}
}

func TestCompressorStereoSidechain(t *testing.T) {
	c := NewCompressor(48000.0)
	c.SetThreshold(-20)
	c.SetRatio(4)
	c.SetKnee(KneeHard, 0)
	c.SetAttack(0.0001)
	n := 4800
	inL := make([]float32, n)
	inR := make([]float32, n)
	key := make([]float32, n)
	outL := make([]float32, n)
	outR := make([]float32, n)
	for i := range inL {
		inL[i], inR[i], key[i] = 0.01, -0.02, 1 // quiet main, 0 dBFS key
	}
	c.ProcessStereoSidechain(inL, inR, key, outL, outR)
	// The key is 20 dB over: 15 dB of reduction on both channels.
	want := math.Pow(10, -15.0/20)
	if math.Abs(float64(outL[n-1])/0.01-want) > 0.01 || math.Abs(float64(outR[n-1])/-0.02-want) > 0.01 {
		t.Fatalf("gains %v %v, want %v", outL[n-1]/0.01, outR[n-1]/-0.02, want)
	}
}
