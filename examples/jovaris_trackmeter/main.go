// Track Meter Trial implements the supplied H/V mockups on vst3go.
// Existing JUCE source and the original mockup images remain untouched.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sync/atomic"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	fp "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"jovarisaudio.local/trackmeter-trial/internal/meter"
)

//go:embed ui/meter.html
var meterHTML string

const (
	paramHold    uint32 = 1
	paramReset   uint32 = 2
	paramLayout  uint32 = 3
	paramPeakL   uint32 = 10
	paramPeakR   uint32 = 11
	paramVUL     uint32 = 12
	paramVUR     uint32 = 13
	paramNumberL uint32 = 14
	paramNumberR uint32 = 15
	paramClipL   uint32 = 16
	paramClipR   uint32 = 17
)

type trackPlugin struct{}

func (trackPlugin) GetInfo() fp.Info {
	return fp.Info{ID: "audio.jovaris.trackmeter.vst3go.trial", Name: "Jovaris Track Meter Trial",
		Version: "0.1.4", Vendor: "Jovaris Audio", Category: fp.CategoryFxAnalyzer}
}
func (trackPlugin) CreateProcessor() plugin.Processor { return newProcessor() }

type processor struct {
	*fp.BaseProcessor
	channels     [2]meter.Channel
	resetDown    bool
	reload       atomic.Bool
	resetRequest atomic.Bool
	persistent   *param.Registry
	untilPublish int
	host         plugin.Host // editor/main thread only
	snapshotSeq  atomic.Uint64
	snapshot     [8]atomic.Uint64
	resetSerial  atomic.Uint64
	resetApplied atomic.Uint64
	blocks       atomic.Uint64
}

func (*processor) Editor() plugin.Editor {
	return plugin.Editor{HTML: meterHTML, Width: 800, Height: 380,
		MinWidth: 400, MinHeight: 380, MaxWidth: 800, MaxHeight: 940}
}

func (p *processor) StateParameters() *param.Registry { return p.persistent }

func (p *processor) SetHost(h plugin.Host) { p.host = h }

// Pull snapshots from the editor's main-thread message handler as well as
// publishing host parameters. This path does not depend on host callbacks.
func (p *processor) EditorMessage(name string, _ json.RawMessage) {
	if name == "reset-peak" {
		p.resetSerial.Add(1)
		p.resetRequest.Store(true)
	}
	if (name == "meter-poll" || name == "reset-peak") && p.host != nil {
		for attempt := 0; attempt < 3; attempt++ {
			seq := p.snapshotSeq.Load()
			if seq&1 != 0 {
				continue
			}
			var values [8]float64
			for i := range values {
				values[i] = math.Float64frombits(p.snapshot[i].Load())
			}
			applied, blocks := p.resetApplied.Load(), p.blocks.Load()
			if seq != p.snapshotSeq.Load() {
				continue
			}
			_ = p.host.PostEditorMessage("meter-snapshot", struct {
				Values  [8]float64 `json:"values"`
				Blocks  uint64     `json:"blocks"`
				Pending bool       `json:"pending"`
			}{values, blocks, p.resetSerial.Load() != applied})
			break
		}
	}
}

func levelText(v float64) string {
	if v <= meter.FloorDB {
		return "-inf"
	}
	return fmt.Sprintf("%+.1f", v)
}

func newProcessor() *processor {
	p := &processor{BaseProcessor: fp.NewBaseProcessor(bus.NewEffectStereo())}
	for i, v := range [8]float64{-120, -120, -20, -20, -120, -120, 0, 0} {
		p.snapshot[i].Store(math.Float64bits(v))
	}
	// The framework's default deadline guard emits silence for late blocks.
	// Turn it off for this measurement-only trial to avoid deliberately muting
	// its input. Go GC pauses remain a real-time validation concern.
	p.SetProcessDeadline(0)
	p.Parameters().Add(
		param.New(paramHold, "Peak Hold").Toggle().Build(),
		param.New(paramReset, "Reset Peak (OFF then ON)").Toggle().Build(),
		param.New(paramLayout, "Vertical Layout").Toggle().Flags(param.IsHidden).Build(),
	)
	p.persistent = param.NewRegistry()
	p.persistent.Add(p.Parameters().Get(paramHold), p.Parameters().Get(paramLayout))
	for _, s := range []struct {
		id          uint32
		name, unit  string
		lo, hi, def float64
	}{
		{paramPeakL, "L Live Peak", "dBFS", -120, 60, -120},
		{paramPeakR, "R Live Peak", "dBFS", -120, 60, -120},
		{paramVUL, "L Analog VU", "VU", -20, 3, -20},
		{paramVUR, "R Analog VU", "VU", -20, 3, -20},
		{paramNumberL, "L Peak Readout / Hold", "dBFS", -120, 60, -120},
		{paramNumberR, "R Peak Readout / Hold", "dBFS", -120, 60, -120},
		{paramClipL, "L Clip", "", 0, 1, 0},
		{paramClipR, "R Clip", "", 0, 1, 0},
	} {
		b := param.New(s.id, s.name).Range(s.lo, s.hi).Default(s.def).Unit(s.unit).ReadOnly()
		if s.unit == "dBFS" {
			b.Formatter(levelText, param.DecibelParser)
		}
		p.Parameters().Add(b.Build())
	}
	p.OnInitialize(func(sr float64, _ int32) error {
		p.channels = [2]meter.Channel{meter.New(sr), meter.New(sr)}
		p.untilPublish = 0
		p.resetDown = false
		return nil
	})
	return p
}

