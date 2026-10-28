package standalone

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Settings are the device choices of a standalone application. They are
// saved in its config directory (standalone.json) and restored at launch;
// command-line flags override and update them.
type Settings struct {
	// Backend is the audio API ("coreaudio", "wasapi", "alsa",
	// "pulseaudio", "jack", "null", ...); empty picks the platform's
	// default.
	Backend string `json:"backend,omitempty"`
	// Output and Input are audio device names (exact, or a unique part of
	// the name); empty uses the system default. Input "none" opens no
	// input device.
	Output string `json:"output,omitempty"`
	Input  string `json:"input,omitempty"`
	// SampleRate in Hz; 0 uses the device rate.
	SampleRate int `json:"sampleRate,omitempty"`
	// BufferSize is the device period in frames; 0 uses 256.
	BufferSize int `json:"bufferSize,omitempty"`
	// MIDIInput is "all" (or empty), "none", or a comma-separated list of
	// MIDI input port names.
	MIDIInput string `json:"midiInput,omitempty"`
	// NoVirtualMIDI turns off the virtual MIDI input named after the
	// plugin, which other applications can play (macOS and Linux).
	NoVirtualMIDI bool `json:"noVirtualMIDI,omitempty"`
	// MIDIOutput is the MIDI output port name for the plugin's MIDI output;
	// empty sends none.
	MIDIOutput string `json:"midiOutput,omitempty"`
}

const (
	defaultBufferSize = 256
	settingsFile      = "standalone.json"
	stateFile         = "standalone.state"
)

// bufferSize returns the period with the default applied.
func (s Settings) bufferSize() int {
	if s.BufferSize <= 0 {
		return defaultBufferSize
	}
	return s.BufferSize
}

// midiInputs returns the MIDI inputs to open: nil for none, ["*"] for all.
func (s Settings) midiInputs() []string {
	switch strings.TrimSpace(s.MIDIInput) {
	case "", "all":
		return []string{"*"}
	case "none":
		return nil
	}
	var names []string
	for _, name := range strings.Split(s.MIDIInput, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ConfigDir returns the directory a standalone application keeps its
// settings and plugin state in: <user config dir>/<vendor>/<name>.
func ConfigDir(vendor, name string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, pathName(vendor, "vst3go"), pathName(name, "plugin")), nil
}

// pathName makes a vendor or plugin name usable as one path element.
func pathName(name, fallback string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < ' ' {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return fallback
	}
	return name
}

// LoadSettings reads the settings of dir; a missing file gives zero
// Settings.
func LoadSettings(dir string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// SaveSettings writes the settings into dir.
func SaveSettings(dir string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, settingsFile), append(data, '\n'))
}

// writeFile replaces a file through a temporary file, so a crash leaves
// the old or the new content.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
