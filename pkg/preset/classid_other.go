//go:build !windows

package preset

// guidOrder is the identity: outside Windows TUIDs are printed byte by byte.
func guidOrder(id [16]byte) [16]byte { return id }
