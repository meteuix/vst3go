//go:build linux && webkitgtk

package plugin

// The Linux editor embeds a WebKitGTK view into the host's X11 window. It is
// opt-in (build tag webkitgtk) because it links GTK 3 and WebKitGTK 4.1:
//
//	GOFLAGS=-tags=webkitgtk scripts/build_vst3.sh ./myplugin MyPlugin

/*
#cgo CFLAGS: -I../../include
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1 x11
*/
import "C"
