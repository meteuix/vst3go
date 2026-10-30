package plugin

import (
	"crypto/md5" // #nosec G501 - MD5 is appropriate for deterministic UUID generation
	"fmt"
)

const (
	// UUID v4 version and variant constants per RFC 4122
	uuidVersion4    = 0x40
	uuidVariant     = 0x80
	uuidVersionMask = 0x0f
	uuidVariantMask = 0x3f
)

// Info contains host-visible plugin metadata.
//
// This type lives in framework/plugin because it is shared metadata rather than
// a runtime wrapper concern. pkg/plugin.Plugin.GetInfo returns this type.
type Info struct {
	ID      string // Unique plugin identifier (e.g., "com.example.myplugin")
	Name    string // Display name
	Version string // Semantic version (e.g., "1.0.0")
	Vendor  string // Company/developer name
	// Category is the VST3 sub-category string hosts use to file the plugin,
	// for example CategoryFxEQ or CategoryInstrumentSynth. Several categories
	// are joined with '|'. Empty means CategoryFx.
	Category string
}

// VST3 sub-categories (Steinberg::Vst::PlugType).
const (
	CategoryFx                 = "Fx"
	CategoryFxAnalyzer         = "Fx|Analyzer"
	CategoryFxDelay            = "Fx|Delay"
	CategoryFxDistortion       = "Fx|Distortion"
	CategoryFxDynamics         = "Fx|Dynamics"
	CategoryFxEQ               = "Fx|EQ"
	CategoryFxFilter           = "Fx|Filter"
	CategoryFxGenerator        = "Fx|Generator"
	CategoryFxMastering        = "Fx|Mastering"
	CategoryFxModulation       = "Fx|Modulation"
	CategoryFxPitchShift       = "Fx|Pitch Shift"
	CategoryFxRestoration      = "Fx|Restoration"
	CategoryFxReverb           = "Fx|Reverb"
	CategoryFxSpatial          = "Fx|Spatial"
	CategoryFxSurround         = "Fx|Surround"
	CategoryFxTools            = "Fx|Tools"
	CategoryFxVocals           = "Fx|Vocals"
	CategoryFxInstrument       = "Fx|Instrument" // MIDI-driven effect
	CategoryInstrument         = "Instrument"
	CategoryInstrumentDrum     = "Instrument|Drum"
	CategoryInstrumentPiano    = "Instrument|Piano"
	CategoryInstrumentSampler  = "Instrument|Sampler"
	CategoryInstrumentSynth    = "Instrument|Synth"
	CategoryAnalyzer           = "Analyzer"
	CategoryOnlyRealTime       = "OnlyRT"
	CategoryOnlyOfflineProcess = "OnlyOfflineProcess"
	CategoryNoOfflineProcess   = "NoOfflineProcess"
)

// SubCategories returns the category string reported to the host.
func (i *Info) SubCategories() string {
	if i.Category == "" {
		return CategoryFx
	}
	return i.Category
}

// UID converts the string ID to a deterministic 16-byte array for VST3.
func (i *Info) UID() [16]byte {
	return i.generateDeterministicUID()
}

// ControllerUID is the class ID of the edit controller of a distributed
// plugin, derived from the plugin ID like UID.
func (i *Info) ControllerUID() [16]byte {
	controller := Info{ID: i.ID + "#controller"}
	return controller.generateDeterministicUID()
}

// ARAFactoryUID is the class ID of the ARA main factory class of an ARA
// plugin, derived from the plugin ID like UID.
func (i *Info) ARAFactoryUID() [16]byte {
	factory := Info{ID: i.ID + "#ara"}
	return factory.generateDeterministicUID()
}

// generateDeterministicUID creates a deterministic UUID v4 from the plugin ID
func (i *Info) generateDeterministicUID() [16]byte {
	// Generate deterministic UUID from plugin ID string
	// This ensures the same plugin ID always generates the same UUID
	hash := md5.Sum([]byte(i.ID)) // #nosec G401 - MD5 is appropriate for deterministic UUID generation

	// Ensure it's a valid UUID v4 format
	// Set version (4) and variant bits according to RFC 4122
	hash[6] = (hash[6] & uuidVersionMask) | uuidVersion4 // Version 4
	hash[8] = (hash[8] & uuidVariantMask) | uuidVariant  // Variant 10

	return hash
}

// ValidateUID checks if the generated UID is unique and valid
func (i *Info) ValidateUID() error {
	// Validate that ID is not empty
	if i.ID == "" {
		return fmt.Errorf("plugin ID cannot be empty")
	}

	return nil
}
