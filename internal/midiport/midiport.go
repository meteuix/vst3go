//go:build standalone

// Package midiport opens the MIDI ports of the system MIDI API (Core MIDI,
// the ALSA sequencer, WinMM) for the standalone applications: listing,
// inputs, virtual inputs other applications play (Core MIDI and ALSA) and
// outputs. Inputs deliver complete channel, system common and real-time
// messages; SysEx is dropped.
package midiport

/*
#include <stdlib.h>
#include "midiport.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

// ErrUnsupported reports a feature the platform's MIDI API lacks (virtual
// ports on Windows).
var ErrUnsupported = errors.New("not supported by the system MIDI API")

var (
	// mu serializes the calls into the MIDI API.
	mu      sync.Mutex
	inited  bool
	initErr error
	nextID  uintptr

	// inputsMu guards inputs, which the MIDI threads read; it is never
	// held while calling the MIDI API, so closing a port cannot wait on a
	// callback that waits on the lock.
	inputsMu sync.RWMutex
	inputs   = map[uintptr]*Input{}
)

func apiError(code C.int) error {
	if code == C.VST3GO_MIDI_UNSUPPORTED {
		return ErrUnsupported
	}
	return errors.New(C.GoString(C.vst3go_midi_error(code)))
}

// Init connects to the system MIDI API under a client name; the first call
// wins. The other functions call it with "vst3go".
func Init(clientName string) error {
	mu.Lock()
	defer mu.Unlock()
	return initLocked(clientName)
}

func initLocked(clientName string) error {
	if !inited {
		inited = true
		name := C.CString(clientName)
		defer C.free(unsafe.Pointer(name))
		if code := C.vst3go_midi_init(name); code < 0 {
			initErr = fmt.Errorf("open the system MIDI API: %w", apiError(code))
		}
	}
	return initErr
}

func ports(input bool) ([]string, error) {
	if err := initLocked("vst3go"); err != nil {
		return nil, err
	}
	n := int(C.vst3go_midi_count(C.bool(input)))
	names := make([]string, 0, n)
	var buf [256]C.char
	for i := range n {
		if C.vst3go_midi_name(C.bool(input), C.int(i), &buf[0], C.int(len(buf))) < 0 {
			continue
		}
		names = append(names, C.GoString(&buf[0]))
	}
	return names, nil
}

// Inputs lists the MIDI sources an application can read.
func Inputs() ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	return ports(true)
}

// Outputs lists the MIDI destinations an application can play.
func Outputs() ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	return ports(false)
}

// find returns the index of a port by exact name, else by a unique part of
// the name (case-insensitive), and its full name.
func find(input bool, name string) (int, string, error) {
	names, err := ports(input)
	if err != nil {
		return 0, "", err
	}
	match := -1
	for i, n := range names {
		if n == name {
			return i, n, nil
		}
		if strings.Contains(strings.ToLower(n), strings.ToLower(name)) {
			if match >= 0 {
				return 0, "", fmt.Errorf("MIDI port %q is ambiguous: %q, %q", name, names[match], n)
			}
			match = i
		}
	}
	if match < 0 {
		return 0, "", fmt.Errorf("no MIDI port %q", name)
	}
	return match, names[match], nil
}

// Input is an open MIDI input.
type Input struct {
	Name    string
	id      uintptr
	handle  unsafe.Pointer
	receive func(msg []byte)
	parser  parser
}

func openInput(name string, receive func([]byte), open func(id uintptr, handle *unsafe.Pointer) C.int) (*Input, error) {
	nextID++
	in := &Input{Name: name, id: nextID, receive: receive}
	inputsMu.Lock()
	inputs[in.id] = in
	inputsMu.Unlock()
	// The handle comes back through a pointer to memory without Go
	// pointers, as cgo requires.
	var handle unsafe.Pointer
	code := open(in.id, &handle)
	in.handle = handle
	if code < 0 {
		inputsMu.Lock()
		delete(inputs, in.id)
		inputsMu.Unlock()
		return nil, fmt.Errorf("MIDI input %q: %w", name, apiError(code))
	}
	return in, nil
}

// OpenInput opens an input by name (exact or a unique part); receive gets
// every message on a MIDI thread and must not keep msg.
func OpenInput(name string, receive func(msg []byte)) (*Input, error) {
	mu.Lock()
	defer mu.Unlock()
	index, full, err := find(true, name)
	if err != nil {
		return nil, err
	}
	return openInput(full, receive, func(id uintptr, handle *unsafe.Pointer) C.int {
		return C.vst3go_midi_open_input(C.int(index), C.uintptr_t(id), handle)
	})
}

// OpenVirtualInput creates an input other applications see as a MIDI
// destination called name.
func OpenVirtualInput(name string, receive func(msg []byte)) (*Input, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := initLocked("vst3go"); err != nil {
		return nil, err
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return openInput(name, receive, func(id uintptr, handle *unsafe.Pointer) C.int {
		return C.vst3go_midi_open_virtual_input(cname, C.uintptr_t(id), handle)
	})
}

// Close stops the input; no new receive call starts after it returns.
func (in *Input) Close() {
	mu.Lock()
	defer mu.Unlock()
	if in.handle == nil {
		return
	}
	inputsMu.Lock()
	delete(inputs, in.id)
	inputsMu.Unlock()
	C.vst3go_midi_close_input(in.handle)
	in.handle = nil
}

//export goMIDIPortReceive
func goMIDIPortReceive(id C.uintptr_t, data *C.uint8_t, size C.int) {
	inputsMu.RLock()
	in := inputs[uintptr(id)]
	inputsMu.RUnlock()
	if in == nil || size <= 0 {
		return
	}
	in.parser.feed(unsafe.Slice((*byte)(unsafe.Pointer(data)), int(size)), in.receive)
}

// Output is an open MIDI output.
type Output struct {
	Name   string
	handle unsafe.Pointer
}

// OpenOutput opens an output by name (exact or a unique part).
func OpenOutput(name string) (*Output, error) {
	mu.Lock()
	defer mu.Unlock()
	index, full, err := find(false, name)
	if err != nil {
		return nil, err
	}
	var handle unsafe.Pointer
	if code := C.vst3go_midi_open_output(C.int(index), &handle); code < 0 {
		return nil, fmt.Errorf("MIDI output %q: %w", full, apiError(code))
	}
	return &Output{Name: full, handle: handle}, nil
}

// Send sends one complete short message.
func (o *Output) Send(msg []byte) error {
	if len(msg) == 0 || len(msg) > 3 {
		return fmt.Errorf("MIDI output %q: % x is not a short message", o.Name, msg)
	}
	if code := C.vst3go_midi_send(o.handle, (*C.uint8_t)(unsafe.Pointer(&msg[0])), C.int(len(msg))); code < 0 {
		return fmt.Errorf("MIDI output %q: %w", o.Name, apiError(code))
	}
	return nil
}

func (o *Output) Close() {
	mu.Lock()
	defer mu.Unlock()
	if o.handle != nil {
		C.vst3go_midi_close_output(o.handle)
		o.handle = nil
	}
}
