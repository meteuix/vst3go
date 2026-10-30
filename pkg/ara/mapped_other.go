//go:build !unix && !windows

package ara

import "errors"

// mapFile is not available here; sources stay in memory.
func mapFile(dir string, size int) (data []byte, unmap func(), err error) {
	return nil, nil, errors.New("ara: no file mappings on this platform")
}
