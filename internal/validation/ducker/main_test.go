package main

import (
	"encoding/binary"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/plugin"
	"github.com/meteuix/vst3go/v2/pkg/plugintest"
)

func TestDuckerLatencyAndSidechain(t *testing.T) {
	p := plugintest.New(t, duckerPlugin{})
	latency := p.Latency()
	if latency != 96 {
		t.Fatalf("latency = %d, want 96 at 48 kHz", latency)
	}
	if p.TakeRestarts()&plugin.RestartLatencyChanged == 0 {
		t.Fatal("latency change not announced")
	}
	msgs := p.TakeMessages()
	if len(msgs) != 1 || msgs[0].ID != messageLatency || binary.LittleEndian.Uint32(msgs[0].Data) != 96 {
		t.Fatalf("messages = %+v", msgs)
	}

	n := 4096
	main := plugintest.Channels(2, plugintest.Impulse(n, 0))
	quiet := plugintest.Silence(2, n)
	out := plugintest.Silence(2, n)
	if err := p.ProcessBuses([][][]float32{main, quiet}, [][][]float32{out}); err != nil {
		t.Fatal(err)
	}
	if at := plugintest.FirstNonZero(out[0], 0); at != latency || out[0][latency] != 1 {
		t.Fatalf("impulse at %d (%v), want %d", at, out[0][latency], latency)
	}

	// A loud sidechain ducks the main signal by Depth (18 dB).
	loud := plugintest.Channels(2, plugintest.Constant(n, 1))
	tone := plugintest.Channels(2, plugintest.Constant(n, 0.5))
	if err := p.ProcessBuses([][][]float32{tone, loud}, [][][]float32{out}); err != nil {
		t.Fatal(err)
	}
	if db := plugintest.DB(float64(out[0][n-1]) / 0.5); db > -17.9 || db < -18.1 {
		t.Fatalf("reduction = %.2f dB, want -18", db)
	}
}

func TestDuckerLearnThreshold(t *testing.T) {
	p := plugintest.New(t, duckerPlugin{})
	n := 1024
	side := plugintest.Channels(2, plugintest.Constant(n, 0.1)) // -20 dB
	if err := p.ProcessBuses([][][]float32{plugintest.Silence(2, n), side}, [][][]float32{plugintest.Silence(2, n)}); err != nil {
		t.Fatal(err)
	}
	p.Processor().(plugin.MessageReceiver).ReceiveMessage(messageLearn, nil)
	if got := p.Plain(paramThreshold); got < -20.01 || got > -19.99 {
		t.Fatalf("learned threshold = %v dB, want -20", got)
	}
}
