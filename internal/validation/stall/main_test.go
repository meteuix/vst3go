package main

import (
	"testing"

	"github.com/th-release/vst3go/pkg/plugin"
	"github.com/th-release/vst3go/pkg/plugintest"
)

func TestStallCountsNotesInItsGain(t *testing.T) {
	p := plugintest.New(t, stallPlugin{})
	if d, ok := p.Processor().(plugin.DeadlineProcessor); !ok || d.ProcessDeadline() != 0.5 {
		t.Fatal("stall does not run on the deadline guard")
	}
	in := plugintest.Channels(2, plugintest.Sine(440, 48000, 256, 0.25))
	if out := p.Render(in, 2); out[0][100] != in[0][100] || out[1][200] != in[1][200] {
		t.Fatal("without notes the output is not the input")
	}
	p.Note(0, 60, 1, 10, 20)
	p.Note(0, 64, 1, 40, 0)
	p.Render(in, 2)
	// Three note events so far: the gain is 4.
	out := p.Render(in, 2)
	for ch := range out {
		for i := range out[ch] {
			if out[ch][i] != 4*in[ch][i] {
				t.Fatalf("output %d[%d] = %v, want 4 × %v", ch, i, out[ch][i], in[ch][i])
			}
		}
	}
}
