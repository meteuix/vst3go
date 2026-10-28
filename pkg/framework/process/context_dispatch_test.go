package process

import (
	"testing"

	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/midi"
)

type segment struct {
	offset, length int
	gain           float64
	notes          []uint8
	inputFirst     float32
}

func newGainRegistry(t *testing.T) *param.Registry {
	t.Helper()
	registry := param.NewRegistry()
	if err := registry.Add(&param.Parameter{ID: 7, Name: "Gain", Min: 0, Max: 1}); err != nil {
		t.Fatal(err)
	}
	return registry
}

func runtimeContext(t *testing.T, numSamples int) (*Context, []float32, []float32) {
	t.Helper()
	ctx := NewContext(numSamples, newGainRegistry(t))
	ctx.ConfigureBuses([]int{1, 2}, []int{1}, 1)
	in := make([]float32, numSamples)
	for i := range in {
		in[i] = float32(i)
	}
	out := make([]float32, numSamples)
	ctx.BeginBlock(numSamples)
	ctx.AddInputChannel(0, in)
	ctx.AddOutputChannel(0, out)
	return ctx, in, out
}

func TestDispatchSplitsAtParameterChanges(t *testing.T) {
	ctx, _, out := runtimeContext(t, 64)
	ctx.AddParameterChange(7, 0.5, 40)
	ctx.AddParameterChange(7, 0.25, 0)
	ctx.AddParameterChange(7, 0.75, 40) // same offset: later host point wins
	ctx.AddInputEvent(midi.NoteOn(0, 60, 0).At(10))
	ctx.AddInputEvent(midi.NoteOn(0, 61, 0).At(40))
	ctx.AddInputEvent(midi.NoteOn(0, 62, 0).At(63))

	var got []segment
	ctx.Dispatch(func(c *Context) {
		seg := segment{offset: c.BlockOffset(), length: c.NumSamples(), gain: c.Param(7), inputFirst: c.Input[0][0]}
		for _, ev := range c.GetAllInputEvents() {
			seg.notes = append(seg.notes, ev.Note)
		}
		for i := range c.Output[0] {
			c.Output[0][i] = float32(seg.gain)
		}
		got = append(got, seg)
	})

	want := []segment{
		{offset: 0, length: 40, gain: 0.25, notes: []uint8{60}, inputFirst: 0},
		{offset: 40, length: 24, gain: 0.75, notes: []uint8{61, 62}, inputFirst: 40},
	}
	if len(got) != len(want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.offset != w.offset || g.length != w.length || g.gain != w.gain || g.inputFirst != w.inputFirst || len(g.notes) != len(w.notes) {
			t.Fatalf("segment %d = %+v, want %+v", i, g, w)
		}
		for j := range w.notes {
			if g.notes[j] != w.notes[j] {
				t.Fatalf("segment %d notes = %v, want %v", i, g.notes, w.notes)
			}
		}
	}
	if out[39] != 0.25 || out[40] != 0.75 || out[63] != 0.75 {
		t.Fatalf("output not written through segment views: out[39]=%v out[40]=%v out[63]=%v", out[39], out[40], out[63])
	}
	if ctx.NumSamples() != 64 || ctx.BlockOffset() != 0 || len(ctx.GetAllInputEvents()) != 3 {
		t.Fatalf("context not restored after dispatch: samples=%d offset=%d events=%d", ctx.NumSamples(), ctx.BlockOffset(), len(ctx.GetAllInputEvents()))
	}
}

func TestDispatchAppliesChangesBeyondBlockAfterProcessing(t *testing.T) {
	ctx, _, _ := runtimeContext(t, 16)
	ctx.AddParameterChange(7, 0.9, 100)

	calls := 0
	ctx.Dispatch(func(c *Context) {
		calls++
		if c.Param(7) != 0 {
			t.Fatalf("change beyond the block applied too early: %v", c.Param(7))
		}
	})
	if calls != 1 || ctx.Param(7) != 0.9 {
		t.Fatalf("calls=%d gain=%v, want 1 call and gain 0.9", calls, ctx.Param(7))
	}
}

func TestDispatchSilenceFlagsOfSplitBlock(t *testing.T) {
	ctx, _, _ := runtimeContext(t, 64)
	ctx.AddParameterChange(7, 0.5, 20)
	ctx.AddParameterChange(7, 0.7, 40)
	// Silent before the first change, sounding in the middle segment.
	ctx.Dispatch(func(c *Context) {
		if c.BlockOffset() != 20 {
			c.SetOutputSilenceFlags(0, 1)
		}
	})
	if flags := ctx.OutputSilenceFlags(0); flags != 0 {
		t.Fatalf("silence flags = %b, want 0: one segment sounded", flags)
	}

	ctx.BeginBlock(64)
	ctx.AddParameterChange(7, 0.5, 20)
	ctx.Dispatch(func(c *Context) { c.SetOutputSilenceFlags(0, 1) })
	if flags := ctx.OutputSilenceFlags(0); flags != 1 {
		t.Fatalf("silence flags = %b, want 1: every segment was silent", flags)
	}
}

