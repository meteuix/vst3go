//go:build !windows

package vst3

// Result codes (Steinberg::tresult) of non-COM platforms.
const (
	ResultOK              = 0
	ResultFalse           = 1
	ResultInvalidArgument = 2
	ResultNotImplemented  = 3
	ResultInternalError   = 4
	ResultNoInterface     = -1
)
