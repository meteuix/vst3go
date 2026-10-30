package plugin

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/th-release/vst3go/pkg/framework/bus"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
)

// Format-independent parts of the Audio Unit (AUv2) front end
// (au_darwin.c, wrapper_au_darwin.go).

// AudioUnitPlugin sets the Audio Unit component codes of a plugin. Hosts
// identify an Audio Unit by its type, subtype and manufacturer codes, four
// ASCII characters each; the manufacturer code needs at least one
// non-lowercase character (all-lowercase codes are reserved by Apple).
// Without it the manufacturer code comes from the vendor name and the
// subtype from a hash of Info.ID.
type AudioUnitPlugin interface {
	Plugin
	AudioUnitCodes() (manufacturer, subtype string)
}

// AudioUnitFactoryFunction is the factory function every component of a
// bundle names in its Info.plist.
const AudioUnitFactoryFunction = "VST3GoAUFactory"

// Audio Unit component types.
const (
	auTypeEffect        = "aufx"
	auTypeMusicEffect   = "aumf"
	auTypeMusicDevice   = "aumu"
	auTypeGenerator     = "augn"
	auTypeMIDIProcessor = "aumi"
)

// auComponent is the Audio Unit identity of one registered plugin.
type auComponent struct {
	Type, Subtype, Manufacturer string
	Name                        string // "Vendor: Name", as hosts list it
	Version                     uint32 // 0xMMMMmmbb
	Tags                        []string
}

func fourCC(code string) uint32 {
	return uint32(code[0])<<24 | uint32(code[1])<<16 | uint32(code[2])<<8 | uint32(code[3])
}

func validFourCC(code string) bool {
	if len(code) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if code[i] < 0x20 || code[i] > 0x7E {
			return false
		}
	}
	return true
}

// auComponents derives the component of every registered plugin.
func auComponents(plugins []Plugin) ([]auComponent, error) {
	list := make([]auComponent, 0, len(plugins))
	seen := map[string]string{}
	for _, p := range plugins {
		c, err := newAUComponent(p)
		if err != nil {
			return nil, err
		}
		key := c.Type + c.Subtype + c.Manufacturer
		if other, ok := seen[key]; ok {
			return nil, fmt.Errorf("audio unit %s and %s have the same codes %s %s %s", other, p.GetInfo().ID, c.Type, c.Subtype, c.Manufacturer)
		}
		seen[key] = p.GetInfo().ID
		list = append(list, c)
	}
	return list, nil
}

func newAUComponent(p Plugin) (auComponent, error) {
	info := p.GetInfo()
	vendor := info.Vendor
	if vendor == "" {
		vendor = globalFactoryInfo.Vendor
	}
	manufacturer, subtype := auManufacturer(vendor), auSubtype(info.ID)
	if custom, ok := p.(AudioUnitPlugin); ok {
		manufacturer, subtype = custom.AudioUnitCodes()
	}
	if !validFourCC(manufacturer) || !validFourCC(subtype) {
		return auComponent{}, fmt.Errorf("audio unit codes of %s must be four printable ASCII characters: %q %q", info.ID, manufacturer, subtype)
	}
	if strings.ToLower(manufacturer) == manufacturer {
		return auComponent{}, fmt.Errorf("audio unit manufacturer code of %s needs a non-lowercase character: %q", info.ID, manufacturer)
	}
	c := auComponent{
		Type:         auType(p, info),
		Subtype:      subtype,
		Manufacturer: manufacturer,
		Name:         vendor + ": " + info.Name,
		Version:      auVersion(info.Version),
	}
	switch c.Type {
	case auTypeMusicDevice:
		c.Tags = []string{"Synthesizer"}
	case auTypeMIDIProcessor:
		c.Tags = []string{"MIDI"}
	default:
		c.Tags = []string{"Effects"}
	}
	if _, ok := p.(ARAPlugin); ok {
		c.Tags = append(c.Tags, "ARA") // kARAAudioComponentTag
	}
	return c, nil
}