func TestDispatchFlushBlockOnlyAppliesChanges(t *testing.T) {
	ctx := NewContext(16, newGainRegistry(t))
	ctx.ConfigureBuses([]int{2}, []int{2}, -1)
	ctx.BeginBlock(0)
	ctx.AddParameterChange(7, 0.3, 0)

	ctx.Dispatch(func(*Context) { t.Fatal("process must not run for a zero-sample block") })
	if ctx.Param(7) != 0.3 {
		t.Fatalf("gain = %v, want 0.3", ctx.Param(7))
	}
}

func TestBusAccessAndSidechain(t *testing.T) {
	ctx, _, _ := runtimeContext(t, 8)
	if ctx.Sidechain() != nil {
		t.Fatal("inactive sidechain must be nil")
	}
	sc := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	ctx.AddInputChannel(1, sc)
	ctx.AddInputChannel(1, sc)
	ctx.AddInputChannel(1, sc) // beyond configured channel count: ignored
	ctx.SetInputSilenceFlags(1, 0b10)
	ctx.AddParameterChange(7, 1, 4)

	var sidechainFirst []float32
	ctx.Dispatch(func(c *Context) {
		if c.NumInputBuses() != 2 || c.NumOutputBuses() != 1 {
			t.Fatalf("bus counts = %d/%d", c.NumInputBuses(), c.NumOutputBuses())
		}
		if len(c.Sidechain()) != 2 || c.InputSilenceFlags(1) != 0b10 {
			t.Fatalf("sidechain channels=%d flags=%b", len(c.Sidechain()), c.InputSilenceFlags(1))
		}
		sidechainFirst = append(sidechainFirst, c.Sidechain()[0][0])
		c.SetOutputSilenceFlags(0, 1)
	})
	if len(sidechainFirst) != 2 || sidechainFirst[0] != 1 || sidechainFirst[1] != 5 {
		t.Fatalf("sidechain segment views = %v, want [1 5]", sidechainFirst)
	}
	if ctx.OutputSilenceFlags(0) != 1 {
		t.Fatal("output silence flags lost")
	}

	ctx.BeginBlock(8)
	if ctx.OutputSilenceFlags(0) != 0 || len(ctx.InputBus(1)) != 0 {
		t.Fatal("BeginBlock must reset silence flags and bus channels")
	}
}

func TestOutputParametersUseSegmentOffset(t *testing.T) {
	ctx, _, _ := runtimeContext(t, 32)
	ctx.AddParameterChange(7, 0.5, 16)
	ctx.Dispatch(func(c *Context) {
		c.SetOutputParameter(99, 2) // clamped
	})

	changes := ctx.OutputParameterChanges()
	if len(changes) != 2 || changes[0].SampleOffset != 0 || changes[1].SampleOffset != 16 || changes[1].Value != 1 {
		t.Fatalf("output parameter changes = %+v", changes)
	}
}

func TestHandBuiltContextDispatch(t *testing.T) {
	ctx := NewContext(32, newGainRegistry(t))
	ctx.Input = [][]float32{make([]float32, 32)}
	ctx.Output = [][]float32{make([]float32, 32)}
	ctx.AddParameterChange(7, 0.5, 8)

	var lengths []int
	ctx.Dispatch(func(c *Context) { lengths = append(lengths, c.NumSamples()) })
	if len(lengths) != 2 || lengths[0] != 8 || lengths[1] != 24 {
		t.Fatalf("segment lengths = %v, want [8 24]", lengths)
	}

	ctx.Input = [][]float32{make([]float32, 16)}
	ctx.Output = [][]float32{make([]float32, 16)}
	ctx.ResetParameterChanges()
	lengths = nil
	ctx.Dispatch(func(c *Context) { lengths = append(lengths, c.NumSamples()) })
	if len(lengths) != 1 || lengths[0] != 16 {
		t.Fatalf("second block lengths = %v, want [16]", lengths)
	}
}

func TestEventDataBuffer(t *testing.T) {
	ctx := NewContext(16, nil)
	a := ctx.EventDataBuffer(4)
	b := ctx.EventDataBuffer(4)
	copy(a, "abcd")
	copy(b, "efgh")
	if string(a) != "abcd" || string(b) != "efgh" {
		t.Fatalf("event data buffers overlap: %q %q", a, b)
	}
	big := ctx.EventDataBuffer(defaultEventDataCapacity * 2)
	if len(big) != defaultEventDataCapacity*2 {
		t.Fatalf("oversized buffer len = %d", len(big))
	}
}

