// Package vst3 provides Go helpers for the VST3 C API used by the runtime.
package vst3

// #include "../../include/vst3/vst3_c_api.h"
import "C"

// Result codes (Steinberg::tresult) as plain Go values.
const (
	ResultOK              = C.Steinberg_kResultOk
	ResultFalse           = C.Steinberg_kResultFalse
	ResultInvalidArgument = C.Steinberg_kInvalidArgument
	ResultNotImplemented  = C.Steinberg_kNotImplemented
	ResultNoInterface     = C.Steinberg_kNoInterface
)

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
