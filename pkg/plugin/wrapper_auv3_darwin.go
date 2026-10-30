//go:build darwin && auv3

package plugin

// The AUv3 extension front end (auv3_darwin.c) needs CoreAudioKit for
// AUViewController and AVFAudio for the bus formats.

/*
#cgo LDFLAGS: -framework CoreAudioKit -framework AVFAudio
*/
import "C"
