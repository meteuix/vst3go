//go:build windows

package vst3

// Result codes (Steinberg::tresult) of COM-compatible platforms: HRESULT
// values as signed 32-bit integers.
const (
	ResultOK              = 0
	ResultFalse           = 1
	ResultInvalidArgument = -2147024809 // 0x80070057
	ResultNotImplemented  = -2147467263 // 0x80004001
	ResultInternalError   = -2147467259 // 0x80004005
	ResultNoInterface     = -2147467262 // 0x80004002
)
