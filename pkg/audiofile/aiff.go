package audiofile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

func newAIFFReader(rs io.ReadSeeker) (Reader, error) {
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
	aifc := string(head[8:12]) == "AIFC"
	be := binary.BigEndian
	var channels, bits int
	var frames int64
	var rate float64
	compression := "NONE"
	var haveComm bool
	var dataStart int64 = -1
	markers := map[int16]int64{}
	var inst []byte
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
		size := int64(be.Uint32(ch[4:]))
		body := pos + 8
		switch id {
		case "COMM":
			b := make([]byte, min(size, 64))
			if _, err := io.ReadFull(rs, b); err != nil || len(b) < 18 {
				return nil, ErrFormat
			}
			channels = int(be.Uint16(b[0:]))
			frames = int64(be.Uint32(b[2:]))
			bits = int(be.Uint16(b[6:]))
			rate = extendedToFloat(b[8:18])
			if aifc {
				if len(b) < 22 {
					return nil, ErrFormat
				}
				compression = string(b[18:22])
			}
			haveComm = true
		case "SSND":
			var b [8]byte
			if _, err := io.ReadFull(rs, b[:]); err != nil {
				return nil, ErrFormat
			}
			dataStart = body + 8 + int64(be.Uint32(b[0:]))
		case "MARK":
			b := make([]byte, min(size, 1<<16))
			if _, err := io.ReadFull(rs, b); err == nil && len(b) >= 2 {
				n := int(be.Uint16(b))
				p := 2
				for i := 0; i < n && p+7 <= len(b); i++ {
					markers[int16(be.Uint16(b[p:]))] = int64(be.Uint32(b[p+2:]))
					nameLen := int(b[p+6])
					p += 7 + nameLen
					if (1+nameLen)&1 != 0 {
						p++
					}
				}
			}
		case "INST":
			inst = make([]byte, min(size, 20))
			if _, err := io.ReadFull(rs, inst); err != nil {
				inst = nil
			}
		}
		pos = body + size + size&1
	}
	if !haveComm || dataStart < 0 || channels == 0 || rate <= 0 {
		return nil, ErrFormat
	}
	r := &pcmReader{rs: newBufferedReadSeeker(rs), dataStart: dataStart, bigEndian: true}
	switch compression {
	case "NONE", "twos", "sowt", "raw ":
		if bits < 1 || bits > 32 {
			return nil, fmt.Errorf("%w: AIFF with %d-bit samples", ErrFormat, bits)
		}
		r.bytes = (bits + 7) / 8
		r.bigEndian = compression != "sowt"
		r.unsigned = compression == "raw " && r.bytes == 1
		if compression == "raw " && r.bytes != 1 {
			return nil, fmt.Errorf("%w: AIFF-C raw with %d-bit samples", ErrFormat, bits)
		}
	case "fl32", "FL32":
		r.bytes, r.float, bits = 4, true, 32
	case "fl64", "FL64":
		r.bytes, r.float, bits = 8, true, 64
	default:
		return nil, fmt.Errorf("%w: AIFF-C compression %q", ErrFormat, compression)
	}
	r.blockSize = r.bytes * channels
	if avail := (end - dataStart) / int64(r.blockSize); frames > avail {
		frames = max(avail, 0)
	}
	info := Info{Format: AIFF, SampleRate: rate, Channels: channels, Frames: frames, BitDepth: bits, Float: r.float, RootNote: -1}
	if len(inst) >= 20 {
		if note := int8(inst[0]); note >= 0 {
			info.RootNote = int(note)
		}
		for _, l := range [][]byte{inst[8:14], inst[14:20]} {
			if be.Uint16(l) == 0 {
				continue // no looping
			}
			start, ok1 := markers[int16(be.Uint16(l[2:]))]
			stop, ok2 := markers[int16(be.Uint16(l[4:]))]
			if ok1 && ok2 && stop > start {
				info.Loops = append(info.Loops, Loop{min(start, frames), min(stop, frames)})
			}
		}
	}
	r.info = info
	return r, nil
}

// extendedToFloat decodes an 80-bit IEEE 754 extended float.
func extendedToFloat(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b)&0x7FFF) - 16383 - 63
	mant := binary.BigEndian.Uint64(b[2:])
	v := math.Ldexp(float64(mant), exp)
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}

