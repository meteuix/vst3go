package preset

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"slices"
)

// Meta info attribute keys (Steinberg::Vst::PresetAttributes) that host
// preset browsers read.
const (
	AttrPlugInName     = "PlugInName"        // plugin name
	AttrPlugInCategory = "PlugInCategory"    // "Fx|Dynamics", "Instrument|Synth"
	AttrInstrument     = "MusicalInstrument" // "Piano|A. Piano"
	AttrStyle          = "MusicalStyle"      // "Pop", "Jazz"
	AttrCharacter      = "MusicalCharacter"  // "Soft|Dry"
	AttrMediaType      = "MediaType"         // always "VstPreset"
)

const (
	mediaTypeVSTPreset = "VstPreset"
	attrFlagsReadOnly  = "writeProtected"
	attrTypeString     = "string"
	xmlHeader          = `<?xml version="1.0" encoding="utf-8"?>` + "\n"
	maxMetaInfoSize    = 1 << 20
)

// MetaInfo is the XML meta info of a preset: attribute IDs to values.
type MetaInfo map[string]string

type metaXML struct {
	XMLName    xml.Name  `xml:"MetaInfo"`
	Attributes []attrXML `xml:"Attribute"`
}

type attrXML struct {
	ID    string `xml:"id,attr"`
	Value string `xml:"value,attr"`
	Type  string `xml:"type,attr"`
	Flags string `xml:"flags,attr,omitempty"`
}

// Encode returns the meta info chunk in the format of Steinberg hosts.
// The known attributes come first, then the others sorted by key, so files
// are reproducible.
func (m MetaInfo) Encode() []byte {
	doc := metaXML{Attributes: []attrXML{{ID: AttrMediaType, Value: mediaTypeVSTPreset, Type: attrTypeString, Flags: attrFlagsReadOnly}}}
	for _, key := range sortedKeys(m) {
		if key == AttrMediaType {
			continue
		}
		a := attrXML{ID: key, Value: m[key], Type: attrTypeString}
		if key == AttrPlugInName || key == AttrPlugInCategory {
			a.Flags = attrFlagsReadOnly
		}
		doc.Attributes = append(doc.Attributes, a)
	}
	out, err := xml.MarshalIndent(doc, "", "\t")
	if err != nil {
		panic(err) // only strings: cannot fail
	}
	return append([]byte(xmlHeader), out...)
}

// ParseMetaInfo parses a meta info chunk.
func ParseMetaInfo(data []byte) (MetaInfo, error) {
	if len(data) > maxMetaInfoSize {
		return nil, fmt.Errorf("preset meta info: %d bytes is too large", len(data))
	}
	var doc metaXML
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("preset meta info: %w", err)
	}
	m := MetaInfo{}
	for _, a := range doc.Attributes {
		m[a.ID] = a.Value
	}
	return m, nil
}

func sortedKeys(m MetaInfo) []string {
	known := []string{AttrPlugInName, AttrPlugInCategory, AttrInstrument, AttrStyle, AttrCharacter}
	keys := make([]string, 0, len(m))
	seen := map[string]bool{}
	for _, k := range known {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}
