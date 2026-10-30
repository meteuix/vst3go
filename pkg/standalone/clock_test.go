//go:build standalone

package standalone

import (
	"io"
	"math"
	"testing"
	"time"
)

// master plays a MIDI clock master to an application's clock follower in
// simulated time: clocks arrive between rendered blocks of 48 kHz audio.
type master struct {
	a      *app
	now    time.Time
	frames float64 // rendered frames not yet a whole block, carried over
	clocks int64   // clocks sent since the last start, continue or locate
	out    []float32
}

func (m *master) send(msg ...byte) { m.a.clock.message(msg, m.now) }

// play sends n clocks at tempo, each jitter late or early by turns, and
// renders the audio between them.
func (m *master) play(n int, tempo float64, jitter time.Duration) {
	interval := 60 / (tempo * clocksPerBeat)
	for i := range n {
		late := jitter
		if i%2 == 1 {
			late = -jitter
		}
		m.a.clock.message([]byte{clockTick}, m.now.Add(late))
		m.clocks++
		m.advance(interval)
	}
}

// advance renders seconds of audio in 64-frame blocks.
func (m *master) advance(seconds float64) {
	m.now = m.now.Add(time.Duration(seconds * float64(time.Second)))
	m.frames += seconds * 48000
	blocks := uint32(m.frames / 64)
	m.frames -= float64(blocks) * 64
	if blocks > 0 {
		render(m.out, nil, 0, 2, blocks*64)
	}
}