func (*processor) SupportsDoublePrecision() bool { return true }
func (*processor) AcceptBusArrangements(in, out []bus.SpeakerArrangement) bool {
	return len(in) == 1 && len(out) == 1 && in[0] == out[0] &&
		(in[0] == bus.ArrangementMono || in[0] == bus.ArrangementStereo)
}

func consume[T ~float32 | ~float64](p *processor, in [][]T, n int) {
	for ch := 0; ch < 2; ch++ {
		source := ch
		if len(in) == 1 {
			source = 0
		}
		for i := 0; i < n; i++ {
			x := 0.0
			if source < len(in) && i < len(in[source]) {
				x = float64(in[source][i])
			}
			p.channels[ch].Sample(x)
		}
	}
}

func (p *processor) ProcessAudio(ctx *process.Context) {
	ctx.PassThrough()
	if p.reload.Swap(false) {
		p.channels = [2]meter.Channel{meter.New(p.SampleRate()), meter.New(p.SampleRate())}
		p.untilPublish = 0
		p.resetDown = false
	}
	down := ctx.Param(paramReset) >= .5
	serial := p.resetSerial.Load()
	requested := p.resetRequest.Swap(false) || serial != p.resetApplied.Load()
	if requested || (down && !p.resetDown) {
		for ch := range p.channels {
			p.channels[ch].ResetPeak()
		}
		p.untilPublish = 0
	}
	p.resetDown = down
	n := ctx.NumSamples()
	if ctx.Is64Bit() {
		consume(p, ctx.Input64, n)
	} else {
		consume(p, ctx.Input, n)
	}
	p.blocks.Add(1)
	p.untilPublish -= n
	if p.untilPublish > 0 {
		return
	}
	p.untilPublish = max(1, int(p.SampleRate()/30))
	p.snapshotSeq.Add(1)
	hold := ctx.Param(paramHold) >= .5
	for ch := 0; ch < 2; ch++ {
		r := p.channels[ch].Reading()
		number, clip := r.PeakDB, r.Clip
		if hold {
			number, clip = r.HoldDB, r.RecordedClip
		}
		clipValue := 0.0
		if clip {
			clipValue = 1
		}
		for _, v := range [...]struct {
			id    uint32
			value float64
		}{
			{paramPeakL + uint32(ch), r.PeakDB}, {paramVUL + uint32(ch), r.VU},
			{paramNumberL + uint32(ch), number}, {paramClipL + uint32(ch), clipValue},
		} {
			parameter := p.Parameters().Get(v.id)
			normalized := parameter.Normalize(v.value)
			ctx.SetOutputParameter(v.id, normalized)
			p.snapshot[v.id-paramPeakL].Store(math.Float64bits(parameter.Denormalize(normalized)))
		}
	}
	p.resetApplied.Store(serial)
	p.snapshotSeq.Add(1)
}

// Only Hold and H/V are persisted through StateParameters. Transient values
// are also cleared when restoring older trial states or restoring in place.
func (*processor) SaveCustomState(io.Writer) error { return nil }
func (p *processor) LoadCustomState(io.Reader) error {
	for _, v := range p.Parameters().All() {
		if v.ID != paramHold && v.ID != paramLayout {
			v.SetNormalized(v.DefaultValue)
		}
	}
	p.reload.Store(true)
	p.resetRequest.Store(false)
	return nil
}

func init() { plugin.Register(trackPlugin{}) }
func main() {}
