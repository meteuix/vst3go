//go:build standalone

package standalone

/*
#cgo CFLAGS: -I${SRCDIR}/../../include
#cgo darwin CFLAGS: -x objective-c
#cgo darwin LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "host.h"
#include "platform.h"
*/
import "C"

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	// The application hosts the plugin through its clap_entry.
	_ "github.com/th-release/vst3go/pkg/plugin"
)

// The main goroutine owns the main thread: the run loop and the editor
// live there.
func init() { runtime.LockOSThread() }

const (
	tickInterval = 10 * time.Millisecond
	maxMIDI      = 512
	stopTimeout  = time.Second
)

// Main runs the standalone application of the registered plugin until the
// window closes or the process gets SIGINT/SIGTERM. Call it from main.
func Main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// options are the command-line flags of the application.
type options struct {
	list     bool
	plugin   string
	headless bool
	reset    bool
	config   string
	duration time.Duration
}

func parseFlags(args []string, name string, out io.Writer, s *Settings) (options, error) {
	var o options
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&o.list, "list", false, "list audio devices and MIDI ports, then exit")
	fs.StringVar(&o.plugin, "plugin", "", "plugin to run, by CLAP ID or name (default: the first)")
	fs.BoolVar(&o.headless, "headless", false, "run without the editor window")
	fs.BoolVar(&o.reset, "reset", false, "start from the plugin's default state")
	fs.StringVar(&o.config, "config", "", "settings and state directory (default: the user config directory)")
	fs.DurationVar(&o.duration, "duration", 0, "quit after this long (0: run until closed)")
	backend := fs.String("backend", "", "audio API: coreaudio, wasapi, dsound, winmm, alsa, pulseaudio, jack, oss, null")
	output := fs.String("output", "", "audio output device name")
	input := fs.String("input", "", `audio input device name, "none" for no input`)
	rate := fs.Int("sample-rate", 0, "sample rate in Hz (0: the device rate)")
	buffer := fs.Int("buffer", 0, "buffer size in frames (default 256)")
	midiIn := fs.String("midi-in", "", `MIDI inputs: "all", "none" or comma-separated port names`)
	midiOut := fs.String("midi-out", "", "MIDI output port for the plugin's MIDI output")
	virtual := fs.Bool("virtual-midi", true, "open a virtual MIDI input named after the plugin (macOS, Linux)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	// Flags given override the saved settings.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "backend":
			s.Backend = *backend
		case "output":
			s.Output = *output
		case "input":
			s.Input = *input
		case "sample-rate":
			s.SampleRate = *rate
		case "buffer":
			s.BufferSize = *buffer
		case "midi-in":
			s.MIDIInput = *midiIn
		case "midi-out":
			s.MIDIOutput = *midiOut
		case "virtual-midi":
			s.NoVirtualMIDI = !*virtual
		}
	})
	if s.SampleRate < 0 || s.BufferSize < 0 {
		return o, errors.New("sample rate and buffer size must not be negative")
	}
	return o, nil
}

// createPlugin creates and initializes the plugin of a descriptor index;
// the process hosts one plugin at a time.
func createPlugin(index uint32) error {
	if msg := C.vst3go_sa_create(C.uint32_t(index)); msg != nil {
		return errors.New(C.GoString(msg))
	}
	return nil
}

func destroyPlugin() { C.vst3go_sa_destroy() }

// mainChannels returns the channels of the plugin's main audio ports.
func mainChannels() (in, out uint32) {
	return uint32(C.vst3go_sa_main_channels(true)), uint32(C.vst3go_sa_main_channels(false))
}

func activatePlugin(sampleRate, frames int) error {
	if !C.vst3go_sa_activate(C.double(sampleRate), C.uint32_t(frames)) {
		return fmt.Errorf("activate at %d Hz, %d frames failed", sampleRate, frames)
	}
	return nil
}