func TestMIDIClock(t *testing.T) {
	hostPlugin(t, "Standalone Test Synth", 64)
	toolbar := &recorder{}
	a := &app{dir: t.TempDir(), settings: Settings{Tempo: 90, MIDIClock: true}, toolbarPost: toolbar}
	a.followClock()
	t.Cleanup(func() {
		a.settings.MIDIClock = false
		a.followClock()
	})
	m := &master{a: a, now: time.Unix(1000, 0), out: make([]float32, 2*48000)}

	// Song position 8 (16th notes) locates to two quarter notes, stopped.
	m.send(songPosition, 8, 0)
	m.advance(0.01)
	if tr := synthTransport; tr.IsPlaying || tr.ProjectTimeMusic != 2 || tr.Tempo != 90 {
		t.Fatalf("after song position: %+v", tr)
	}

	// Continue plays from there at the clock's tempo, phase-locked: a
	// block's position is the master's, which moves a clock (1/24 quarter)
	// between clocks, within a few ms of jittered arrival.
	m.send(clockContinue)
	m.clocks = -1 // the first clock plays the position it continues from
	m.play(24*8, 125, time.Millisecond)
	checkLock := func(what string, tempo float64) {
		t.Helper()
		tr := synthTransport
		if !tr.IsPlaying || math.Abs(tr.Tempo-tempo) > 0.05 {
			t.Fatalf("%s: transport %+v, want playing at %v BPM", what, tr, tempo)
		}
	}
	checkPhase := func(what string, start float64, tempo float64) {
		t.Helper()
		// The last block started (m.frames+64)/48000 seconds before the next
		// clock, a clock (1/24 quarter) past the last one.
		master := start + float64(m.clocks+1)/clocksPerBeat - (m.frames+64)/48000*tempo/60
		ms := (synthTransport.ProjectTimeMusic - master) * 60000 / tempo
		t.Logf("%s: %.2f ms from the master", what, ms)
		if math.Abs(ms) > 2 {
			t.Fatalf("%s: position %v, master at %v (%.2f ms apart)", what, synthTransport.ProjectTimeMusic, master, ms)
		}
	}
	checkLock("following at 125 BPM", 125)
	checkPhase("following at 125 BPM", 2, 125)
	a.updateClock(m.now)
	if msg, ok := toolbar.last().(transportMessage); !ok || !msg.Playing || msg.Tempo != 125 || !msg.Clock {
		t.Fatalf("toolbar following = %+v", toolbar.last())
	}

	// The toolbar's transport controls do nothing while following.
	a.toolbarMessage([]byte(`{"type":"stop"}`))
	a.toolbarMessage([]byte(`{"type":"tempo","tempo":60}`))
	m.play(24, 125, 0)
	checkLock("after toolbar stop", 125)
	if msg, ok := toolbar.last().(transportMessage); !ok || !msg.Playing || msg.Tempo != 125 {
		t.Fatalf("toolbar after its stop while following = %+v", toolbar.last())
	}

	// A tempo change: the tempo follows within the two-beat window, the
	// position locks again.
	m.play(24*8, 100, 2*time.Millisecond)
	checkLock("following at 100 BPM", 100)
	m.play(24*4, 100, 0)
	checkPhase("following at 100 BPM", 2, 100)

	// Stop holds at the master's position.
	m.send(clockStop)
	m.advance(0.01)
	stopped := 2 + float64(m.clocks)/clocksPerBeat
	if tr := synthTransport; tr.IsPlaying || math.Abs(tr.ProjectTimeMusic-stopped) > 1e-9 {
		t.Fatalf("after stop: %+v, want %v", tr, stopped)
	}
	a.updateClock(m.now)
	if msg, ok := toolbar.last().(transportMessage); !ok || msg.Playing {
		t.Fatalf("toolbar after stop = %+v", toolbar.last())
	}

	// Clocks while stopped keep the tempo but not the position.
	m.play(24, 140, 0)
	if tr := synthTransport; tr.IsPlaying || math.Abs(tr.ProjectTimeMusic-stopped) > 1e-9 {
		t.Fatalf("stopped transport moved with clocks: %+v", tr)
	}

	// Start plays from zero on the next clock.
	m.send(clockStart)
	m.clocks = -1
	m.play(24*8, 140, time.Millisecond)
	checkLock("after start", 140)
	checkPhase("after start", 0, 140)

	// A master gone quiet without stop stops the transport.
	m.advance(0.1)
	a.updateClock(m.now)
	m.advance(0.01)
	if tr := synthTransport; tr.IsPlaying {
		t.Fatalf("transport plays without clocks: %+v", tr)
	}
	if msg, ok := toolbar.last().(transportMessage); !ok || msg.Playing {
		t.Fatalf("toolbar after the master went quiet = %+v", toolbar.last())
	}

	// Not following, the transport is the toolbar's again.
	a.settings.MIDIClock = false
	a.followClock()
	a.toolbarMessage([]byte(`{"type":"play"}`))
	m.advance(0.01)
	if tr := synthTransport; !tr.IsPlaying || tr.Tempo != 90 {
		t.Fatalf("after following: %+v", tr)
	}
	if msg, ok := toolbar.last().(transportMessage); !ok || msg.Clock || msg.Tempo != 90 {
		t.Fatalf("toolbar after following = %+v", toolbar.last())
	}
}

func TestClockInputRouting(t *testing.T) {
	var c clockFollower
	inputHandler(false, false, &c)([]byte{clockStart})
	inputHandler(false, true, &c)([]byte{clockContinue})
	inputHandler(false, true, &c)([]byte{clockTick})
	if playing, _ := c.state(); !playing {
		t.Fatal("continue and a clock through the input handler do not play")
	}
	inputHandler(true, false, &c)([]byte{clockStop})
	if playing, _ := c.state(); !playing {
		t.Fatal("a clock message reached the follower without following")
	}
	inputHandler(false, true, &c)([]byte{clockStop})
	if playing, _ := c.state(); playing {
		t.Fatal("stop through the input handler does not stop")
	}
}

func TestMIDIClockFlag(t *testing.T) {
	var s Settings
	if _, err := parseFlags([]string{"-midi-clock"}, "test", io.Discard, &s); err != nil || !s.MIDIClock {
		t.Fatalf("-midi-clock: %+v, %v", s, err)
	}
	s, err := LoadSettings(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.MIDIClock = true
	dir := t.TempDir()
	if err := SaveSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSettings(dir); err != nil || !got.MIDIClock {
		t.Fatalf("saved settings = %+v, %v", got, err)
	}
}