// floatToExtended encodes a positive value as an 80-bit extended float.
func floatToExtended(v float64) [10]byte {
	var b [10]byte
	if v <= 0 {
		return b
	}
	frac, exp := math.Frexp(v) // v = frac * 2^exp, frac in [0.5, 1)
	binary.BigEndian.PutUint16(b[:], uint16(exp-1+16383))
	binary.BigEndian.PutUint64(b[2:], uint64(math.Ldexp(frac, 64)))
	return b
}

// AIFFWriter writes an AIFF file (AIFF-C for float samples) as it goes;
// Close writes the final sizes.
type AIFFWriter struct {
	w        io.WriteSeeker
	channels int
	encoding Encoding
	frames   int64
	commPos  int64 // offset of the frame count in COMM
	ssndPos  int64 // offset of the SSND chunk
	raw      []byte
	closed   bool
}

// NewAIFFWriter starts an AIFF file.
func NewAIFFWriter(w io.WriteSeeker, sampleRate float64, channels int, e Encoding) (*AIFFWriter, error) {
	if channels < 1 || channels > math.MaxInt16 {
		return nil, fmt.Errorf("audiofile: %d channels", channels)
	}
	if e.bits() == 0 {
		return nil, fmt.Errorf("audiofile: unknown encoding %d", e)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("audiofile: sample rate %g", sampleRate)
	}
	be := binary.BigEndian
	var h bytes.Buffer
	h.WriteString("FORM")
	_ = binary.Write(&h, be, uint32(0))
	comm := []byte{}
	comm = be.AppendUint16(comm, uint16(channels))
	comm = be.AppendUint32(comm, 0)
	comm = be.AppendUint16(comm, uint16(e.bits()))
	rate := floatToExtended(sampleRate)
	comm = append(comm, rate[:]...)
	if e == Float32 {
		h.WriteString("AIFC")
		h.WriteString("FVER")
		_ = binary.Write(&h, be, uint32(4))
		_ = binary.Write(&h, be, uint32(0xA2805140)) // AIFF-C version 1
		comm = append(comm, "fl32"...)
		comm = append(comm, 0, 0) // empty name, padded
	} else {
		h.WriteString("AIFF")
	}
	h.WriteString("COMM")
	_ = binary.Write(&h, be, uint32(len(comm)))
	commPos := int64(h.Len()) + 2
	h.Write(comm)
	ssndPos := int64(h.Len())
	h.WriteString("SSND")
	_ = binary.Write(&h, be, uint32(8))
	_ = binary.Write(&h, be, uint64(0)) // offset, block size
	if _, err := w.Write(h.Bytes()); err != nil {
		return nil, err
	}
	return &AIFFWriter{w: w, channels: channels, encoding: e, commPos: commPos, ssndPos: ssndPos}, nil
}

// Write appends frames, one slice per channel, up to the shortest slice.
func (w *AIFFWriter) Write(src [][]float32) error {
	if w.closed {
		return errors.New("audiofile: write after close")
	}
	n, err := checkDst(src, w.channels)
	if err != nil {
		return err
	}
	sampleBytes := w.encoding.bits() / 8
	block := sampleBytes * w.channels
	if w.frames+int64(n) > math.MaxUint32 || (w.frames+int64(n))*int64(block) > math.MaxUint32-w.ssndPos-16 {
		return errors.New("audiofile: AIFF files hold at most 4 GiB of samples")
	}
	if cap(w.raw) < n*block {
		w.raw = make([]byte, n*block)
	}
	raw := w.raw[:n*block]
	for i := 0; i < n; i++ {
		for ch := 0; ch < w.channels; ch++ {
			s := raw[i*block+ch*sampleBytes : i*block+(ch+1)*sampleBytes]
			putSample(s, src[ch][i], w.encoding)
			for a, b := 0, len(s)-1; a < b; a, b = a+1, b-1 {
				s[a], s[b] = s[b], s[a]
			}
		}
	}
	if _, err := w.w.Write(raw); err != nil {
		return err
	}
	w.frames += int64(n)
	return nil
}

// Close writes the sizes into the header. It does not close the file.
func (w *AIFFWriter) Close() error {
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
	be := binary.BigEndian
	for _, field := range []struct {
		pos int64
		v   uint32
	}{
		{4, uint32(end - 8)},
		{w.commPos, uint32(w.frames)},
		{w.ssndPos + 4, uint32(8 + dataSize)},
	} {
		if _, err := w.w.Seek(field.pos, io.SeekStart); err != nil {
			return err
		}
		if _, err := w.w.Write(be.AppendUint32(nil, field.v)); err != nil {
			return err
		}
	}
	_, err = w.w.Seek(end, io.SeekStart)
	return err
}
