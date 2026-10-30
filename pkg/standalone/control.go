//go:build standalone

package standalone

/*
#include "host.h"
#include "audio.h"
*/
import "C"

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

// The application window's toolbar (transport, status, settings button)
// and the audio and MIDI settings window are web panels (plugin.Panel).

var (
	//go:embed ui/toolbar.html
	toolbarHTML string
	//go:embed ui/settings.html
	settingsHTML string
)

const (
	// toolbarHeight is the toolbar strip's height in logical pixels.
	toolbarHeight  = 36
	settingsWidth  = 460
	settingsHeight = 470
)

var (
	sampleRates = []int{44100, 48000, 88200, 96000, 176400, 192000}
	bufferSizes = []int{16, 32, 64, 128, 256, 512, 1024, 2048, 4096}
)

// poster sends a message to a panel page; a *plugin.Panel, or a recorder
// in tests.
type poster interface{ Post(message any) error }

// controlMessage is a message the toolbar or settings page posts.
type controlMessage struct {
	Type        string    `json:"type"`
	Tempo       float64   `json:"tempo"`
	BeatsPerBar int       `json:"beatsPerBar"`
	BeatUnit    int       `json:"beatUnit"`
	Backend     string    `json:"backend"`
	Settings    *Settings `json:"settings"`
}

type transportMessage struct {
	Type        string  `json:"type"` // "transport"
	Playing     bool    `json:"playing"`
	Tempo       float64 `json:"tempo"`
	BeatsPerBar int     `json:"beatsPerBar"`
	BeatUnit    int     `json:"beatUnit"`
	Position    string  `json:"position"`
}

type positionMessage struct {
	Type     string `json:"type"` // "position"
	Position string `json:"position"`
}

type statusMessage struct {
	Type string `json:"type"` // "status"
	Text string `json:"text"`
}

// devicesMessage fills the settings page for a backend.
type devicesMessage struct {
	Type              string   `json:"type"` // "devices"
	Backend           string   `json:"backend"`
	Backends          []string `json:"backends"`
	Outputs           []string `json:"outputs"`
	Inputs            []string `json:"inputs"`
	AudioInput        bool     `json:"audioInput"` // the plugin takes audio input
	MIDIInputs        []string `json:"midiInputs"`
	MIDIOutputs       []string `json:"midiOutputs"`
	SampleRates       []int    `json:"sampleRates"`
	BufferSizes       []int    `json:"bufferSizes"`
	DefaultBufferSize int      `json:"defaultBufferSize"`
	Settings          Settings `json:"settings"`
	Error             string   `json:"error,omitempty"`
}

type appliedMessage struct {
	Type   string `json:"type"` // "applied"
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// applyTransport hands the transport state to the audio thread.
func (a *app) applyTransport() {
	beats, unit := a.settings.timeSignature()
	C.vst3go_sa_transport_set(C.bool(a.playing), C.double(a.settings.tempo()), C.uint32_t(beats), C.uint32_t(unit))
}

// position formats the transport position as bar.beat, both from 1.
func (a *app) position() string {
	beats, unit := a.settings.timeSignature()
	quarters := float64(C.vst3go_sa_transport_position())
	beat := 4 / float64(unit) // quarter notes per beat
	n := int64(math.Floor(quarters/beat + 1e-9))
	return fmt.Sprintf("%d.%d", n/int64(beats)+1, n%int64(beats)+1)
}

func (a *app) postTransport() {
	if a.toolbarPost == nil {
		return
	}
	beats, unit := a.settings.timeSignature()
	a.shownPosition = a.position()
	_ = a.toolbarPost.Post(transportMessage{"transport", a.playing, a.settings.tempo(), beats, unit, a.shownPosition})
}

func (a *app) postStatus() {
	if a.toolbarPost == nil {
		return
	}
	text := "no audio device"
	if a.audio != nil {
		text = a.audio.describe() + ", " + a.midi.describe()
	}
	_ = a.toolbarPost.Post(statusMessage{"status", text})
}

// updatePosition posts the transport position when it moved to another
// beat; it runs every tick.
func (a *app) updatePosition() {
	if a.toolbarPost == nil {
		return
	}
	if position := a.position(); position != a.shownPosition {
		a.shownPosition = position
		_ = a.toolbarPost.Post(positionMessage{"position", position})
	}
}

// toolbarMessage serves a message of the toolbar page.
func (a *app) toolbarMessage(data []byte) {
	var msg controlMessage
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "ready":
		a.postStatus()
	case "play", "stop":
		a.playing = msg.Type == "play"
		a.applyTransport()
	case "rewind":
		C.vst3go_sa_transport_rewind()
	case "tempo":
		next := a.settings
		next.Tempo = msg.Tempo
		a.changeTransport(next)
	case "time-signature":
		next := a.settings
		next.BeatsPerBar, next.BeatUnit = msg.BeatsPerBar, msg.BeatUnit
		a.changeTransport(next)
	case "settings":
		a.openSettings()
		return
	default:
		return
	}
	// The page shows what applied, so rejected values revert; "ready"
	// gets the first state.
	a.postTransport()
}

