package standalone

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Vendor", "Plugin")
	s, err := LoadSettings(dir)
	if err != nil || s != (Settings{}) {
		t.Fatalf("missing settings = %+v, %v", s, err)
	}
	want := Settings{Backend: "null", Output: "Speakers", Input: "none", SampleRate: 96000, BufferSize: 64, MIDIInput: "Keys, Pads", MIDIOutput: "Synth", NoVirtualMIDI: true}
	if err := SaveSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSettings(dir); err != nil || got != want {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(dir); err == nil {
		t.Fatal("corrupt settings loaded")
	}
}

func TestSettingsDefaults(t *testing.T) {
	if n := (Settings{}).bufferSize(); n != defaultBufferSize {
		t.Fatalf("default buffer = %d", n)
	}
	for in, want := range map[string][]string{
		"":              {"*"},
		"all":           {"*"},
		"none":          nil,
		" Keys , ,Pads": {"Keys", "Pads"},
	} {
		if got := (Settings{MIDIInput: in}).midiInputs(); !reflect.DeepEqual(got, want) {
			t.Errorf("midiInputs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigDir(t *testing.T) {
	dir, err := ConfigDir("Acme/Audio", "..")
	if err != nil {
		t.Skip(err)
	}
	if !strings.HasSuffix(dir, filepath.Join("Acme_Audio", "plugin")) {
		t.Fatalf("ConfigDir = %q", dir)
	}
}
