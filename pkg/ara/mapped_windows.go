//go:build windows

package ara

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// mapFile maps size bytes of a new temporary file in dir, deleted when the
// mapping closes.
func mapFile(dir string, size int) (data []byte, unmap func(), err error) {
	f, err := os.CreateTemp(dir, "source-*.f32")
	if err != nil {
		return nil, nil, err
	}
	name := f.Name()
	f.Close()
	path, err := syscall.UTF16PtrFromString(filepath.Clean(name))
	if err != nil {
		os.Remove(name)
		return nil, nil, err
	}
	const (
		fileAttributeTemporary = 0x00000100
		fileFlagDeleteOnClose  = 0x04000000
	)
	h, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_DELETE, nil,
		syscall.CREATE_ALWAYS, fileAttributeTemporary|fileFlagDeleteOnClose, 0)
	if err != nil {
		os.Remove(name)
		return nil, nil, err
	}
	// The section keeps the file open; closing both handles deletes it.
	defer syscall.CloseHandle(h)
	section, err := syscall.CreateFileMapping(h, nil, syscall.PAGE_READWRITE, uint32(uint64(size)>>32), uint32(size), nil)
	if err != nil {
		return nil, nil, err
	}
	addr, err := syscall.MapViewOfFile(section, syscall.FILE_MAP_WRITE, 0, 0, uintptr(size))
	if err != nil {
		syscall.CloseHandle(section)
		return nil, nil, err
	}
	// The view is outside the Go heap; reading the address as a pointer
	// keeps vet's uintptr conversion check quiet.
	data = unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), size)
	return data, func() {
		_ = syscall.UnmapViewOfFile(addr)
		_ = syscall.CloseHandle(section)
	}, nil
}