// pluginIndex finds the descriptor to run by CLAP ID or name.
func pluginIndex(key string) (uint32, *C.clap_plugin_descriptor_t, error) {
	count := uint32(C.vst3go_sa_plugin_count())
	var names []string
	for i := range count {
		desc := C.vst3go_sa_plugin_descriptor(C.uint32_t(i))
		if desc == nil {
			continue
		}
		id, name := C.GoString(desc.id), C.GoString(desc.name)
		if key == "" || key == id || strings.EqualFold(key, name) {
			return i, desc, nil
		}
		names = append(names, fmt.Sprintf("%s (%s)", name, id))
	}
	if count == 0 {
		return 0, nil, errors.New("no plugin registered (plugin.Register)")
	}
	return 0, nil, fmt.Errorf("no plugin %q; registered: %s", key, strings.Join(names, ", "))
}

// app is the running application; the main thread owns it, the audio
// callback only touches the fields marked.
type app struct {
	name     string
	dir      string
	settings Settings
	audio    *audio
	midi     *midiPorts
	gui      bool
	quit     atomic.Bool
	failed   error

	// audio thread
	midiIn     chan [3]byte
	midiOut    chan [3]byte
	midiBuf    [maxMIDI * 3]byte
	midiOutBuf [maxMIDI * 3]byte
}

var current *app

//export goStandaloneTick
func goStandaloneTick() {
	if current != nil && current.tick() {
		C.vst3go_sa_quit()
	}
}

func run(args []string, log io.Writer) error {
	name := filepath.Base(os.Args[0])
	// Flags are parsed twice: first for -plugin and -config, then over the
	// saved settings of that plugin.
	var probe Settings
	o, err := parseFlags(args, name, io.Discard, &probe)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = parseFlags(args, name, log, &probe)
		return nil
	}
	if err != nil {
		_, _ = parseFlags(args, name, log, &probe)
		return err
	}
	index, desc, err := pluginIndex(o.plugin)
	if err != nil {
		return err
	}
	a := &app{name: C.GoString(desc.name), midiIn: make(chan [3]byte, 1024), midiOut: make(chan [3]byte, 1024)}
	a.dir = o.config
	if a.dir == "" {
		if a.dir, err = ConfigDir(C.GoString(desc.vendor), a.name); err != nil {
			return err
		}
	}
	if a.settings, err = LoadSettings(a.dir); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if o, err = parseFlags(args, name, log, &a.settings); err != nil {
		return err
	}
	if o.list {
		return listDevices(log, a.settings.Backend)
	}

	if err := createPlugin(index); err != nil {
		return fmt.Errorf("%s: %w", a.name, err)
	}
	defer destroyPlugin()
	if !o.reset {
		if err := a.loadState(); err != nil {
			fmt.Fprintf(log, "%s: ignoring the saved state: %v\n", a.name, err)
		}
	}
	if err := SaveSettings(a.dir, a.settings); err != nil {
		fmt.Fprintf(log, "%s: settings not saved: %v\n", a.name, err)
	}

	in, out := mainChannels()
	if a.audio, err = openAudio(a.settings, in, out, a.process); err != nil {
		return err
	}
	defer a.audio.close()
	if err := a.startAudio(); err != nil {
		return err
	}
	defer a.stopAudio()

	if a.midi, err = openMIDI(a.settings, a.name, bool(C.vst3go_sa_note_input()), a.midiIn, a.midiOut); err != nil {
		fmt.Fprintf(log, "%s: MIDI: %v\n", a.name, err)
	}
	defer a.midi.close()

	editor := "no editor"
	if !o.headless {
		if w, h, ok := a.openEditor(); ok {
			editor = fmt.Sprintf("editor %dx%d", w, h)
			defer C.vst3go_sa_window_close()
			defer C.vst3go_sa_gui_destroy()
		}
	}
	fmt.Fprintf(log, "%s: %s, %s, %s, settings in %s\n", a.name, a.audio.describe(), a.midi.describe(), editor, a.dir)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		a.quit.Store(true)
	}()
	if o.duration > 0 {
		timer := time.AfterFunc(o.duration, func() { a.quit.Store(true) })
		defer timer.Stop()
	}

	current = a
	defer func() { current = nil }()
	if a.gui {
		C.vst3go_sa_run(C.uint32_t(tickInterval / time.Millisecond))
	} else {
		ticker := time.NewTicker(tickInterval)
		for range ticker.C {
			if a.tick() {
				break
			}
		}
		ticker.Stop()
	}

	// Stop the audio before the editor goes, then keep the state.
	a.stopAudio()
	if err := a.saveState(); err != nil {
		fmt.Fprintf(log, "%s: state not saved: %v\n", a.name, err)
	}
	return a.failed
}

