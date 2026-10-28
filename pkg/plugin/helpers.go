package plugin

// #include "../../include/vst3/vst3_c_api.h"
import "C"

import (
	"unicode/utf16"
	"unsafe"
)

// copyStringToUTF16 writes src as NUL-terminated UTF-16 into dst, truncating
// on a code-point boundary so surrogate pairs are never split.
func copyStringToUTF16(src string, dst []uint16, maxLen int) {
	if maxLen > len(dst) {
		maxLen = len(dst)
	}
	if maxLen <= 0 {
		return
	}

	n := 0
	for _, r := range src {
		size := utf16.RuneLen(r)
		if size < 0 {
			r, size = '\uFFFD', 1
		}
		if n+size > maxLen-1 {
			break
		}
		if size == 2 {
			hi, lo := utf16.EncodeRune(r)
			dst[n], dst[n+1] = uint16(hi), uint16(lo)
		} else {
			dst[n] = uint16(r)
		}
		n += size
	}
	dst[n] = 0
}

func stringFromUTF16(src []uint16) string {
	length := 0
	for length < len(src) && src[length] != 0 {
		length++
	}
	return string(utf16.Decode(src[:length]))
}

// copyStringToTChar copies a Go string to a VST3 TChar (UTF-16) buffer.
func copyStringToTChar(src string, dst *C.Steinberg_Vst_TChar, maxLen int) {
	if dst == nil || maxLen <= 0 {
		return
	}
	copyStringToUTF16(src, unsafe.Slice((*uint16)(unsafe.Pointer(dst)), maxLen), maxLen)
}

// stringFromTChar converts a NUL-terminated VST3 TChar (UTF-16) string.
func stringFromTChar(src *C.Steinberg_Vst_TChar, maxLen int) string {
	if src == nil {
		return ""
	}
	length := 0
	for length < maxLen && *(*uint16)(unsafe.Add(unsafe.Pointer(src), length*2)) != 0 {
		length++
	}
	return stringFromUTF16(unsafe.Slice((*uint16)(unsafe.Pointer(src)), length))
}

// copyCString writes src as a NUL-terminated byte string into a fixed C array.
func copyCString(dst *C.char, size int, src string) {
	if dst == nil || size <= 0 {
		return
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(dst)), size)
	n := copy(buf[:size-1], src)
	// Do not cut a UTF-8 sequence in half.
	for n > 0 && n < len(src) && src[n]&0xC0 == 0x80 {
		n--
	}
	buf[n] = 0
}
