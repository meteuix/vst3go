//go:build darwin

package plugin

/*
#cgo darwin CFLAGS: -x objective-c
#cgo darwin,!ios LDFLAGS: -framework Cocoa -framework WebKit -framework UniformTypeIdentifiers
#cgo ios LDFLAGS: -framework UIKit -framework WebKit -framework UniformTypeIdentifiers
*/
import "C"
