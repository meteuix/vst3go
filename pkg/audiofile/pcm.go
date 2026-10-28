package audiofile

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// pcmReader reads the uncompressed sample data of WAV and AIFF files.
type pcmReader struct {
	info      Info
	rs        io.ReadSeeker
	dataStart int64
	pos       int64 // frame
	seeked    bool  // rs is at dataStart + pos*blockAlign
	bytes     int   // container bytes per sample
	blockSize int   // bytes per frame
	float     bool
	bigEndian bool
	unsigned  bool // 8-bit WAV
	raw       []byte
}

func (r *pcmReader) Info() Info   { return r.info }
func (r *pcmReader) Close() error { return nil }

func (r *pcmReader) SeekFrame(frame int64) error {
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("audiofile: seek to frame %d of %d", frame, r.info.Frames)
	}
	r.pos = frame
	r.seeked = false
	return nil
}

func (r *pcmReader) Read(dst [][]float32) (int, error) {
	n, err := checkDst(dst, r.info.Channels)
	if err != nil {
		return 0, err
	}
	if left := r.info.Frames - r.pos; int64(n) > left {
		n = int(left)
	}
	if n == 0 {
		if r.pos >= r.info.Frames {
			return 0, io.EOF
		}
		return 0, nil
	}
	if !r.seeked {
		if _, err := r.rs.Seek(r.dataStart+r.pos*int64(r.blockSize), io.SeekStart); err != nil {
			return 0, err
		}
		r.seeked = true
	}
	size := n * r.blockSize
	if cap(r.raw) < size {
		r.raw = make([]byte, size)
	}
	raw := r.raw[:size]
	if _, err := io.ReadFull(r.rs, raw); err != nil {
		r.seeked = false
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return 0, fmt.Errorf("audiofile: sample data ends early: %w", io.ErrUnexpectedEOF)
		}
		return 0, err
	}
	channels := r.info.Channels
	for i := 0; i < n; i++ {
		frame := raw[i*r.blockSize:]
		for ch := 0; ch < channels; ch++ {
			dst[ch][i] = r.sample(frame[ch*r.bytes : (ch+1)*r.bytes])
		}
	}
	r.pos += int64(n)
	return n, nil
}

// sample converts one stored sample; integers are left-justified in their
// container, so the container size sets the full scale.
func (r *pcmReader) sample(b []byte) float32 {
	var order binary.ByteOrder = binary.LittleEndian
	if r.bigEndian {
		order = binary.BigEndian
	}
	if r.float {
		if r.bytes == 8 {
			return float32(math.Float64frombits(order.Uint64(b)))
		}
		return math.Float32frombits(order.Uint32(b))
	}
	var v int32
	switch r.bytes {
	case 1:
		if r.unsigned {
			return float32(int32(b[0])-128) / 128
		}
		return float32(int8(b[0])) / 128
	case 2:
		v = int32(int16(order.Uint16(b)))
		return float32(v) / (1 << 15)
	case 3:
		if r.bigEndian {
			v = int32(b[0])<<24 | int32(b[1])<<16 | int32(b[2])<<8
		} else {
			v = int32(b[2])<<24 | int32(b[1])<<16 | int32(b[0])<<8
		}
		return float32(float64(v>>8) / (1 << 23))
	case 4:
		v = int32(order.Uint32(b))
		return float32(float64(v) / (1 << 31))
	}
	return 0
}

// Encoding is the sample encoding of a written file.
type Encoding int

const (
	PCM16 Encoding = iota + 1
	PCM24
	PCM32
	Float32
)

func (e Encoding) bits() int {
	switch e {
	case PCM16:
		return 16
	case PCM24:
		return 24
	case PCM32, Float32:
		return 32
	}
	return 0
}

// putSample stores x little-endian in the encoding, clipping integers.
func putSample(b []byte, x float32, e Encoding) {
	if e == Float32 {
		binary.LittleEndian.PutUint32(b, math.Float32bits(x))
		return
	}
	bits := e.bits()
	scale := float64(int64(1) << (bits - 1))
	v := math.Round(float64(x) * scale)
	if math.IsNaN(v) {
		v = 0
	}
	v = math.Max(-scale, math.Min(scale-1, v))
	i := int32(v)
	switch e {
	case PCM16:
		binary.LittleEndian.PutUint16(b, uint16(int16(i)))
	case PCM24:
		b[0], b[1], b[2] = byte(i), byte(i>>8), byte(i>>16)
	case PCM32:
		binary.LittleEndian.PutUint32(b, uint32(i))
	}
}
