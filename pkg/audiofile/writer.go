package audiofile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Writer encodes an audio file.
type Writer interface {
	// Write appends frames, one slice per channel, up to the shortest slice.
	Write(src [][]float32) error
	// Close finishes the file (and closes it when Create opened it).
	Close() error
}

// NewWriter starts a WAV, AIFF or FLAC file on w.
func NewWriter(w io.WriteSeeker, f Format, sampleRate float64, channels int, e Encoding) (Writer, error) {
	switch f {
	case WAV:
		return NewWAVWriter(w, sampleRate, channels, e)
	case AIFF:
		return NewAIFFWriter(w, sampleRate, channels, e)
	case FLAC:
		return NewFLACWriter(w, sampleRate, channels, e)
	}
	return nil, fmt.Errorf("audiofile: %v files cannot be written", f)
}

// FormatOf returns the format for a file name: .wav, .aif/.aiff/.aifc or
// .flac.
func FormatOf(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav", ".wave":
		return WAV, nil
	case ".aif", ".aiff", ".aifc":
		return AIFF, nil
	case ".flac":
		return FLAC, nil
	}
	return 0, fmt.Errorf("audiofile: no writable format for %q", path)
}

// Create creates a file, in the format its name gives (FormatOf).
func Create(path string, sampleRate float64, channels int, e Encoding) (Writer, error) {
	format, err := FormatOf(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(path) // #nosec G304 - the caller chooses the file
	if err != nil {
		return nil, err
	}
	w, err := NewWriter(f, format, sampleRate, channels, e)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &fileWriter{Writer: w, f: f}, nil
}

type fileWriter struct {
	Writer
	f *os.File
}

func (w *fileWriter) Close() error {
	return errors.Join(w.Writer.Close(), w.f.Close())
}

// Save writes a buffer to a file, in the format its name gives.
func Save(path string, b *Buffer, e Encoding) error {
	if b == nil || len(b.Channels) == 0 {
		return errors.New("audiofile: no channels")
	}
	w, err := Create(path, b.SampleRate, len(b.Channels), e)
	if err != nil {
		return err
	}
	if err := w.Write(b.Channels); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// Encode writes a buffer as a whole WAV, AIFF or FLAC file to w, which need
// not be seekable.
func Encode(w io.Writer, f Format, b *Buffer, e Encoding) error {
	if b == nil || len(b.Channels) == 0 {
		return errors.New("audiofile: no channels")
	}
	var mem seekBuffer
	enc, err := NewWriter(&mem, f, b.SampleRate, len(b.Channels), e)
	if err != nil {
		return err
	}
	if err := enc.Write(b.Channels); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	_, err = w.Write(mem.data)
	return err
}
