//go:build standalone

package standalone

import (
	"fmt"
	"io"
	"strings"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// backendNull is miniaudio's ma_backend_null, a device without hardware
// that runs on a timer (tests, servers). malgo.BackendNull is off by one:
// malgo leaves out ma_backend_custom, which precedes it.
const backendNull = malgo.BackendWebaudio + 2

var backends = map[string]malgo.Backend{
	"coreaudio":  malgo.BackendCoreaudio,
	"wasapi":     malgo.BackendWasapi,
	"dsound":     malgo.BackendDsound,
	"winmm":      malgo.BackendWinmm,
	"alsa":       malgo.BackendAlsa,
	"pulseaudio": malgo.BackendPulseaudio,
	"jack":       malgo.BackendJack,
	"oss":        malgo.BackendOss,
	"sndio":      malgo.BackendSndio,
	"null":       backendNull,
}

func newContext(backend string) (*malgo.AllocatedContext, error) {
	var list []malgo.Backend
	if backend != "" {
		b, ok := backends[strings.ToLower(backend)]
		if !ok {
			return nil, fmt.Errorf("unknown audio backend %q", backend)
		}
		list = []malgo.Backend{b}
	}
	ctx, err := malgo.InitContext(list, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio: %w", err)
	}
	return ctx, nil
}

// audio is the duplex (or output-only) device of the application.
type audio struct {
	ctx         *malgo.AllocatedContext
	device      *malgo.Device
	backend     string
	output      string
	input       string
	sampleRate  int
	frames      int
	inChannels  uint32
	outChannels uint32
}

// openAudio opens the devices of the settings for a plugin with in and out
// main channels; the callback gets interleaved float32 frames.
func openAudio(s Settings, in, out uint32, callback malgo.DataProc) (*audio, error) {
	ctx, err := newContext(s.Backend)
	if err != nil {
		return nil, err
	}
	a := &audio{ctx: ctx, backend: s.Backend, frames: s.bufferSize(), inChannels: in, outChannels: out}
	if a.backend == "" {
		a.backend = "default"
	}
	if s.Input == "none" {
		a.inChannels = 0
	}
	if a.outChannels == 0 {
		// The device clock drives plugins without audio output (MIDI
		// effects, analyzers), which then play silence.
		a.outChannels = 2
	}
	kind := malgo.Playback
	if a.inChannels > 0 {
		kind = malgo.Duplex
	}
	cfg := malgo.DefaultDeviceConfig(kind)
	cfg.SampleRate = uint32(s.SampleRate)
	cfg.PeriodSizeInFrames = uint32(a.frames)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = a.outChannels
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = a.inChannels
	var free []unsafe.Pointer
	defer func() {
		for _, p := range free {
			freeC(p)
		}
	}()
	if s.Output != "" {
		info, err := findDevice(ctx, malgo.Playback, s.Output)
		if err != nil {
			a.close()
			return nil, err
		}
		cfg.Playback.DeviceID = info.ID.Pointer()
		free = append(free, cfg.Playback.DeviceID)
		a.output = info.Name()
	}
	if a.inChannels > 0 && s.Input != "" {
		info, err := findDevice(ctx, malgo.Capture, s.Input)
		if err != nil {
			a.close()
			return nil, err
		}
		cfg.Capture.DeviceID = info.ID.Pointer()
		free = append(free, cfg.Capture.DeviceID)
		a.input = info.Name()
	}
	if a.device, err = malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: callback}); err != nil {
		a.close()
		return nil, fmt.Errorf("audio device: %w", err)
	}
	a.sampleRate = int(a.device.SampleRate())
	return a, nil
}

// findDevice picks a device by exact name, else by a unique part of it.
func findDevice(ctx *malgo.AllocatedContext, kind malgo.DeviceType, name string) (malgo.DeviceInfo, error) {
	devices, err := ctx.Devices(kind)
	if err != nil {
		return malgo.DeviceInfo{}, fmt.Errorf("audio devices: %w", err)
	}
	var matches []malgo.DeviceInfo
	var names []string
	for _, d := range devices {
		if d.Name() == name {
			return d, nil
		}
		if strings.Contains(strings.ToLower(d.Name()), strings.ToLower(name)) {
			matches = append(matches, d)
		}
		names = append(names, d.Name())
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return malgo.DeviceInfo{}, fmt.Errorf("no single audio device matches %q; devices: %s", name, strings.Join(names, ", "))
}

func (a *audio) start() error {
	if err := a.device.Start(); err != nil {
		return fmt.Errorf("audio start: %w", err)
	}
	return nil
}

func (a *audio) running() bool { return a.device != nil && a.device.IsStarted() }

func (a *audio) stop() {
	if a.running() {
		_ = a.device.Stop()
	}
}

func (a *audio) close() {
	if a == nil {
		return
	}
	if a.device != nil {
		a.device.Uninit()
		a.device = nil
	}
	if a.ctx != nil {
		_ = a.ctx.Uninit()
		a.ctx.Free()
		a.ctx = nil
	}
}

func (a *audio) describe() string {
	device := func(name string) string {
		if name == "" {
			return "default"
		}
		return fmt.Sprintf("%q", name)
	}
	in := "no input"
	if a.inChannels > 0 {
		in = fmt.Sprintf("input %s (%d ch)", device(a.input), a.inChannels)
	}
	return fmt.Sprintf("audio %s: output %s (%d ch), %s, %d Hz, %d frames",
		a.backend, device(a.output), a.outChannels, in, a.sampleRate, a.frames)
}

// listDevices prints the audio devices of a backend and the MIDI ports.
func listDevices(w io.Writer, backend string) error {
	ctx, err := newContext(backend)
	if err != nil {
		return err
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()
	for _, kind := range []struct {
		name string
		kind malgo.DeviceType
	}{{"audio outputs", malgo.Playback}, {"audio inputs", malgo.Capture}} {
		devices, err := ctx.Devices(kind.kind)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s:\n", kind.name)
		for _, d := range devices {
			mark := ""
			if d.IsDefault != 0 {
				mark = " (default)"
			}
			fmt.Fprintf(w, "  %s%s\n", d.Name(), mark)
		}
	}
	ins, outs, err := midiPortNames()
	if err != nil {
		fmt.Fprintf(w, "MIDI: %v\n", err)
		return nil
	}
	fmt.Fprintln(w, "MIDI inputs:")
	for _, name := range ins {
		fmt.Fprintf(w, "  %s\n", name)
	}
	fmt.Fprintln(w, "MIDI outputs:")
	for _, name := range outs {
		fmt.Fprintf(w, "  %s\n", name)
	}
	return nil
}
