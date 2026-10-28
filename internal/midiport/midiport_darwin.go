//go:build standalone && darwin

package midiport

/*
#cgo LDFLAGS: -framework CoreMIDI -framework CoreFoundation
*/
import "C"
