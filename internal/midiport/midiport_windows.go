//go:build standalone && windows

package midiport

/*
#cgo LDFLAGS: -lwinmm
*/
import "C"
