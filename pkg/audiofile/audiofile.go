// Package audiofile reads and writes audio files for plugins and tools:
// samplers loading their samples, convolution loading impulse responses,
// tests and the standalone app writing renders.
//
// Decoding: WAV (RIFF and RF64; 8 to 32-bit PCM, 32 and 64-bit float,
// WAVE_FORMAT_EXTENSIBLE), AIFF and AIFF-C (PCM, little-endian "sowt",
// 32 and 64-bit float), FLAC, Ogg Vorbis and MP3. The format comes from the
// file contents, not the name. Sample loop points and the root note of WAV
// (smpl) and AIFF (INST, MARK) files are part of the Info.
//
// Encoding: WAV (16, 24 and 32-bit PCM, 32-bit float) and FLAC (16 and
// 24-bit).
//
// Files are read and written on a background goroutine, never on the audio
// thread; hand the decoded Buffer to the processor through an
// atomic.Pointer.
package audiofile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// Format is a file format.
type Format int

const (
	WAV Format = iota + 1
	AIFF
	FLAC
	OggVorbis
	MP3
)

func (f Format) String() string {
	switch f {
	case WAV:
		return "WAV"
	case AIFF:
		return "AIFF"
	case FLAC:
		return "FLAC"
	case OggVorbis:
		return "Ogg Vorbis"
	case MP3:
		return "MP3"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// Loop is a sample loop, in frames: Start is the first frame of the loop,
// End the frame after its last.
type Loop struct {
	Start, End int64
}

// Info describes an audio file.
type Info struct {
	Format     Format
	SampleRate float64
	Channels   int
	Frames     int64 // length in frames
	BitDepth   int   // bits per sample of the encoding, 0 for lossy formats
	Float      bool  // floating-point samples
	RootNote   int   // MIDI note of the recorded pitch, -1 when the file has none
	Loops      []Loop
}

// Reader decodes an audio file.
type Reader interface {
	Info() Info
	// Read decodes the next frames into dst, one slice per channel
	// (len(dst) == Info().Channels), up to the shortest slice, and returns
	// the number of frames. At the end it returns 0, io.EOF.
	Read(dst [][]float32) (int, error)
	// SeekFrame moves to a frame.
	SeekFrame(frame int64) error
	// Close closes the file the reader opened (Open), or does nothing.
	Close() error
}

// Buffer is decoded audio: one slice of samples per channel.
type Buffer struct {
	SampleRate float64
	Channels   [][]float32
}

// Frames returns the length of the buffer.
func (b *Buffer) Frames() int {
	if b == nil || len(b.Channels) == 0 {
		return 0
	}
	return len(b.Channels[0])
}

// ErrFormat reports a file that is not in a supported format.
var ErrFormat = errors.New("audiofile: unsupported or invalid file")

// Open opens a file for decoding.
func Open(path string) (Reader, error) {
	f, err := os.Open(path) // #nosec G304 - the caller chooses the file
	if err != nil {
		return nil, err
	}
	r, err := newReader(f, f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// NewReader decodes from rs.
func NewReader(rs io.ReadSeeker) (Reader, error) {
	return newReader(rs, nil)
}

// Load decodes a whole file.
func Load(path string) (*Buffer, error) {
	r, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readAll(r)
}

// Decode decodes a whole stream.
func Decode(rs io.ReadSeeker) (*Buffer, error) {
	r, err := NewReader(rs)
	if err != nil {
		return nil, err
	}
	return readAll(r)
}

// DecodeBytes decodes a file held in memory (for example embedded with
// go:embed).
func DecodeBytes(data []byte) (*Buffer, error) {
	return Decode(bytes.NewReader(data))
}

func readAll(r Reader) (*Buffer, error) {
	info := r.Info()
	b := &Buffer{SampleRate: info.SampleRate, Channels: make([][]float32, info.Channels)}
	if info.Frames > 0 {
		for ch := range b.Channels {
			b.Channels[ch] = make([]float32, 0, info.Frames)
		}
	}
	block := make([][]float32, info.Channels)
	for ch := range block {
		block[ch] = make([]float32, 4096)
	}
	for {
		n, err := r.Read(block)
		for ch := range block {
			b.Channels[ch] = append(b.Channels[ch], block[ch][:n]...)
		}
		if err == io.EOF {
			return b, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func newReader(rs io.ReadSeeker, closer io.Closer) (Reader, error) {
	format, err := sniff(rs)
	if err != nil {
		return nil, err
	}
	var r Reader
	switch format {
	case WAV:
		r, err = newWAVReader(rs)
	case AIFF:
		r, err = newAIFFReader(rs)
	case FLAC:
		r, err = newFLACReader(rs)
	case OggVorbis:
		r, err = newVorbisReader(rs)
	case MP3:
		r, err = newMP3Reader(rs)
	}
	if err != nil {
		return nil, err
	}
	if closer != nil {
		r = closingReader{r, closer}
	}
	return r, nil
}

type closingReader struct {
	Reader
	c io.Closer
}

func (r closingReader) Close() error { return r.c.Close() }

// sniff finds the format from the start of the stream and rewinds it.
func sniff(rs io.ReadSeeker) (Format, error) {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	head := make([]byte, 12)
	n, _ := io.ReadFull(rs, head)
	head = head[:n]
	format := Format(0)
	switch {
	case len(head) >= 12 && (string(head[:4]) == "RIFF" || string(head[:4]) == "RF64") && string(head[8:12]) == "WAVE":
		format = WAV
	case len(head) >= 12 && string(head[:4]) == "FORM" && (string(head[8:12]) == "AIFF" || string(head[8:12]) == "AIFC"):
		format = AIFF
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		format = FLAC
	case len(head) >= 4 && string(head[:4]) == "OggS":
		format = OggVorbis
	case len(head) >= 10 && string(head[:3]) == "ID3":
		// An ID3v2 tag precedes FLAC or MP3 data.
		size := int64(head[6]&0x7F)<<21 | int64(head[7]&0x7F)<<14 | int64(head[8]&0x7F)<<7 | int64(head[9]&0x7F)
		if head[5]&0x10 != 0 {
			size += 10 // footer
		}
		if _, err := rs.Seek(10+size, io.SeekStart); err != nil {
			return 0, err
		}
		after := make([]byte, 4)
		if _, err := io.ReadFull(rs, after); err == nil && string(after) == "fLaC" {
			format = FLAC
		} else if err == nil && mp3Sync(after) {
			format = MP3
		}
	case len(head) >= 2 && mp3Sync(head):
		format = MP3
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if format == 0 {
		return 0, ErrFormat
	}
	return format, nil
}

// mp3Sync reports an MPEG audio frame header (layer I-III).
func mp3Sync(b []byte) bool {
	return len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0 && b[1]&0x06 != 0
}

// checkDst validates a Read destination and returns its frame capacity.
func checkDst(dst [][]float32, channels int) (int, error) {
	if len(dst) != channels {
		return 0, fmt.Errorf("audiofile: read into %d channels, the file has %d", len(dst), channels)
	}
	n := -1
	for _, ch := range dst {
		if n < 0 || len(ch) < n {
			n = len(ch)
		}
	}
	if n < 0 {
		n = 0
	}
	return n, nil
}

// bufferedReadSeeker adds buffering to sequential reads of a ReadSeeker.
type bufferedReadSeeker struct {
	rs  io.ReadSeeker
	buf *bufio.Reader
}

func newBufferedReadSeeker(rs io.ReadSeeker) *bufferedReadSeeker {
	return &bufferedReadSeeker{rs, bufio.NewReaderSize(rs, 64<<10)}
}

func (b *bufferedReadSeeker) Read(p []byte) (int, error) { return b.buf.Read(p) }

func (b *bufferedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		offset -= int64(b.buf.Buffered())
	}
	pos, err := b.rs.Seek(offset, whence)
	b.buf.Reset(b.rs)
	return pos, err
}
