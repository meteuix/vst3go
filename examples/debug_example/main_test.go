package main

import (
	"bytes"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meteuix/vst3go/v2/pkg/framework/debug"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

// logBuffer is a bytes.Buffer safe for the reporter goroutine.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDebugExampleAppliesGainAndMeters(t *testing.T) {
	p := plugintest.New(t, debugPlugin{logTo: &logBuffer{}})
	p.Set(paramGain, -6)
	out := p.Render(plugintest.Channels(2, plugintest.Constant(4800, 0.5)), 2)
	want := 0.5 * math.Pow(10, -6.0/20)
	if math.Abs(float64(out[1][4799])-want) > 1e-6 {
		t.Fatalf("output %v, want %v", out[1][4799], want)
	}

	var cpu, peak []float64
	for _, c := range p.OutputParameters() {
		switch c.ParamID {
		case paramCPULoad:
			cpu = append(cpu, c.Value)
		case paramPeak:
			peak = append(peak, c.Value)
		}
	}
	if len(cpu) == 0 || len(peak) == 0 {
		t.Fatalf("meters not published: %+v", p.OutputParameters())
	}
	// -12 dBFS peak on the -60..0 dB meter.
	if got, want := peak[len(peak)-1], (60-12.04)/60; math.Abs(got-want) > 0.01 {
		t.Fatalf("peak meter %v, want %v", got, want)
	}
}

func TestDebugExampleReportsProblemsOffTheAudioThread(t *testing.T) {
	logs := &logBuffer{}
	p := plugintest.New(t, debugPlugin{logTo: logs, reportEvery: 5 * time.Millisecond})
	p.Set(paramDebugLevel, float64(debug.LogLevelDebug))
	p.Set(paramGain, 0)

	// Full scale clips once the gain has ramped from -3 dB to 0 dB.
	in := plugintest.Channels(2, plugintest.Constant(4096, 1))
	in[0][3000] = float32(math.NaN())
	out := p.Render(in, 2)
	if out[0][3000] != 0 {
		t.Fatalf("NaN reached the output: %v", out[0][3000])
	}

	// The reporter logs on its own clock: wait until every line is there.
	wants := []string{"[ERROR] [DebugExample] 1 NaN/Inf output samples", "[WARN] [DebugExample] ", " output samples clipped", "[DEBUG] [DebugExample] blocks 1,"}
	logged := func() bool {
		got := logs.String()
		for _, want := range wants {
			if !strings.Contains(got, want) {
				return false
			}
		}
		return true
	}
	deadline := time.Now().Add(2 * time.Second)
	for !logged() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := logs.String()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "deactivated") {
		t.Fatalf("no final report:\n%s", logs.String())
	}
}

func TestDebugExampleDoesNotAllocate(t *testing.T) {
	p := plugintest.New(t, debugPlugin{logTo: &logBuffer{}})
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 512, 0.5))
	out := plugintest.Silence(2, 512)
	if allocs := p.AllocsPerProcess(20, in, out, nil); allocs != 0 {
		t.Fatalf("%v allocations per block", allocs)
	}
}
