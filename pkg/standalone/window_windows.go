//go:build standalone && windows

package standalone

/*
#cgo LDFLAGS: -luser32 -lgdi32
*/
import "C"
