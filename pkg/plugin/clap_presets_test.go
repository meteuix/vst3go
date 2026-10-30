package plugin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/preset"
)

func TestCLAPPresetMetadata(t *testing.T) {
	info := frameworkplugin.Info{ID: "com.example.synth", Name: "Synth", Vendor: "Example"}
	other := frameworkplugin.Info{ID: "com.example.other", Name: "Other", Vendor: "Example"}
	dir := t.TempDir()
	meta := preset.MetaInfo{
		preset.AttrPlugInName:     "Synth",
		preset.AttrInstrument:     "Piano|A. Piano",
		preset.AttrStyle:          "Jazz",
		preset.AttrMediaType:      "VstPreset",
		preset.AttrPlugInCategory: "Instrument|Synth",
	}
	path, err := preset.Save(dir, "Keys/Warm", &preset.File{ClassID: info.UID(), Component: []byte{1}, MetaInfo: meta.Encode()})
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := clapPresetMetadata(info, path)
	if err != nil || !ok {
		t.Fatalf("own preset: ok=%v err=%v", ok, err)
	}
	if got.Name != "Warm" || got.Modified <= 0 {
		t.Errorf("name %q modified %d", got.Name, got.Modified)
	}
	if !slices.Equal(got.Features, []string{"Piano", "A. Piano", "Jazz"}) {
		t.Errorf("features %v", got.Features)
	}
	if len(got.Extra) != 2 || got.Extra[preset.AttrPlugInName] != "Synth" || got.Extra[preset.AttrPlugInCategory] != "Instrument|Synth" {
		t.Errorf("extra %v", got.Extra)
	}
	if want := uuidString(info.UID()); got.VST3ID != want || len(want) != 36 || want[8] != '-' || want[23] != '-' {
		t.Errorf("vst3 id %q, want %q", got.VST3ID, want)
	}

	if _, ok, err := clapPresetMetadata(other, path); ok || err != nil {
		t.Errorf("foreign preset: ok=%v err=%v", ok, err)
	}
	broken := filepath.Join(dir, "Broken.vstpreset")
	if err := os.WriteFile(broken, []byte("not a preset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := clapPresetMetadata(info, broken); !errors.Is(err, preset.ErrFormat) {
		t.Errorf("broken preset: %v", err)
	}
	if _, _, err := clapPresetMetadata(info, filepath.Join(dir, "Missing.vstpreset")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing preset: %v", err)
	}
}

func TestUUIDString(t *testing.T) {
	var id [16]byte
	for i := range id {
		id[i] = byte(i * 17)
	}
	got := uuidString(id)
	parsed, err := preset.ParseClassID(got[:8] + got[9:13] + got[14:18] + got[19:23] + got[24:])
	if err != nil || parsed != id {
		t.Errorf("uuid %q does not round-trip: %v", got, err)
	}
}
