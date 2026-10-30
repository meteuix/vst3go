package main

import (
	"math"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugintest"
)

// drum is a decaying 100 Hz hit starting at sample 1000.
func drum(n int) [][]float32 {
	x := make([]float32, n)
	for i := 1000; i < n; i++ {
		t := float64(i-1000) / 48000
		x[i] = float32(0.8 * math.Exp(-t/0.15) * math.Sin(2*math.Pi*100*t))
	}
	return plugintest.Channels(2, x)
}

func TestTransientShaperNeutralPassesThrough(t *testing.T) {
	p := plugintest.New(t, transientShaperPlugin{})
	in := drum(24000)
	out := p.Render(in, 2)
	for i := range out[0] {
		if math.Abs(float64(out[0][i]-in[0][i])) > 1e-6 {
			t.Fatalf("sample %d changed: %v -> %v", i, in[0][i], out[0][i])
		}
	}
}

func TestTransientShaperAttackAndSustain(t *testing.T) {
	in := drum(48000)
	render := func(attack, sustain, mix float64) [][]float32 {
		p := plugintest.New(t, transientShaperPlugin{})
		p.Set(paramAttack, attack)
		p.Set(paramSustain, sustain)
		p.Set(paramMix, mix)
		return p.Render(in, 2)
	}
	hit := func(x [][]float32) float64 { return plugintest.PeakDB(x[0][1000:1480]) }    // first 10 ms
	tail := func(x [][]float32) float64 { return plugintest.PeakDB(x[1][24000:28800]) } // 480-580 ms

	dry := render(0, 0, 100)
	if punchy := render(100, 0, 100); hit(punchy) < hit(dry)+3 {
		t.Errorf("Attack +100%%: hit %.2f dB, dry %.2f dB", hit(punchy), hit(dry))
	}
	if soft := render(-100, 0, 100); hit(soft) > hit(dry)-3 {
		t.Errorf("Attack -100%%: hit %.2f dB, dry %.2f dB", hit(soft), hit(dry))
	}
	if long := render(0, 100, 100); tail(long) < tail(dry)+3 {
		t.Errorf("Sustain +100%%: tail %.2f dB, dry %.2f dB", tail(long), tail(dry))
	}
	if short := render(0, -100, 100); tail(short) > tail(dry)-3 {
		t.Errorf("Sustain -100%%: tail %.2f dB, dry %.2f dB", tail(short), tail(dry))
	}
	if unmixed := render(100, 100, 0); hit(unmixed) != hit(dry) || tail(unmixed) != tail(dry) {
		t.Error("0% Mix is not the dry signal")
	}
}

func TestTransientShaperOutputAndMeter(t *testing.T) {
	p := plugintest.New(t, transientShaperPlugin{})
	p.Set(paramOutput, -6)
	p.Set(paramAttack, 100)
	out := p.Render(plugintest.Channels(2, plugintest.Constant(48000, 0.5)), 2)
	if db := plugintest.DB(float64(out[0][47999]) / 0.5); math.Abs(db+6) > 0.01 {
		t.Fatalf("steady output %.2f dB, want -6", db)
	}
	changes := p.OutputParameters()
	last := changes[len(changes)-1]
	if last.ParamID != paramGain || math.Abs(last.Value-p.Normalized(paramGain, 0)) > 0.001 {
		t.Fatalf("meter %+v, want 0 dB on a steady signal", last)
	}
}

func TestTransientShaperDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, transientShaperPlugin{})
	p.Set(paramAttack, 50)
	in := drum(512)
	if allocs := p.AllocsPerProcess(20, in, plugintest.Silence(2, 512), nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