// changeTransport applies and saves a tempo or time signature change.
func (a *app) changeTransport(next Settings) {
	if next.check() != nil {
		return
	}
	a.settings = next
	a.applyTransport()
	a.saveSettings()
}

func (a *app) saveSettings() {
	if err := SaveSettings(a.dir, a.settings); err != nil && a.log != nil {
		fmt.Fprintf(a.log, "%s: settings not saved: %v\n", a.name, err)
	}
}

// settingsMessage serves a message of the settings page.
func (a *app) settingsMessage(data []byte) {
	var msg controlMessage
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "ready":
		a.postDevices(a.settings.Backend, a.settings)
	case "backend":
		// Another API lists other devices; the rest of the form stays.
		s := a.settings
		s.Backend, s.Output, s.Input = msg.Backend, "", ""
		a.postDevices(msg.Backend, s)
	case "apply":
		if msg.Settings == nil {
			return
		}
		reply := appliedMessage{Type: "applied"}
		if err := a.applySettings(*msg.Settings); err != nil {
			reply.Error = err.Error()
		} else {
			reply.Status = "Applied: " + a.audio.describe()
		}
		if a.settingsPost != nil {
			_ = a.settingsPost.Post(reply)
		}
		a.postStatus()
	case "close":
		if a.settingsWindow != nil {
			a.settingsWindow.ShowWindow(false)
		}
	}
}

// postDevices lists the devices of a backend and the MIDI ports to the
// settings page, with the settings s to show.
func (a *app) postDevices(backend string, s Settings) {
	if a.settingsPost == nil {
		return
	}
	in, _ := mainChannels()
	msg := devicesMessage{
		Type: "devices", Backend: backend, Backends: audioBackends(), AudioInput: in > 0,
		SampleRates: sampleRates, BufferSizes: bufferSizes, DefaultBufferSize: defaultBufferSize, Settings: s,
		Outputs: []string{}, Inputs: []string{}, MIDIInputs: []string{}, MIDIOutputs: []string{},
	}
	if s.BufferSize > 0 && !slices.Contains(bufferSizes, s.BufferSize) {
		msg.BufferSizes = append(slices.Clone(bufferSizes), s.BufferSize)
		slices.Sort(msg.BufferSizes)
	}
	if s.SampleRate > 0 && !slices.Contains(sampleRates, s.SampleRate) {
		msg.SampleRates = append(slices.Clone(sampleRates), s.SampleRate)
		slices.Sort(msg.SampleRates)
	}
	if api, err := audioAPI(backend); err != nil {
		msg.Error = err.Error()
	} else {
		outputs, _ := deviceNames(api, false)
		inputs, _ := deviceNames(api, true)
		msg.Outputs = append(msg.Outputs, outputs...)
		msg.Inputs = append(msg.Inputs, inputs...)
		C.vst3go_sa_audio_free(api)
	}
	if ins, outs, err := midiPortNames(); err == nil {
		msg.MIDIInputs = append(msg.MIDIInputs, ins...)
		msg.MIDIOutputs = append(msg.MIDIOutputs, outs...)
	}
	_ = a.settingsPost.Post(msg)
}

