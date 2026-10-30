//go:build standalone

package standalone

/*
#include "host.h"
*/
import "C"

import (
	"math"
	"sync"
	"time"
)

// MIDI clock (MIDI 1.0 real-time messages): a master sends 24 clocks per
// quarter note, start, continue, stop and the song position pointer.
const (
	clockTick     = 0xF8
	clockStart    = 0xFA
	clockContinue = 0xFB
	clockStop     = 0xFC
	songPosition  = 0xF2
	clocksPerBeat = 24
	// clockWindow is how many clocks the tempo comes from: two quarter
	// notes, which follows tempo changes within a beat or two and keeps
	// the jitter of their arrival out of the tempo.
	clockWindow = 2 * clocksPerBeat
	// clockMinCount clocks (a 16th note) give the first tempo.
	clockMinCount = 6
	// A gap of clockGap restarts the tempo measurement.
	clockGap = 500 * time.Millisecond
	// The master is gone after clockTimeoutClocks clock intervals without a
	// clock, at least clockMinTimeout: the transport stops.
	clockTimeoutClocks = 3
	clockMinTimeout    = 50 * time.Millisecond
)

// clockFollower makes the transport follow a MIDI clock master. Messages
// come from MIDI input threads; the main thread reads the state for the
// toolbar and times the master out.
type clockFollower struct {
	mu      sync.Mutex
	times   [clockWindow]time.Time // arrival of the last clocks, a ring
	count   int                    // clocks in times
	next    int                    // ring index of the next clock
	last    time.Time              // the last clock's arrival
	tempo   float64                // BPM, 0 until measured
	clocks  int64                  // the master's position in clocks
	armed   bool                   // start or continue: playing from the next clock
	playing bool
}

// message takes a real-time or song position message that arrived at now.
func (c *clockFollower) message(msg []byte, now time.Time) {
	if len(msg) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch msg[0] {
	case clockTick:
		c.measure(now)
		switch {
		case c.armed:
			// The first clock after start or continue is the position the
			// master starts from.
			c.armed, c.playing = false, true
			C.vst3go_sa_clock_locate(C.double(c.position()))
		case c.playing:
			c.clocks++
		}
		C.vst3go_sa_clock(C.bool(c.playing), C.double(c.tempo), C.double(c.position()))
	case clockStart:
		c.clocks, c.armed, c.playing = 0, true, false
		c.locate()
	case clockContinue:
		c.armed, c.playing = true, false
		c.locate()
	case clockStop:
		c.armed = false
		c.locate()
	case songPosition:
		if len(msg) == 3 {
			// Song position counts 16th notes, 6 clocks each.
			c.clocks = int64(msg[1]&0x7F|(msg[2]&0x7F)<<7) * 6
			c.locate()
		}
	}
}

// measure adds a clock's arrival to the tempo measurement: the slope of a
// least-squares line through the arrival times of the window.
func (c *clockFollower) measure(now time.Time) {
	if c.count > 0 && now.Sub(c.last) > clockGap {
		c.count = 0
	}
	c.last = now
	c.times[c.next] = now
	c.next = (c.next + 1) % clockWindow
	c.count = min(c.count+1, clockWindow)
	if c.count < clockMinCount {
		return
	}
	n := float64(c.count)
	first := c.times[(c.next-c.count+clockWindow)%clockWindow]
	var sumT, sumIT float64
	for i := range c.count {
		t := c.times[(c.next-c.count+i+clockWindow)%clockWindow].Sub(first).Seconds()
		sumT += t
		sumIT += float64(i) * t
	}
	meanI := (n - 1) / 2
	// Σ(i-meanI)² over 0..n-1 is n(n²-1)/12.
	interval := (sumIT - meanI*sumT) / (n * (n*n - 1) / 12)
	if interval <= 0 {
		return
	}
	if tempo := 60 / (interval * clocksPerBeat); tempo >= minTempo && tempo <= maxTempo {
		c.tempo = tempo
	}
}

func (c *clockFollower) position() float64 { return float64(c.clocks) / clocksPerBeat }

// locate stops the transport at the master's position; after start or
// continue, the next clock plays on.
func (c *clockFollower) locate() {
	c.playing = false
	C.vst3go_sa_clock(false, C.double(c.tempo), C.double(c.position()))
	C.vst3go_sa_clock_locate(C.double(c.position()))
}

// check stops the transport when the master stopped sending clocks
// without a stop message; it runs on the main thread.
func (c *clockFollower) check(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.playing {
		return
	}
	timeout := clockMinTimeout
	if c.tempo > 0 {
		interval := time.Duration(float64(time.Minute) / (c.tempo * clocksPerBeat))
		timeout = max(timeout, clockTimeoutClocks*interval)
	}
	if now.Sub(c.last) > timeout {
		c.locate()
	}
}

// state returns whether the master plays and its tempo (0 until measured).
func (c *clockFollower) state() (playing bool, tempo float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.playing, c.tempo
}

// reset forgets the master, for when following starts.
func (c *clockFollower) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count, c.next, c.tempo, c.clocks, c.armed, c.playing = 0, 0, 0, 0, false, false
}

// roundTempo rounds a tempo to the toolbar's 0.1 BPM.
func roundTempo(tempo float64) float64 { return math.Round(tempo*10) / 10 }