func TestDispatchDoesNotAllocate(t *testing.T) {
	ctx, _, _ := runtimeContext(t, 64)
	note := midi.NoteOn(0, 60, 0).At(20)
	process := func(c *Context) {
		for range c.GetAllInputEvents() {
		}
		c.SetOutputParameter(1, 0.5)
	}
	in := make([]float32, 64)
	out := make([]float32, 64)

	allocs := testing.AllocsPerRun(100, func() {
		ctx.BeginBlock(64)
		ctx.AddInputChannel(0, in)
		ctx.AddOutputChannel(0, out)
		ctx.AddParameterChange(7, 0.5, 32)
		ctx.AddParameterChange(7, 0.6, 10)
		ctx.AddInputEvent(note)
		ctx.Dispatch(process)
	})
	if allocs != 0 {
		t.Fatalf("Dispatch allocated %.1f times per block", allocs)
	}
}

func TestDispatch64BitSegments(t *testing.T) {
	ctx := NewContext(32, newGainRegistry(t))
	ctx.ConfigureBuses([]int{1}, []int{1}, -1)
	in := make([]float64, 32)
	for i := range in {
		in[i] = float64(i)
	}
	out := make([]float64, 32)
	ctx.BeginBlock64(32)
	ctx.AddInputChannel64(0, in)
	ctx.AddOutputChannel64(0, out)
	ctx.AddParameterChange(7, 0.5, 16)

	var offsets []int
	ctx.Dispatch(func(c *Context) {
		if !c.Is64Bit() || c.Input != nil || c.Output != nil {
			t.Fatal("64-bit block must expose only Input64/Output64")
		}
		offsets = append(offsets, c.BlockOffset())
		for i, x := range c.Input64[0] {
			c.Output64[0][i] = x * c.Param(7)
		}
	})
	if len(offsets) != 2 || offsets[1] != 16 {
		t.Fatalf("segment offsets = %v, want [0 16]", offsets)
	}
	if out[15] != 0 || out[16] != 8 || out[31] != 15.5 {
		t.Fatalf("64-bit output = %v", out)
	}

	// The next 32-bit block switches back.
	in32, out32 := make([]float32, 32), make([]float32, 32)
	ctx.BeginBlock(32)
	ctx.AddInputChannel(0, in32)
	ctx.AddOutputChannel(0, out32)
	ctx.Dispatch(func(c *Context) {
		if c.Is64Bit() || c.Input64 != nil || len(c.Output) != 1 {
			t.Fatal("32-bit block must expose only Input/Output")
		}
	})
}

func TestAppendEventText(t *testing.T) {
	ctx := NewContext(16, nil)
	ctx.BeginBlock(16)
	text := ctx.AppendEventText([]uint16{'C', 'm', 0xD834, 0xDD1E}) // "Cm𝄞"
	if string(text) != "Cm𝄞" {
		t.Fatalf("text = %q", text)
	}
	other := ctx.AppendEventText([]uint16{'x'})
	if string(text) != "Cm𝄞" || string(other) != "x" {
		t.Fatal("event texts of one block must not overlap")
	}
}

func TestSmoothedParameterGlidesAcrossSegments(t *testing.T) {
	registry := param.NewRegistry()
	gain := param.New(1, "Gain").Range(0, 1).Default(0).Smooth(0.008).Build() // 8 samples at 1 kHz
	plain := param.New(2, "Plain").Range(0, 1).Build()
	if err := registry.Add(gain, plain); err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(16, registry)
	ctx.SampleRate = 1000
	ctx.ConfigureBuses(nil, []int{1}, -1)
	out := make([]float32, 16)
	ctx.BeginBlock(16)
	ctx.AddOutputChannel(0, out)
	ctx.AddParameterChange(1, 1, 4)

	if ctx.Smoothed(2) != nil {
		t.Fatal("parameter without smoothing time must have no ramp")
	}
	ctx.Dispatch(func(c *Context) {
		c.Smoothed(1).Fill(c.Output[0])
	})
	// 0 until the change at sample 4, then 8 linear steps to 1.
	want := []float32{0, 0, 0, 0, 0.125, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875, 1, 1, 1, 1, 1}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out = %v, want %v", out, want)
		}
	}

	// A sample-rate change re-prepares ramps at the current value.
	ctx.SampleRate = 2000
	ctx.BeginBlock(16)
	ctx.AddOutputChannel(0, out)
	ctx.Dispatch(func(c *Context) {
		if r := c.Smoothed(1); r.IsSmoothing() || r.Current() != 1 {
			t.Fatalf("ramp after sample-rate change: smoothing=%v current=%v", r.IsSmoothing(), r.Current())
		}
	})
}
