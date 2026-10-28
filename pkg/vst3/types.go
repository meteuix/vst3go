// Package vst3 provides Go helpers for the VST3 C API used by the runtime.
package vst3

// Result codes (Steinberg::tresult) are defined per platform in
// results_com.go (Windows, COM HRESULT values) and results_posix.go. The C
// header declares them as static const variables, which cgo cannot use as
// Go constants on every compiler.

// Error codes
type Error int

const (
	ErrNotImplemented  Error = -1
	ErrInvalidArgument Error = -2
	ErrShortTransfer   Error = -3
)

func (e Error) Error() string {
	switch e {
	case ErrNotImplemented:
		return "not implemented"
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrShortTransfer:
		return "stream transferred fewer bytes than requested"
	default:
		return "unknown error"
	}
}
