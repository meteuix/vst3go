package process

import (
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

func TestProcessSamplesDoesNotAllocateAfterWarmup(t *testing.T) {
	ctx := NewContext(64, param.NewRegistry())
	ctx.Input = [][]float32{
		make([]float32, 64),
		make([]float32, 64),
	}
	ctx.Output = [][]float32{
		make([]float32, 64),
		make([]float32, 64),
	}

	// Warmup initializes scratch buffers once.
	ctx.ProcessSamples(func(sample int, inputs, outputs []float32) {
		outputs[0] = inputs[0]
		outputs[1] = inputs[1]
	})

	allocs := testing.AllocsPerRun(100, func() {
		ctx.ProcessSamples(func(sample int, inputs, outputs []float32) {
			outputs[0] = inputs[0]
			outputs[1] = inputs[1]
		})
	})

	if allocs != 0 {
		t.Fatalf("expected zero allocations after warmup, got %f", allocs)
	}
}
