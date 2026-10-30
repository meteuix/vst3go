//go:build standalone

package standalone

/*
#cgo linux LDFLAGS: -ldl -lpthread -lm
#cgo amd64 CFLAGS: -msse2
#include <stdlib.h>
#include "audio.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unsafe"
)

// audioAPI opens an audio API (see vst3go_sa_audio_new) and lists its
// devices.
func audioAPI(backend string) (*C.VST3GoSAAudio, error) {
	name := C.CString(backend)
	defer C.free(unsafe.Pointer(name))
	if backend != "" && !C.vst3go_sa_audio_backend_known(name) {
		return nil, fmt.Errorf("unknown audio backend %q", backend)
	}
	var msg *C.char
	api := C.vst3go_sa_audio_new(name, &msg)
	if api == nil {
		return nil, fmt.Errorf("audio: %s", C.GoString(msg))
	}
	return api, nil
}

// render renders one period as the device's callback does (see
// vst3go_sa_audio_render); the tests drive it, as _test files cannot use
// cgo.
func render(out, in []float32, inChannels, outChannels, frames uint32) {
	var inPtr, outPtr *C.float
	if len(in) > 0 {
		inPtr = (*C.float)(unsafe.Pointer(&in[0]))
	}
	if len(out) > 0 {
		outPtr = (*C.float)(unsafe.Pointer(&out[0]))
	}
	C.vst3go_sa_audio_render(inPtr, C.uint32_t(inChannels), outPtr, C.uint32_t(outChannels), C.uint32_t(frames))
}

// midiInput serializes the MIDI input callbacks of every port: the ring
// takes one producer.
var midiInput sync.Mutex

// pushMIDI queues a message for the next block; it returns false when the
// ring is full (the audio thread is behind).
func pushMIDI(b [3]byte) bool {
	midiInput.Lock()
	defer midiInput.Unlock()
	return bool(C.vst3go_sa_midi_in_push((*C.uint8_t)(&b[0])))
}

// popMIDI takes a message the plugin output; one goroutine at a time.
func popMIDI() (b [3]byte, ok bool) {
	ok = bool(C.vst3go_sa_midi_out_pop((*C.uint8_t)(&b[0])))
	return b, ok
}

// deviceNames returns the output (capture false) or input devices of an
// API and the index of the default one (-1 without one).
func deviceNames(api *C.VST3GoSAAudio, capture bool) (names []string, def int) {
	def = -1
	for i := range uint32(C.vst3go_sa_audio_device_count(api, C.bool(capture))) {
		names = append(names, C.GoString(C.vst3go_sa_audio_device_name(api, C.bool(capture), C.uint32_t(i))))
		if C.vst3go_sa_audio_device_default(api, C.bool(capture), C.uint32_t(i)) {
			def = int(i)
		}
	}
	return names, def
}

// audio is the duplex (or output-only) device of the application.
type audio struct {
	api         *C.VST3GoSAAudio
	backend     string
	output      string
	input       string
	sampleRate  int
	frames      int
	inChannels  uint32
	outChannels uint32
}

// openAudio opens the devices of the settings for a plugin with in and out
// main channels.
func openAudio(s Settings, in, out uint32) (*audio, error) {
	api, err := audioAPI(s.Backend)
	if err != nil {
		return nil, err
	}
	a := &audio{api: api, backend: s.Backend, frames: s.bufferSize(), inChannels: in, outChannels: out}
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
	output, input := -1, -1
	if s.Output != "" {
		if output, a.output, err = findDevice(api, false, s.Output); err != nil {
			a.close()
			return nil, err
		}
	}
	if a.inChannels > 0 && s.Input != "" {
		if input, a.input, err = findDevice(api, true, s.Input); err != nil {
			a.close()
			return nil, err
		}
	}
	if msg := C.vst3go_sa_audio_open(api, C.int32_t(output), C.int32_t(input), C.uint32_t(s.SampleRate), C.uint32_t(a.frames),
		C.uint32_t(a.inChannels), C.uint32_t(a.outChannels)); msg != nil {
		a.close()
		return nil, fmt.Errorf("audio device: %s", C.GoString(msg))
	}
	a.sampleRate = int(C.vst3go_sa_audio_sample_rate(api))
	return a, nil
}

// findDevice picks a device by exact name, else by a unique part of it.
func findDevice(api *C.VST3GoSAAudio, capture bool, name string) (int, string, error) {
	names, _ := deviceNames(api, capture)
	var matches []int
	for i, n := range names {
		if n == name {
			return i, n, nil
		}
		if strings.Contains(strings.ToLower(n), strings.ToLower(name)) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return matches[0], names[matches[0]], nil
	}
	return 0, "", fmt.Errorf("no single audio device matches %q; devices: %s", name, strings.Join(names, ", "))
}

func (a *audio) start() error {
	if msg := C.vst3go_sa_audio_start(a.api); msg != nil {
		return errors.New("audio start: " + C.GoString(msg))
	}
	return nil
}

func (a *audio) running() bool { return a.api != nil && bool(C.vst3go_sa_audio_running(a.api)) }

func (a *audio) stop() { C.vst3go_sa_audio_stop(a.api) }

func (a *audio) close() {
	if a == nil || a.api == nil {
		return
	}
	C.vst3go_sa_audio_free(a.api)
	a.api = nil
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
	api, err := audioAPI(backend)
	if err != nil {
		return err
	}
	defer C.vst3go_sa_audio_free(api)
	for _, kind := range []struct {
		name    string
		capture bool
	}{{"audio outputs", false}, {"audio inputs", true}} {
		names, def := deviceNames(api, kind.capture)
		fmt.Fprintf(w, "%s:\n", kind.name)
		for i, name := range names {
			mark := ""
			if i == def {
				mark = " (default)"
			}
			fmt.Fprintf(w, "  %s%s\n", name, mark)
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
