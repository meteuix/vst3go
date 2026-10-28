//go:build standalone && linux

package midiport

/*
#cgo pkg-config: alsa
#cgo LDFLAGS: -lpthread
*/
import "C"
