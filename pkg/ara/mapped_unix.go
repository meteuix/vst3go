//go:build unix

package ara

import (
	"os"
	"syscall"
)

// mapFile maps size bytes of a new temporary file in dir, which is removed
// at once: the mapping keeps it until unmap.
func mapFile(dir string, size int) (data []byte, unmap func(), err error) {
	f, err := os.CreateTemp(dir, "source-*.f32")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	if err := f.Truncate(int64(size)); err != nil {
		return nil, nil, err
	}
	data, err = syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return data, func() { _ = syscall.Munmap(data) }, nil
}
