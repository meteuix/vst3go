package process

import (
	"testing"

	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/midi"
)

type benchmarkEventProcessor struct {
	count int
}

func (p *benchmarkEventProcessor) ProcessEvent(event midi.Event) {
	p.count++
}

func BenchmarkContextParamAccess(b *testing.B) {
	registry := param.NewRegistry()
	gain := &param.Parameter{ID: 1, Name: "Gain", Min: -12, Max: 12}
	gain.SetPlain(3)

	if err := registry.Add(gain); err != nil {
		b.Fatalf("Add(gain) error = %v", err)
	}

	ctx := NewContext(64, registry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ctx.Param(1)
		_ = ctx.ParamPlain(1)
	}
}

func BenchmarkProcessSamples(b *testing.B) {
	ctx := NewContext(64, param.NewRegistry())
	ctx.Input = [][]float32{
		make([]float32, 64),
		make([]float32, 64),
	}
	ctx.Output = [][]float32{
		make([]float32, 64),
		make([]float32, 64),
	}

	for ch := range ctx.Input {
		for sample := range ctx.Input[ch] {
			ctx.Input[ch][sample] = float32(ch + sample)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.ProcessSamples(func(sample int, inputs, outputs []float32) {
			outputs[0] = inputs[0]
			outputs[1] = inputs[1]
		})
	}
}

func BenchmarkProcessEvents(b *testing.B) {
	ctx := NewContext(256, param.NewRegistry())
	ctx.Input = [][]float32{make([]float32, 256)}
	ctx.Output = [][]float32{make([]float32, 256)}

	for i := 0; i < 128; i++ {
		ctx.AddInputEvent(midi.NoteOn(0, uint8(60+i%12), 0.8).At(int32(i * 2)))
	}

	processor := &benchmarkEventProcessor{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.count = 0
		ctx.ProcessEvents(processor, 0, 256)
	}
}