// auType picks the component type from the category and the buses:
// instruments are music devices, effects with note input music effects,
// plugins with notes and no audio MIDI processors.
func auType(p Plugin, info frameworkplugin.Info) string {
	buses := p.CreateProcessor().GetBuses()
	count := func(media bus.MediaType, dir bus.Direction) int32 {
		if buses == nil {
			return 0
		}
		return buses.GetBusCount(media, dir)
	}
	audioIn := count(bus.MediaTypeAudio, bus.DirectionInput) > 0
	audioOut := count(bus.MediaTypeAudio, bus.DirectionOutput) > 0
	noteIn := count(bus.MediaTypeEvent, bus.DirectionInput) > 0
	instrument := strings.HasPrefix(info.SubCategories(), "Instrument")
	switch {
	case !audioIn && !audioOut:
		return auTypeMIDIProcessor
	case instrument || (!audioIn && noteIn):
		return auTypeMusicDevice
	case !audioIn:
		return auTypeGenerator
	case noteIn:
		return auTypeMusicEffect
	default:
		return auTypeEffect
	}
}

// auManufacturer takes the first four letters and digits of the vendor,
// the first one upper case, padded with 'x'.
func auManufacturer(vendor string) string {
	var code []byte
	for _, r := range vendor {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			code = append(code, byte(r))
			if len(code) == 4 {
				break
			}
		}
	}
	for len(code) < 4 {
		code = append(code, 'x')
	}
	code[0] = byte(unicode.ToUpper(rune(code[0])))
	return string(code)
}

// auSubtype hashes the plugin ID into four lower case letters and digits.
func auSubtype(id string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	sum := sha256.Sum256([]byte(id))
	code := make([]byte, 4)
	for i := range code {
		code[i] = alphabet[int(sum[i])%len(alphabet)]
	}
	return string(code)
}

// auVersion packs "major.minor.patch" as 0xMMMMmmbb.
func auVersion(version string) uint32 {
	parts := strings.SplitN(version, ".", 3)
	var v [3]uint64
	for i, part := range parts {
		end := 0
		for end < len(part) && part[end] >= '0' && part[end] <= '9' {
			end++
		}
		v[i], _ = strconv.ParseUint(part[:end], 10, 16)
	}
	return uint32(min(v[0], 0xFFFF)<<16 | min(v[1], 0xFF)<<8 | min(v[2], 0xFF))
}

// auComponentsPlist is the AudioComponents entry of the bundle's
// Info.plist.
func auComponentsPlist(list []auComponent) string {
	var b strings.Builder
	b.WriteString("<key>AudioComponents</key>\n<array>\n")
	for _, c := range list {
		b.WriteString("  <dict>\n")
		fmt.Fprintf(&b, "    <key>type</key><string>%s</string>\n", xmlEscape(c.Type))
		fmt.Fprintf(&b, "    <key>subtype</key><string>%s</string>\n", xmlEscape(c.Subtype))
		fmt.Fprintf(&b, "    <key>manufacturer</key><string>%s</string>\n", xmlEscape(c.Manufacturer))
		fmt.Fprintf(&b, "    <key>name</key><string>%s</string>\n", xmlEscape(c.Name))
		fmt.Fprintf(&b, "    <key>description</key><string>%s</string>\n", xmlEscape(c.Name))
		fmt.Fprintf(&b, "    <key>version</key><integer>%d</integer>\n", c.Version)
		fmt.Fprintf(&b, "    <key>factoryFunction</key><string>%s</string>\n", AudioUnitFactoryFunction)
		b.WriteString("    <key>sandboxSafe</key><true/>\n")
		b.WriteString("    <key>tags</key><array>")
		for _, tag := range c.Tags {
			fmt.Fprintf(&b, "<string>%s</string>", xmlEscape(tag))
		}
		b.WriteString("</array>\n")
		b.WriteString("  </dict>\n")
	}
	b.WriteString("</array>\n")
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