// openEditor opens the plugin's editor in the application window.
func (a *app) openEditor() (width, height uint32, ok bool) {
	api := C.vst3go_sa_window_api()
	if api == nil {
		return 0, 0, false
	}
	var w, h C.uint32_t
	var resizable C.bool
	if !C.vst3go_sa_gui_create(api, C.vst3go_sa_window_scale(), &w, &h, &resizable) {
		return 0, 0, false
	}
	title := C.CString(a.name)
	defer C.free(unsafe.Pointer(title))
	var window C.clap_window_t
	if !C.vst3go_sa_window_open(title, w, h, resizable, &window) {
		C.vst3go_sa_gui_destroy()
		return 0, 0, false
	}
	if !C.vst3go_sa_gui_attach(&window) {
		C.vst3go_sa_gui_destroy()
		C.vst3go_sa_window_close()
		return 0, 0, false
	}
	a.gui = true
	return uint32(w), uint32(h), true
}

// tick runs on the main thread every tickInterval and reports whether to
// quit.
func (a *app) tick() bool {
	if C.vst3go_sa_service()&C.VST3GO_SA_RESTART != 0 {
		// The plugin changed its latency or ports while active.
		a.stopAudio()
		if err := a.startAudio(); err != nil {
			a.failed = fmt.Errorf("%s: restart: %w", a.name, err)
			return true
		}
	}
	return a.quit.Load()
}

func (a *app) startAudio() error {
	if err := activatePlugin(a.audio.sampleRate, a.audio.frames); err != nil {
		return fmt.Errorf("%s: %w", a.name, err)
	}
	if err := a.audio.start(); err != nil {
		C.vst3go_sa_deactivate()
		return err
	}
	return nil
}

// stopAudio lets the audio thread stop processing, stops the device and
// deactivates the plugin.
func (a *app) stopAudio() {
	if a.audio == nil || !a.audio.running() {
		C.vst3go_sa_deactivate()
		return
	}
	C.vst3go_sa_request_stop()
	for deadline := time.Now().Add(stopTimeout); !bool(C.vst3go_sa_stopped()) && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	a.audio.stop()
	C.vst3go_sa_deactivate()
}

// process is the audio callback: interleaved float32 in and out.
func (a *app) process(out, in []byte, frames uint32) {
	n := 0
drain:
	for n < maxMIDI {
		select {
		case m := <-a.midiIn:
			copy(a.midiBuf[3*n:], m[:])
			n++
		default:
			break drain
		}
	}
	var inPtr, outPtr *C.float
	if len(in) > 0 {
		inPtr = (*C.float)(unsafe.Pointer(&in[0]))
	}
	if len(out) > 0 {
		outPtr = (*C.float)(unsafe.Pointer(&out[0]))
	}
	count := C.vst3go_sa_process(inPtr, C.uint32_t(a.audio.inChannels), outPtr, C.uint32_t(a.audio.outChannels), C.uint32_t(frames),
		(*C.uint8_t)(unsafe.Pointer(&a.midiBuf[0])), C.uint32_t(n), (*C.uint8_t)(unsafe.Pointer(&a.midiOutBuf[0])), maxMIDI)
	for i := range int(count) {
		select {
		case a.midiOut <- [3]byte(a.midiOutBuf[3*i : 3*i+3]):
		default:
		}
	}
}

func (a *app) statePath() string { return filepath.Join(a.dir, stateFile) }

func (a *app) loadState() error {
	data, err := os.ReadFile(a.statePath())
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(data) == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	if !C.vst3go_sa_state_load((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))) {
		return errors.New("the plugin rejected it")
	}
	return nil
}

func (a *app) saveState() error {
	var data *C.uint8_t
	var size C.size_t
	if !C.vst3go_sa_state_save(&data, &size) {
		return errors.New("the plugin has no state")
	}
	defer C.free(unsafe.Pointer(data))
	return writeFile(a.statePath(), C.GoBytes(unsafe.Pointer(data), C.int(size)))
}

// freeC frees C memory (malgo device IDs).
func freeC(p unsafe.Pointer) { C.free(p) }
