package audiofile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	wavFormatPCM        = 1
	wavFormatFloat      = 3
	wavFormatExtensible = 0xFFFE
)

func newWAVReader(rs io.ReadSeeker) (Reader, error) {
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var head [12]byte
	if _, err := io.ReadFull(rs, head[:]); err != nil {
		return nil, ErrFormat
	}
	rf64 := string(head[:4]) == "RF64"
	var ds64Data int64 = -1
	var format, channels, blockAlign, bits uint16
	var rate uint32
	var haveFormat bool
	var dataStart, dataSize int64 = -1, 0
	info := Info{Format: WAV, RootNote: -1}
	pos := int64(12)
	for pos+8 <= end {
		var ch [8]byte
		if _, err := rs.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(rs, ch[:]); err != nil {
			break
		}
		id := string(ch[:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:]))
		body := pos + 8
		switch id {
		case "ds64":
			var b [24]byte
			if _, err := io.ReadFull(rs, b[:]); err != nil {
				return nil, ErrFormat
			}
			ds64Data = int64(binary.LittleEndian.Uint64(b[8:]))
		case "fmt ":
			b := make([]byte, min(size, 40))
			if _, err := io.ReadFull(rs, b); err != nil || len(b) < 16 {
				return nil, ErrFormat
			}
			format = binary.LittleEndian.Uint16(b[0:])
			channels = binary.LittleEndian.Uint16(b[2:])
			rate = binary.LittleEndian.Uint32(b[4:])
			blockAlign = binary.LittleEndian.Uint16(b[12:])
			bits = binary.LittleEndian.Uint16(b[14:])
			if format == wavFormatExtensible {
				if len(b) < 40 {
					return nil, ErrFormat
				}
				if valid := binary.LittleEndian.Uint16(b[18:]); valid > 0 && valid <= bits {
					bits = valid
				}
				format = binary.LittleEndian.Uint16(b[24:]) // the subformat GUID starts with the format tag
			}
			haveFormat = true
		case "data":
			dataStart = body
			dataSize = size
			if rf64 && size == 0xFFFFFFFF && ds64Data >= 0 {
				dataSize = ds64Data
			}
			if dataStart+dataSize > end {
				dataSize = end - dataStart // a truncated or still recording file
			}
		case "smpl":
			b := make([]byte, min(size, 36+24*64))
			if _, err := io.ReadFull(rs, b); err == nil && len(b) >= 36 {
				note := binary.LittleEndian.Uint32(b[12:])
				if note <= 127 {
					info.RootNote = int(note)
				}
				loops := int(binary.LittleEndian.Uint32(b[28:]))
				for i := 0; i < loops && 36+24*(i+1) <= len(b); i++ {
					l := b[36+24*i:]
					start, last := int64(binary.LittleEndian.Uint32(l[8:])), int64(binary.LittleEndian.Uint32(l[12:]))
					if last >= start {
						info.Loops = append(info.Loops, Loop{start, last + 1})
					}
				}
			}
		}
		if rf64 && id == "data" && size == 0xFFFFFFFF {
			size = dataSize
		}
		pos = body + size + size&1
	}
	if !haveFormat || dataStart < 0 || channels == 0 || rate == 0 || blockAlign == 0 {
		return nil, ErrFormat
	}
	r := &pcmReader{rs: newBufferedReadSeeker(rs), dataStart: dataStart, blockSize: int(blockAlign)}
	r.bytes = int(blockAlign) / int(channels)
	switch {
	case format == wavFormatPCM && bits >= 1 && bits <= 32 && r.bytes >= 1 && r.bytes <= 4 && int(bits) <= 8*r.bytes:
		r.unsigned = r.bytes == 1
	case format == wavFormatFloat && (bits == 32 && r.bytes == 4 || bits == 64 && r.bytes == 8):
		r.float = true
	default:
		return nil, fmt.Errorf("%w: WAV format %d with %d-bit samples", ErrFormat, format, bits)
	}
	if r.bytes*int(channels) != int(blockAlign) {
		return nil, ErrFormat
	}
	info.SampleRate = float64(rate)
	info.Channels = int(channels)
	info.Frames = dataSize / int64(blockAlign)
	info.BitDepth = int(bits)
	info.Float = r.float
	for i, l := range info.Loops {
		info.Loops[i] = Loop{min(l.Start, info.Frames), min(l.End, info.Frames)}
	}
	r.info = info
	return r, nil
}

// WAVWriter writes a WAV file as it goes; Close writes the final sizes.
type WAVWriter struct {
	w          io.WriteSeeker
	channels   int
	encoding   Encoding
	frames     int64
	headerSize int64
	raw        []byte
	closed     bool
}

