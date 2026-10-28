package preset

// guidOrder swaps between TUID memory order and the textual GUID order:
// COM GUIDs store Data1, Data2 and Data3 little-endian. It is its own
// inverse.
func guidOrder(id [16]byte) [16]byte {
	id[0], id[1], id[2], id[3] = id[3], id[2], id[1], id[0]
	id[4], id[5] = id[5], id[4]
	id[6], id[7] = id[7], id[6]
	return id
}
