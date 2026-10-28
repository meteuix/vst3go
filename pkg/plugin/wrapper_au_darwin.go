//go:build darwin

package plugin

/*
#cgo darwin LDFLAGS: -framework AudioToolbox -framework CoreAudio -framework CoreMIDI -framework CoreFoundation
#include <stdint.h>
#include <stdbool.h>
*/
import "C"

import "sync"

// Audio Unit front end: au_darwin.c hosts the plugin's CLAP instance as an
// AUv2 component; these exports give it the component codes.

var (
	auOnce sync.Once
	auList []auComponent
	auErr  error
)

func auComponentList() ([]auComponent, error) {
	auOnce.Do(func() { auList, auErr = auComponents(registeredPlugins) })
	return auList, auErr
}

// GoAUComponentCodes reports the codes of registered plugin index; false
// past the end or when the codes are invalid.
//
//export GoAUComponentCodes
func GoAUComponentCodes(index C.uint32_t, kind, subtype, manufacturer *C.uint32_t) C.bool {
	list, err := auComponentList()
	if err != nil || int(index) >= len(list) {
		return false
	}
	c := list[index]
	*kind, *subtype, *manufacturer = C.uint32_t(fourCC(c.Type)), C.uint32_t(fourCC(c.Subtype)), C.uint32_t(fourCC(c.Manufacturer))
	return true
}

// GoAUComponentsPlist returns the AudioComponents Info.plist entry (malloc'd)
// for the bundle build, or the error text with ok false.
//
//export GoAUComponentsPlist
func GoAUComponentsPlist(ok *C.bool) *C.char {
	list, err := auComponentList()
	if err != nil {
		*ok = false
		return C.CString(err.Error())
	}
	*ok = true
	return C.CString(auComponentsPlist(list))
}