// audioBackends names the audio APIs built for this platform.
func audioBackends() []string {
	var names []string
	for i := C.uint32_t(0); ; i++ {
		name := C.vst3go_sa_audio_backend_name(i)
		if name == nil {
			return names
		}
		names = append(names, C.GoString(name))
	}
}

// applySettings switches to the devices of next, keeping the transport
// settings, and saves them. When the new devices fail, the previous ones
// come back.
func (a *app) applySettings(next Settings) error {
	next.Tempo, next.BeatsPerBar, next.BeatUnit = a.settings.Tempo, a.settings.BeatsPerBar, a.settings.BeatUnit
	if err := next.check(); err != nil {
		return err
	}
	if err := a.reopenAudio(next); err != nil {
		if restore := a.reopenAudio(a.settings); restore != nil {
			a.failed = fmt.Errorf("%s: audio: %w", a.name, restore)
			a.quit.Store(true)
		}
		return err
	}
	a.settings = next
	a.saveSettings()
	a.midi.close()
	var err error
	if a.midi, err = openMIDI(next, a.name, bool(C.vst3go_sa_note_input())); err != nil {
		return fmt.Errorf("MIDI: %w", err)
	}
	return nil
}

// reopenAudio closes the audio device and opens the devices of s. The old
// device closes first: exclusive devices open once.
func (a *app) reopenAudio(s Settings) error {
	a.stopAudio()
	a.audio.close()
	a.audio = nil
	in, out := mainChannels()
	opened, err := openAudio(s, in, out)
	if err != nil {
		return err
	}
	a.audio = opened
	return a.startAudio()
}

// openToolbar puts the toolbar panel into the window's toolbar strip of
// width x height editor pixels.
func (a *app) openToolbar(parent unsafe.Pointer, width, height int) {
	p, err := plugin.NewPanel(toolbarHTML, width, height, a.toolbarMessage, nil)
	if err != nil {
		return
	}
	if err := p.Attach(parent); err != nil {
		p.Close()
		return
	}
	p.SetSize(width, height)
	a.toolbar = p
	a.toolbarPost = p
}

// openSettings shows the settings window, creating it on first use.
func (a *app) openSettings() {
	if a.settingsWindow != nil {
		a.settingsWindow.ShowWindow(true)
		a.postDevices(a.settings.Backend, a.settings)
		return
	}
	p, err := plugin.NewPanel(settingsHTML, settingsWidth, settingsHeight, a.settingsMessage, nil)
	if err != nil {
		return
	}
	a.settingsWindow, a.settingsPost = p, p
	if err := p.OpenWindow(a.name+" Settings", false); err != nil {
		p.Close()
		a.settingsWindow, a.settingsPost = nil, nil
	}
}

// closePanels destroys the toolbar and the settings window.
func (a *app) closePanels() {
	for _, p := range []*plugin.Panel{a.toolbar, a.settingsWindow} {
		if p != nil {
			p.Close()
		}
	}
	a.toolbar, a.settingsWindow = nil, nil
	a.toolbarPost, a.settingsPost = nil, nil
}

// idlePanels runs the panels' periodic work every tick.
func (a *app) idlePanels() {
	for _, p := range []*plugin.Panel{a.toolbar, a.settingsWindow} {
		if p != nil {
			p.Idle()
		}
	}
}

//export goStandaloneToolbarSize
func goStandaloneToolbarSize(width, height C.uint32_t) {
	if current != nil && current.toolbar != nil {
		current.toolbar.SetSize(int(width), int(height))
	}
}

//export goStandaloneOpenSettings
func goStandaloneOpenSettings() {
	if current != nil {
		current.openSettings()
	}
}
