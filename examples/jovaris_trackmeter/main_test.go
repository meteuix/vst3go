package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

func offline(t *testing.T, sr float64, block int, double bool) *plugin.Offline {
	t.Helper()
	o, err := plugin.NewOffline(trackPlugin{}, plugin.OfflineSetup{SampleRate: sr, MaxBlockSize: block, DoublePrecision: double})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { o.Close() })
	return o
}

func TestPersistOnlyLayoutAndHold(t *testing.T) {
	o := offline(t, 48000, 128, false)
	check(t, o.SetParameterPlain(paramHold, 1))
	check(t, o.SetParameterPlain(paramLayout, 1))
	in := [][]float32{{1.25}, {.5}}
	check(t, o.Process(in, [][]float32{{0}, {0}}))
	state, err := o.State()
	check(t, err)
	r := bytes.NewReader(state)
	header := make([]byte, 6)
	_, err = r.Read(header)
	check(t, err)
	var version uint32
	var count int32
	check(t, binary.Read(r, binary.LittleEndian, &version))
	check(t, binary.Read(r, binary.LittleEndian, &count))
	if string(header) != "VST3GO" || count != 2 {
		t.Fatalf("state must contain only two persistent parameters: %q count=%d", header, count)
	}
	for range count {
		var id uint32
		var value float64
		check(t, binary.Read(r, binary.LittleEndian, &id))
		check(t, binary.Read(r, binary.LittleEndian, &value))
		if (id != paramHold && id != paramLayout) || value != 1 {
			t.Fatalf("unexpected persisted parameter: %d=%v", id, value)
		}
	}
	n := offline(t, 48000, 128, false)
	check(t, n.SetState(state))
	if value(t, n, paramHold) != 1 || value(t, n, paramLayout) != 1 || value(t, n, paramClipL) != 0 || value(t, n, paramNumberL) > -119 {
		t.Fatal("layout/hold not restored or transient values restored")
	}
	// Restoring in place must also clear already accumulated readings.
	check(t, o.SetState(state))
	if value(t, o, paramNumberL) > -119 || value(t, o, paramClipL) != 0 {
		t.Fatal("in-place state load left old peak/clip values")
	}
}