// NewWAVWriter starts a WAV file. Multichannel files (more than two
// channels) use WAVE_FORMAT_EXTENSIBLE.
func NewWAVWriter(w io.WriteSeeker, sampleRate float64, channels int, e Encoding) (*WAVWriter, error) {
	if channels < 1 || channels > 64 {
		return nil, fmt.Errorf("audiofile: %d channels", channels)
	}
	if e.bits() == 0 {
		return nil, fmt.Errorf("audiofile: unknown encoding %d", e)
	}
	if sampleRate < 1 || sampleRate > math.MaxUint32 {
		return nil, fmt.Errorf("audiofile: sample rate %g", sampleRate)
	}
	header := wavHeader(uint32(math.Round(sampleRate)), channels, e, 0)
	if _, err := w.Write(header); err != nil {
		return nil, err
	}
	return &WAVWriter{w: w, channels: channels, encoding: e, headerSize: int64(len(header))}, nil
}

// Write appends frames, one slice per channel, up to the shortest slice.
func (w *WAVWriter) Write(src [][]float32) error {
	if w.closed {
		return errors.New("audiofile: write after close")
	}
	n, err := checkDst(src, w.channels)
	if err != nil {
		return err
	}
	sampleBytes := w.encoding.bits() / 8
	block := sampleBytes * w.channels
	if (w.frames+int64(n))*int64(block) > math.MaxUint32-w.headerSize {
		return errors.New("audiofile: WAV files hold at most 4 GiB of samples")
	}
	if cap(w.raw) < n*block {
		w.raw = make([]byte, n*block)
	}
	raw := w.raw[:n*block]
	for i := 0; i < n; i++ {
		for ch := 0; ch < w.channels; ch++ {
			putSample(raw[i*block+ch*sampleBytes:], src[ch][i], w.encoding)
		}
	}
	if _, err := w.w.Write(raw); err != nil {
		return err
	}
	w.frames += int64(n)
	return nil
}

// Close writes the sizes into the header. It does not close the file.
func (w *WAVWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	dataSize := w.frames * int64(w.channels*w.encoding.bits()/8)
	if dataSize&1 != 0 {
		if _, err := w.w.Write([]byte{0}); err != nil {
			return err
		}
	}
	end, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(end-8))
	if _, err := w.w.Seek(4, io.SeekStart); err != nil {
		return err
	}
	if _, err := w.w.Write(b[:]); err != nil {
		return err
	}
	if w.encoding == Float32 && w.channels <= 2 {
		// fact chunk: frames
		binary.LittleEndian.PutUint32(b[:], uint32(w.frames))
		if _, err := w.w.Seek(w.headerSize-12, io.SeekStart); err != nil {
			return err
		}
		if _, err := w.w.Write(b[:]); err != nil {
			return err
		}
	}
	binary.LittleEndian.PutUint32(b[:], uint32(dataSize))
	if _, err := w.w.Seek(w.headerSize-4, io.SeekStart); err != nil {
		return err
	}
	if _, err := w.w.Write(b[:]); err != nil {
		return err
	}
	_, err = w.w.Seek(end, io.SeekStart)
	return err
}

// wavHeader is everything before the samples, for frames frames.
func wavHeader(rate uint32, channels int, e Encoding, frames int64) []byte {
	bits := e.bits()
	block := channels * bits / 8
	dataSize := uint32(frames * int64(block))
	var fmtChunk bytes.Buffer
	le := binary.LittleEndian
	tag := uint16(wavFormatPCM)
	if e == Float32 {
		tag = wavFormatFloat
	}
	extensible := channels > 2
	write := func(v any) { _ = binary.Write(&fmtChunk, le, v) }
	if extensible {
		write(uint16(wavFormatExtensible))
	} else {
		write(tag)
	}
	write(uint16(channels))
	write(rate)
	write(rate * uint32(block))
	write(uint16(block))
	write(uint16(bits))
	if extensible {
		write(uint16(22))
		write(uint16(bits))
		write(uint32(0)) // no speaker positions
		write(tag)
		fmtChunk.Write([]byte{0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71})
	} else if e == Float32 {
		write(uint16(0))
	}
	var h bytes.Buffer
	h.WriteString("RIFF")
	_ = binary.Write(&h, le, uint32(0)) // set by the caller or Close
	h.WriteString("WAVEfmt ")
	_ = binary.Write(&h, le, uint32(fmtChunk.Len()))
	h.Write(fmtChunk.Bytes())
	if e == Float32 && !extensible {
		h.WriteString("fact")
		_ = binary.Write(&h, le, uint32(4))
		_ = binary.Write(&h, le, uint32(frames))
	}
	h.WriteString("data")
	_ = binary.Write(&h, le, dataSize)
	b := h.Bytes()
	riff := uint32(len(b)-8) + dataSize + dataSize&1
	le.PutUint32(b[4:], riff)
	return b
}

// seekBuffer is an in-memory io.WriteSeeker.
type seekBuffer struct {
	data []byte
	pos  int
}

func (s *seekBuffer) Write(p []byte) (int, error) {
	if need := s.pos + len(p); need > len(s.data) {
		s.data = append(s.data, make([]byte, need-len(s.data))...)
	}
	copy(s.data[s.pos:], p)
	s.pos += len(p)
	return len(p), nil
}

func (s *seekBuffer) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += int64(s.pos)
	case io.SeekEnd:
		offset += int64(len(s.data))
	}
	if offset < 0 {
		return 0, errors.New("audiofile: negative seek")
	}
	s.pos = int(offset)
	return offset, nil
}
