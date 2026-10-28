// Package preset reads and writes VST3 preset files (.vstpreset) and manages
// preset folders in the standard VST3 locations.
//
// A .vstpreset holds the component (processor) state, optionally the
// controller state and an XML meta info chunk, tagged with the class ID of
// the plugin's processor. Hosts load these files directly, so presets saved
// by a vst3go plugin appear in the host's preset browser and vice versa.
package preset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Extension is the file extension of VST3 preset files.
const Extension = ".vstpreset"

const (
	formatVersion = 1
	classIDSize   = 32 // ASCII hex class ID
	headerSize    = 4 + 4 + classIDSize + 8
	maxEntries    = 128
)

// Chunk IDs (Steinberg::Vst::PresetFile).
var (
	chunkHeader     = [4]byte{'V', 'S', 'T', '3'}
	chunkComponent  = [4]byte{'C', 'o', 'm', 'p'}
	chunkController = [4]byte{'C', 'o', 'n', 't'}
	chunkMetaInfo   = [4]byte{'I', 'n', 'f', 'o'}
	chunkList       = [4]byte{'L', 'i', 's', 't'}
)

// ErrFormat reports a file that is not a valid VST3 preset.
var ErrFormat = errors.New("preset: not a VST3 preset file")

// File is the content of a .vstpreset file.
type File struct {
	// ClassID is the processor class ID as reported by the plugin factory.
	ClassID [16]byte
	// Component is the processor state (IComponent::getState).
	Component []byte
	// Controller is the optional controller state (IEditController::getState).
	Controller []byte
	// MetaInfo is the optional XML meta info chunk; see MetaInfo.
	MetaInfo []byte
}

// Encode returns the file in the .vstpreset format.
func (f *File) Encode() []byte {
	type entry struct {
		id           [4]byte
		offset, size int64
	}
	var buf bytes.Buffer
	buf.Write(chunkHeader[:])
	binary.Write(&buf, binary.LittleEndian, int32(formatVersion))
	buf.WriteString(ClassIDString(f.ClassID))
	binary.Write(&buf, binary.LittleEndian, int64(0)) // list offset, patched below

	var entries []entry
	add := func(id [4]byte, data []byte) {
		entries = append(entries, entry{id, int64(buf.Len()), int64(len(data))})
		buf.Write(data)
	}
	add(chunkComponent, f.Component)
	if len(f.Controller) > 0 {
		add(chunkController, f.Controller)
	}
	if len(f.MetaInfo) > 0 {
		add(chunkMetaInfo, f.MetaInfo)
	}

	listOffset := int64(buf.Len())
	buf.Write(chunkList[:])
	binary.Write(&buf, binary.LittleEndian, int32(len(entries)))
	for _, e := range entries {
		buf.Write(e.id[:])
		binary.Write(&buf, binary.LittleEndian, e.offset)
		binary.Write(&buf, binary.LittleEndian, e.size)
	}
	data := buf.Bytes()
	binary.LittleEndian.PutUint64(data[headerSize-8:], uint64(listOffset))
	return data
}

// Decode parses a .vstpreset file. It needs a component state chunk.
func Decode(data []byte) (*File, error) {
	if len(data) < headerSize || !bytes.Equal(data[:4], chunkHeader[:]) {
		return nil, ErrFormat
	}
	classID, err := ParseClassID(string(data[8 : 8+classIDSize]))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	listOffset := int64(binary.LittleEndian.Uint64(data[headerSize-8:]))
	if listOffset < headerSize || listOffset > int64(len(data))-8 || !bytes.Equal(data[listOffset:listOffset+4], chunkList[:]) {
		return nil, fmt.Errorf("%w: missing chunk list", ErrFormat)
	}
	count := int(int32(binary.LittleEndian.Uint32(data[listOffset+4:])))
	if count < 0 || count > maxEntries || listOffset+8+int64(count)*20 > int64(len(data)) {
		return nil, fmt.Errorf("%w: bad chunk list", ErrFormat)
	}

	f := &File{ClassID: classID}
	hasComponent := false
	for i := 0; i < count; i++ {
		e := data[listOffset+8+int64(i)*20:]
		offset := int64(binary.LittleEndian.Uint64(e[4:]))
		size := int64(binary.LittleEndian.Uint64(e[12:]))
		if offset < headerSize || size < 0 || offset+size > listOffset {
			return nil, fmt.Errorf("%w: chunk %q out of range", ErrFormat, e[:4])
		}
		chunk := bytes.Clone(data[offset : offset+size])
		switch [4]byte(e[:4]) {
		case chunkComponent:
			f.Component, hasComponent = chunk, true
		case chunkController:
			f.Controller = chunk
		case chunkMetaInfo:
			f.MetaInfo = chunk
		}
	}
	if !hasComponent {
		return nil, fmt.Errorf("%w: no component state", ErrFormat)
	}
	return f, nil
}

// ReadFile reads and decodes a .vstpreset file.
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// WriteFile writes f to path, replacing an existing file atomically.
func WriteFile(path string, f *File) error {
	tmp, err := os.CreateTemp(dirOf(path), ".vstpreset-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, bytes.NewReader(f.Encode())); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
