package sampler

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/meteuix/vst3go/v2/pkg/audiofile"
)

// MaxFileSeconds is the longest audio file LoadSample accepts.
const MaxFileSeconds = 10 * 60

// DefaultRoot is the root note of files that do not name one.
const DefaultRoot = 60

const readChunk = 1 << 16 // frames decoded per read

// LoadSample decodes an audio file (WAV, AIFF, FLAC, MP3 or Ogg Vorbis)
// into a Sample with the file's root note (DefaultRoot when it has none)
// and its first loop. Only the first two channels are kept. It allocates
// and reads the file; call it off the audio thread.
func LoadSample(path string) (*Sample, error) {
	r, err := audiofile.Open(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	info := r.Info()
	if info.Channels < 1 || !(info.SampleRate > 0) {
		return nil, fmt.Errorf("%s: no audio", filepath.Base(path))
	}
	limit := int(MaxFileSeconds * info.SampleRate)
	data := make([][]float32, info.Channels)
	chunk := make([][]float32, info.Channels)
	for ch := range chunk {
		chunk[ch] = make([]float32, readChunk)
	}
	for len(data[0]) <= limit {
		n, err := r.Read(chunk)
		for ch := range data {
			data[ch] = append(data[ch], chunk[ch][:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if len(data[0]) == 0 {
		return nil, fmt.Errorf("%s: no audio", filepath.Base(path))
	}
	if len(data[0]) > limit {
		return nil, fmt.Errorf("%s: longer than %d minutes", filepath.Base(path), MaxFileSeconds/60)
	}
	root := float64(DefaultRoot)
	if info.RootNote >= 0 {
		root = float64(info.RootNote)
	}
	s, err := NewSample(data[:min(2, len(data))], info.SampleRate, root)
	if err != nil {
		return nil, err
	}
	if len(info.Loops) > 0 {
		if err := s.SetLoop(int(info.Loops[0].Start), int(info.Loops[0].End)); err != nil {
			return nil, err
		}
	}
	return s, nil
}
