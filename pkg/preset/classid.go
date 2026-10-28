package preset

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ClassIDString formats a factory class ID the way the VST3 SDK does
// (FUID::toString) on the current platform: 32 upper-case hex digits of the
// TUID bytes, or, on Windows where TUIDs are COM GUIDs, of the GUID fields.
// Hosts compare this string with the plugin's class.
func ClassIDString(id [16]byte) string {
	b := guidOrder(id)
	return strings.ToUpper(hex.EncodeToString(b[:]))
}

// ParseClassID is the inverse of ClassIDString.
func ParseClassID(s string) ([16]byte, error) {
	var id [16]byte
	if len(s) != 32 {
		return id, fmt.Errorf("class ID %q: want 32 hex digits", s)
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, fmt.Errorf("class ID %q: %w", s, err)
	}
	return guidOrder(id), nil
}
