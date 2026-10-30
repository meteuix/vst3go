// Package granular provides granular synthesis over a recorded buffer:
// short Hann-windowed grains read from the buffer at a chosen position,
// pitch and density, scattered in time, position, pitch and stereo.
package granular

import (
	"math"

	"github.com/meteuix/vst3go/v2/pkg/dsp/fastmath"
	"github.com/meteuix/vst3go/v2/pkg/dsp/interpolation"
)

type grain struct {
	active bool
	pos    float64 // read position in the buffer, in samples
	step   float64 // read increment per output sample
	age    int
	length int
	gainL  float32
	gainR  float32
}

// Granulator records input into a circular buffer and plays grains from
// it. Position is how far behind the newest input a grain starts, so with
// recording on it is a granular delay; Freeze stops recording to hold the
// buffer, and Load fills it with a sample to granulate. Grains start at
// Density per second (with Spray randomizing their start positions,
// Jitter their onset times, PitchSpray their pitch and Spread their
// panning) and are normalized for overlap so the level stays about the
// same at any density.
//
// Every grain is preallocated; Process does not allocate.
type Granulator struct {
	sampleRate float64
	buf        []float32
	write      int
	filled     int
	frozen     bool
	grains     []grain

	size       float64 // seconds
	density    float64 // grains per second
	position   float64 // seconds behind the write head
	spray      float64 // seconds
	pitch      float64 // ratio
	pitchSpray float64 // semitones
	spread     float64 // 0..1
	jitter     float64 // 0..1

	untilNext float64 // samples until the next grain
	rng       uint64
}

// New returns a Granulator with a buffer of bufferSeconds and room for
// maxGrains overlapping grains.
func New(sampleRate, bufferSeconds float64, maxGrains int) *Granulator {
	return &Granulator{
		sampleRate: sampleRate,
		buf:        make([]float32, max(4, int(bufferSeconds*sampleRate))),
		grains:     make([]grain, max(1, maxGrains)),
		size:       0.08,
		density:    20,
		position:   0.2,
		pitch:      1,
		rng:        0x9E3779B97F4A7C15,
	}
}

// SetSize sets the grain length in seconds (5 ms to 1 s).
func (g *Granulator) SetSize(seconds float64) { g.size = max(0.005, min(1, seconds)) }

// SetDensity sets how many grains start per second (0.5 to 500).
func (g *Granulator) SetDensity(perSecond float64) {
	g.density = max(0.5, min(500, perSecond))
}

// SetPosition sets how far behind the newest input grains start, in
// seconds; it is kept inside the buffer.
func (g *Granulator) SetPosition(seconds float64) { g.position = max(0, seconds) }

// SetSpray sets the random spread of grain start positions in seconds.
func (g *Granulator) SetSpray(seconds float64) { g.spray = max(0, seconds) }

// SetPitch sets the grain playback ratio (2 is an octave up, 0.25..4).
func (g *Granulator) SetPitch(ratio float64) { g.pitch = max(0.25, min(4, ratio)) }

// SetPitchSpray sets the random pitch deviation in semitones.
func (g *Granulator) SetPitchSpray(semitones float64) { g.pitchSpray = max(0, semitones) }

// SetSpread sets the random stereo spread (0 centred, 1 anywhere).
func (g *Granulator) SetSpread(amount float64) { g.spread = max(0, min(1, amount)) }

// SetJitter randomizes onset times (0 regular, 1 random).
func (g *Granulator) SetJitter(amount float64) { g.jitter = max(0, min(1, amount)) }

// Freeze stops (true) or resumes (false) recording input.
func (g *Granulator) Freeze(frozen bool) { g.frozen = frozen }

// Frozen reports whether recording is stopped.
func (g *Granulator) Frozen() bool { return g.frozen }

// Load replaces the buffer contents with sample (truncated to the buffer
// length), ends recording, and places the write head after the sample so
// Position counts back from its end.
func (g *Granulator) Load(sample []float32) {
	clear(g.buf)
	n := copy(g.buf, sample)
	g.write = n % len(g.buf)
	g.filled = n
	g.frozen = true
}