func TestEditorResetAndLayoutPreserveVUAndAudio(t *testing.T) {
	o := offline(t, 48000, 128, false)
	p := o.Processor().(*processor)
	in := [][]float32{make([]float32, 48000), make([]float32, 48000)}
	out := [][]float32{make([]float32, 48000), make([]float32, 48000)}
	for i := range in[0] {
		in[0][i] = float32(.18 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		in[1][i] = in[0][i]
	}
	check(t, o.Process(in, out))
	vuBefore := value(t, o, paramVUL)
	check(t, o.SetParameterPlain(paramHold, 1))
	peakBefore := value(t, o, paramNumberL)
	check(t, o.SetParameterPlain(paramLayout, 1))
	if value(t, o, paramVUL) != vuBefore || value(t, o, paramNumberL) != peakBefore {
		t.Fatal("layout change reset measurements")
	}
	// Reset through the real EditorMessage receiver, consumed in audio.
	p.EditorMessage("reset-peak", nil)
	check(t, o.Process([][]float32{{0}, {0}}, [][]float32{{0}, {0}}))
	if value(t, o, paramNumberL) > -119 || math.Abs(value(t, o, paramVUL)-vuBefore) > .01 {
		t.Fatal("editor reset failed or reset the VU dynamics")
	}
	if p.resetRequest.Load() {
		t.Fatal("audio thread did not consume the reset request")
	}
}

// An explicit export used by browser QA: actual product metadata, parameters
// and framework SDK, rather than a hand-maintained fake parameter fixture.
func TestExportBrowserFixture(t *testing.T) {
	path := os.Getenv("TRACKMETER_BROWSER_FIXTURE")
	if path == "" {
		t.Skip("browser fixture export not requested")
	}
	p := newProcessor()
	model, err := plugin.BuildEditorModel(trackPlugin{}.GetInfo(), p.Parameters())
	check(t, err)
	data, err := json.Marshal(map[string]any{"model": model, "editor": p.Editor()})
	check(t, err)
	check(t, os.WriteFile(path, data, 0600))
}
func value(t *testing.T, o *plugin.Offline, id uint32) float64 {
	t.Helper()
	_, v, ok := o.Parameter(id)
	if !ok {
		t.Fatalf("missing parameter %d", id)
	}
	return v
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntime32BitPassThroughAndStereoIndependence(t *testing.T) {
	o := offline(t, 48000, 128, false)
	in := [][]float32{make([]float32, 48000), make([]float32, 48000)}
	out := [][]float32{make([]float32, 48000), make([]float32, 48000)}
	for i := range in[0] {
		in[0][i] = float32(.2 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		in[1][i] = in[0][i] * .1
	}
	check(t, o.Process(in, out))
	for ch := range in {
		for i := range in[ch] {
			if math.Float32bits(in[ch][i]) != math.Float32bits(out[ch][i]) {
				t.Fatalf("audio changed ch=%d i=%d", ch, i)
			}
		}
	}
	l, r := value(t, o, paramVUL), value(t, o, paramVUR)
	if math.Abs(l-r-20) > .01 {
		t.Fatalf("stereo VU not independent: %f %f", l, r)
	}
	if o.Processor().GetLatencySamples() != 0 {
		t.Fatal("nonzero reported latency")
	}
}

func TestRuntime64BitPassThroughIncludingSpecialValues(t *testing.T) {
	o := offline(t, 48000, 64, true)
	in := [][]float64{{math.Copysign(0, -1), math.SmallestNonzeroFloat64, 1.000000000000001, math.NaN(), math.Inf(1)}, {-.1, .2, -.3, 0, .4}}
	out := [][]float64{make([]float64, 5), make([]float64, 5)}
	check(t, o.Process64(in, out))
	for ch := range in {
		for i := range in[ch] {
			if math.Float64bits(in[ch][i]) != math.Float64bits(out[ch][i]) {
				t.Fatalf("64-bit audio changed %d %d", ch, i)
			}
		}
	}
}

func TestBlockSizeInvariance(t *testing.T) {
	var readings [][2]float64
	for _, block := range []int{16, 64, 127, 512, 4096} {
		o := offline(t, 48000, block, false)
		in := [][]float32{make([]float32, 96000), make([]float32, 96000)}
		out := [][]float32{make([]float32, 96000), make([]float32, 96000)}
		for i := range in[0] {
			in[0][i] = float32(math.Sqrt2 * math.Pow(10, -18.0/20) * math.Sin(2*math.Pi*1000*float64(i)/48000))
			in[1][i] = in[0][i]
		}
		check(t, o.Process(in, out))
		readings = append(readings, [2]float64{value(t, o, paramVUL), value(t, o, paramNumberL)})
	}
	for i, r := range readings {
		if math.Abs(r[0]) > .05 || math.Abs(r[1]-readings[0][1]) > .03 {
			t.Errorf("block variant %d: %+v", i, r)
		}
	}
}

func TestMonoMeasurementDuplicateWithoutStereoWidening(t *testing.T) {
	p := newProcessor()
	check(t, p.Initialize(48000, 512))
	ctx := process.NewContext(512, p.Parameters())
	ctx.Input = [][]float32{make([]float32, 512)}
	ctx.Output = [][]float32{make([]float32, 512)}
	for i := range ctx.Input[0] {
		ctx.Input[0][i] = float32(i) / 1024
	}
	p.ProcessAudio(ctx)
	if p.channels[0].Reading() != p.channels[1].Reading() {
		t.Fatal("mono meters differ")
	}
	for i, x := range ctx.Input[0] {
		if x != ctx.Output[0][i] {
			t.Fatal("mono signal changed")
		}
	}
	if !p.AcceptBusArrangements([]bus.SpeakerArrangement{bus.ArrangementMono}, []bus.SpeakerArrangement{bus.ArrangementMono}) {
		t.Fatal("mono rejected")
	}
	if p.AcceptBusArrangements([]bus.SpeakerArrangement{bus.ArrangementMono}, []bus.SpeakerArrangement{bus.ArrangementStereo}) {
		t.Fatal("channel conversion accepted")
	}
}

func TestHoldBackgroundResetAndStateRestore(t *testing.T) {
	o := offline(t, 48000, 128, false)
	in := [][]float32{make([]float32, 2048), make([]float32, 2048)}
	out := [][]float32{make([]float32, 2048), make([]float32, 2048)}
	in[0][0] = 1.25
	in[1][0] = .5
	check(t, o.Process(in, out))
	check(t, o.SetParameterPlain(paramHold, 1))
	clear(in[0])
	clear(in[1])
	check(t, o.Process(in, out))
	if got := value(t, o, paramNumberL); math.Abs(got-20*math.Log10(1.25)) > 1e-8 {
		t.Fatalf("background hold not kept: %f", got)
	}
	if value(t, o, paramClipL) != 1 {
		t.Fatal("recorded clip lost")
	}
	check(t, o.SetParameterPlain(paramReset, 1))
	check(t, o.Process(in, out))
	if value(t, o, paramNumberL) > -119 || value(t, o, paramClipL) != 0 {
		t.Fatal("reset failed")
	}
	state, err := o.State()
	check(t, err)
	n := offline(t, 48000, 128, false)
	check(t, n.SetState(state))
	if value(t, n, paramHold) != 1 || value(t, n, paramReset) != 0 || value(t, n, paramNumberL) > -119 {
		t.Fatal("transient values restored")
	}
}

func TestRuntimeProcessDoesNotAllocate(t *testing.T) {
	o := offline(t, 48000, 128, false)
	in := [][]float32{make([]float32, 128), make([]float32, 128)}
	out := [][]float32{make([]float32, 128), make([]float32, 128)}
	for i := range in[0] {
		in[0][i] = .2
		in[1][i] = .1
	}
	check(t, o.Process(in, out))
	var processErr error
	a := testing.AllocsPerRun(200, func() { processErr = o.Process(in, out) })
	check(t, processErr)
	if a != 0 {
		t.Fatalf("%v allocations per audio block", a)
	}
}

// Exercises editor pull/ack against the real processor and Offline host.
func TestSnapshotStoppedResetAndResume(t *testing.T) {
	o := offline(t, 48000, 128, false)
	p := o.Processor().(*processor)
	check(t, o.Process([][]float32{{1.25}, {.9}}, [][]float32{{0}, {0}}))
	p.EditorMessage("reset-peak", nil)
	messages := o.TakeEditorMessages()
	if len(messages) != 1 {
		t.Fatalf("missing native reset acknowledgement: %v", messages)
	}
	type snapshot struct {
		Values  [8]float64 `json:"values"`
		Pending bool       `json:"pending"`
		Blocks  uint64     `json:"blocks"`
	}
	var pending snapshot
	check(t, json.Unmarshal(messages[0].Data, &pending))
	if !pending.Pending || pending.Values[6] != 1 {
		t.Fatal("stopped reset must acknowledge pending without mutating audio-owned history")
	}
	check(t, o.Process([][]float32{{0}, {0}}, [][]float32{{0}, {0}}))
	p.EditorMessage("meter-poll", nil)
	messages = o.TakeEditorMessages()
	var resumed snapshot
	check(t, json.Unmarshal(messages[0].Data, &resumed))
	if resumed.Pending || resumed.Values[4] > -119 || resumed.Values[6] != 0 || resumed.Blocks <= pending.Blocks {
		t.Fatalf("reset did not apply on resume: %+v", resumed)
	}
}

func TestConcurrentSnapshotAndAudio(t *testing.T) {
	o := offline(t, 48000, 128, false)
	p := o.Processor().(*processor)
	done := make(chan error, 1)
	go func() {
		in := [][]float32{make([]float32, 128), make([]float32, 128)}
		out := [][]float32{make([]float32, 128), make([]float32, 128)}
		for i := 0; i < 500; i++ {
			if err := o.Process(in, out); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 500; i++ {
		if i%20 == 0 {
			p.EditorMessage("reset-peak", nil)
		}
		p.EditorMessage("meter-poll", nil)
	}
	check(t, <-done)
	if len(o.TakeEditorMessages()) == 0 {
		t.Fatal("missing snapshots")
	}
}
