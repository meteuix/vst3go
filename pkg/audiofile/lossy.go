package audiofile

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
)

type vorbisReader struct {
	info Info
	dec  *oggvorbis.Reader
	buf  []float32
}

func newVorbisReader(rs io.ReadSeeker) (Reader, error) {
	dec, err := oggvorbis.NewReader(newBufferedReadSeeker(rs))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if dec.Channels() < 1 || dec.SampleRate() < 1 {
		return nil, ErrFormat
	}
	return &vorbisReader{
		dec: dec,
		info: Info{
			Format:     OggVorbis,
			SampleRate: float64(dec.SampleRate()),
			Channels:   dec.Channels(),
			Frames:     dec.Length(),
			RootNote:   -1,
		},
	}, nil
}

func (r *vorbisReader) Info() Info   { return r.info }
func (r *vorbisReader) Close() error { return nil }

func (r *vorbisReader) SeekFrame(frame int64) error {
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("audiofile: seek to frame %d of %d", frame, r.info.Frames)
	}
	return r.dec.SetPosition(frame)
}

func (r *vorbisReader) Read(dst [][]float32) (int, error) {
	n, err := checkDst(dst, r.info.Channels)
	if err != nil {
		return 0, err
	}
	channels := r.info.Channels
	if cap(r.buf) < n*channels {
		r.buf = make([]float32, n*channels)
	}
	got := 0
	for got < n {
		k, err := r.dec.Read(r.buf[got*channels : n*channels])
		frames := k / channels
		for i := 0; i < frames; i++ {
			for ch := 0; ch < channels; ch++ {
				dst[ch][got+i] = r.buf[(got+i)*channels+ch]
			}
		}
		got += frames
		if err == io.EOF {
			if got == 0 {
				return 0, io.EOF
			}
			break
		}
		if err != nil {
			return got, err
		}
		if k == 0 {
			break
		}
	}
	return got, nil
}

// mp3Reader decodes MPEG-1/2 layer III; the decoder always produces two
// channels.
type mp3Reader struct {
	info  Info
	dec   *mp3.Decoder
	raw   []byte
	atEnd bool // the decoder cannot seek to its end
}

func newMP3Reader(rs io.ReadSeeker) (Reader, error) {
	dec, err := mp3.NewDecoder(newBufferedReadSeeker(rs))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	return &mp3Reader{
		dec: dec,
		info: Info{
			Format:     MP3,
			SampleRate: float64(dec.SampleRate()),
			Channels:   2,
			Frames:     dec.Length() / 4,
			RootNote:   -1,
		},
	}, nil
}

func (r *mp3Reader) Info() Info   { return r.info }
func (r *mp3Reader) Close() error { return nil }

func (r *mp3Reader) SeekFrame(frame int64) error {
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("audiofile: seek to frame %d of %d", frame, r.info.Frames)
	}
	r.atEnd = frame == r.info.Frames
	if r.atEnd {
		return nil
	}
	_, err := r.dec.Seek(frame*4, io.SeekStart)
	return err
}

func (r *mp3Reader) Read(dst [][]float32) (int, error) {
	n, err := checkDst(dst, 2)
	if err != nil {
		return 0, err
	}
	if r.atEnd {
		return 0, io.EOF
	}
	if cap(r.raw) < n*4 {
		r.raw = make([]byte, n*4)
	}
	raw := r.raw[:n*4]
	k, err := io.ReadFull(r.dec, raw)
	frames := k / 4
	for i := 0; i < frames; i++ {
		dst[0][i] = float32(int16(binary.LittleEndian.Uint16(raw[4*i:]))) / (1 << 15)
		dst[1][i] = float32(int16(binary.LittleEndian.Uint16(raw[4*i+2:]))) / (1 << 15)
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if frames == 0 {
			return 0, io.EOF
		}
		return frames, nil
	}
	return frames, err
}