// Process records in (unless frozen) and writes the grains to outL and
// outR (same length as in; in may alias either output).
func (g *Granulator) Process(in, outL, outR []float32) {
	n := len(g.buf)
	length := max(2, int(g.size*g.sampleRate))
	interval := g.sampleRate / g.density
	// Each grain is a Hann window with mean 1/2; at density·size grains
	// overlapping, this keeps the average level near the input level.
	norm := float32(1 / max(1, 0.5*g.density*g.size))
	for i := range in {
		if !g.frozen {
			g.buf[g.write] = in[i]
			g.write = (g.write + 1) % n
			g.filled = min(n, g.filled+1)
		}
		g.untilNext--
		if g.untilNext <= 0 {
			g.start(length)
			g.untilNext += interval * (1 + g.jitter*(2*g.random()-1))
		}
		var l, r float32
		for j := range g.grains {
			gr := &g.grains[j]
			if !gr.active {
				continue
			}
			x := g.read(gr.pos)
			phase := float64(gr.age) / float64(gr.length)
			w := float32(0.5 - 0.5*fastmath.Cos2Pi(phase))
			l += x * w * gr.gainL
			r += x * w * gr.gainR
			gr.pos += gr.step
			gr.age++
			if gr.age >= gr.length {
				gr.active = false
			}
		}
		outL[i] = l * norm
		outR[i] = r * norm
	}
}

// start launches a grain if a slot is free.
func (g *Granulator) start(length int) {
	if g.filled < 4 {
		return
	}
	var gr *grain
	for j := range g.grains {
		if !g.grains[j].active {
			gr = &g.grains[j]
			break
		}
	}
	if gr == nil {
		return
	}
	step := g.pitch * math.Exp2(g.pitchSpray*(2*g.random()-1)/12)
	// While recording, a grain starts far enough back that it never reads
	// past the newest input and recent enough that the input does not
	// overwrite what it has still to read; the unrecorded part of the
	// buffer is silence, so the delay is exact from the first sample.
	// Frozen, it stays within what has been recorded.
	back := (g.position + g.spray*g.random()) * g.sampleRate
	if g.frozen {
		back = min(float64(g.filled-2), back)
	} else {
		back = max(back, (step-1)*float64(length)+2)
		back = min(float64(len(g.buf)-4)-max(0, (1-step)*float64(length)), back)
	}
	back = max(2, back)
	pan := 0.5 + g.spread*(g.random()-0.5)
	*gr = grain{
		active: true,
		pos:    float64(g.write-1) - back,
		step:   step,
		length: length,
		gainL:  float32(math.Cos(pan * math.Pi / 2)),
		gainR:  float32(math.Sin(pan * math.Pi / 2)),
	}
	// Constant-power panning, normalized so a centred grain has unity gain
	// in each channel.
	gr.gainL *= math.Sqrt2
	gr.gainR *= math.Sqrt2
}

// read interpolates the buffer at a (possibly negative or wrapped)
// position.
func (g *Granulator) read(pos float64) float32 {
	n := len(g.buf)
	i := int(math.Floor(pos))
	f := float32(pos - float64(i))
	at := func(k int) float32 { return g.buf[((k%n)+n)%n] }
	return interpolation.Hermite(at(i-1), at(i), at(i+1), at(i+2), f)
}

// random returns a uniform number in [0, 1).
func (g *Granulator) random() float64 {
	g.rng ^= g.rng << 13
	g.rng ^= g.rng >> 7
	g.rng ^= g.rng << 17
	return float64(g.rng>>11) / (1 << 53)
}

// ActiveGrains returns how many grains are playing.
func (g *Granulator) ActiveGrains() int {
	n := 0
	for _, gr := range g.grains {
		if gr.active {
			n++
		}
	}
	return n
}

// Reset stops all grains and clears the buffer.
func (g *Granulator) Reset() {
	for j := range g.grains {
		g.grains[j].active = false
	}
	clear(g.buf)
	g.write, g.filled, g.untilNext = 0, 0, 0
}
