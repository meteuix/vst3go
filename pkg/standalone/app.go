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
	"math"
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
	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

// The main goroutine owns the main thread: the run loop and the editor
// live there.
func init() { runtime.LockOSThread() }

const (
	tickInterval = 10 * time.Millisecond
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
	play     bool
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
	tempo := fs.Float64("tempo", 0, "transport tempo in BPM (default 120)")
	signature := fs.String("time-signature", "", `transport time signature, such as "3/4" (default 4/4)`)
	fs.BoolVar(&o.play, "play", false, "start the transport playing")
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
		case "tempo":
			s.Tempo = *tempo
		}
	})
	if *signature != "" {
		if _, err := fmt.Sscanf(*signature, "%d/%d", &s.BeatsPerBar, &s.BeatUnit); err != nil || s.BeatsPerBar <= 0 || s.BeatUnit <= 0 {
			return o, fmt.Errorf("time signature %q is not beats/unit", *signature)
		}
	}
	return o, s.check()
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

// app is the running application, owned by the main thread; the audio
// device renders in C (audio.h).
type app struct {
	name     string
	dir      string
	log      io.Writer
	settings Settings
	audio    *audio
	midi     *midiPorts
	gui      bool
	quit     atomic.Bool
	failed   error

	playing        bool   // transport
	shownPosition  string // position the toolbar shows
	toolbar        *plugin.Panel
	settingsWindow *plugin.Panel
	toolbarPost    poster
	settingsPost   poster
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
	a := &app{name: C.GoString(desc.name), log: log}
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
	a.saveSettings()
	a.playing = o.play
	a.applyTransport()

	in, out := mainChannels()
	if a.audio, err = openAudio(a.settings, in, out); err != nil {
		return err
	}
	// The settings window may replace the device and the MIDI ports.
	defer func() { a.audio.close() }()
	if err := a.startAudio(); err != nil {
		return err
	}
	defer a.stopAudio()

	if a.midi, err = openMIDI(a.settings, a.name, bool(C.vst3go_sa_note_input())); err != nil {
		fmt.Fprintf(log, "%s: MIDI: %v\n", a.name, err)
	}
	defer func() { a.midi.close() }()

	editor := "no editor"
	if !o.headless {
		if w, h, ok := a.openEditor(); ok {
			editor = fmt.Sprintf("editor %dx%d", w, h)
			defer a.closeEditor()
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

	a.loop()

	// Stop the audio before the editor goes, then keep the state.
	a.stopAudio()
	if err := a.saveState(); err != nil {
		fmt.Fprintf(log, "%s: state not saved: %v\n", a.name, err)
	}
	return a.failed
}

// loop runs the main thread until the application quits.
func (a *app) loop() {
	current = a
	defer func() { current = nil }()
	if a.gui {
		C.vst3go_sa_run(C.uint32_t(tickInterval / time.Millisecond))
		return
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		if a.tick() {
			return
		}
	}
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
	// The toolbar has the editor's pixels: physical ones on Windows.
	bar := toolbarHeight
	if scale := float64(C.vst3go_sa_window_scale()); scale > 0 {
		bar = int(math.Round(toolbarHeight * scale))
	}
	var window C.clap_window_t
	var toolbar unsafe.Pointer
	if !C.vst3go_sa_window_open(title, w, h, resizable, C.uint32_t(bar), &window, &toolbar) {
		C.vst3go_sa_gui_destroy()
		return 0, 0, false
	}
	if !C.vst3go_sa_gui_attach(&window) {
		C.vst3go_sa_gui_destroy()
		C.vst3go_sa_window_close()
		return 0, 0, false
	}
	a.openToolbar(toolbar, int(w), bar)
	a.gui = true
	return uint32(w), uint32(h), true
}

// closeEditor closes the toolbar and settings panels, the editor and the
// window.
func (a *app) closeEditor() {
	a.closePanels()
	C.vst3go_sa_gui_destroy()
	C.vst3go_sa_window_close()
	a.gui = false
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
	a.updatePosition()
	a.idlePanels()
	return a.quit.Load()
}

func (a *app) startAudio() error {
	if a.audio == nil {
		return errors.New("no audio device")
	}
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
