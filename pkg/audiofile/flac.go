package audiofile

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

type flacReader struct {
	info   Info
	stream *flac.Stream
	cur    *frame.Frame
	off    int // next sample in cur
	pos    int64
	scale  float32
}

func newFLACReader(rs io.ReadSeeker) (Reader, error) {
	stream, err := flac.NewSeek(rs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	si := stream.Info
	if si.NChannels == 0 || si.SampleRate == 0 || si.BitsPerSample == 0 || si.BitsPerSample > 32 {
		return nil, ErrFormat
	}
	r := &flacReader{stream: stream, scale: 1 / float32(int64(1)<<(si.BitsPerSample-1))}
	r.info = Info{
		Format:     FLAC,
		SampleRate: float64(si.SampleRate),
		Channels:   int(si.NChannels),
		Frames:     int64(si.NSamples),
		BitDepth:   int(si.BitsPerSample),
		RootNote:   -1,
	}
	return r, nil
}

func (r *flacReader) Info() Info   { return r.info }
func (r *flacReader) Close() error { return nil }

func (r *flacReader) Read(dst [][]float32) (int, error) {
	n, err := checkDst(dst, r.info.Channels)
	if err != nil {
		return 0, err
	}
	done := 0
	for done < n {
		if r.cur == nil || r.off >= r.cur.Subframes[0].NSamples {
			f, err := r.stream.ParseNext()
			if err == io.EOF {
				r.cur = nil
				if done == 0 {
					return 0, io.EOF
				}
				break
			}
			if err != nil {
				r.cur = nil
				return done, err
			}
			if len(f.Subframes) != r.info.Channels {
				return done, fmt.Errorf("%w: FLAC frame with %d channels", ErrFormat, len(f.Subframes))
			}
			r.cur, r.off = f, 0
		}
		count := min(n-done, r.cur.Subframes[0].NSamples-r.off)
		for ch, sub := range r.cur.Subframes {
			out := dst[ch][done : done+count]
			for i, s := range sub.Samples[r.off : r.off+count] {
				out[i] = float32(s) * r.scale
			}
		}
		r.off += count
		done += count
	}
	r.pos += int64(done)
	return done, nil
}

func (r *flacReader) SeekFrame(target int64) error {
	if target < 0 || r.info.Frames > 0 && target > r.info.Frames {
		return fmt.Errorf("audiofile: seek to frame %d of %d", target, r.info.Frames)
	}
	r.cur = nil
	if target == r.info.Frames {
		r.pos = target
		_, err := r.stream.Seek(uint64(max(target-1, 0)))
		if err == nil {
			_, err = r.stream.ParseNext() // the last frame
		}
		return err
	}
	start, err := r.stream.Seek(uint64(target))
	if err != nil {
		return err
	}
	r.pos = target
	f, err := r.stream.ParseNext()
	if err == io.EOF {
		return nil // an empty stream
	}
	if err != nil {
		return err
	}
	r.cur, r.off = f, int(target-int64(start))
	return nil
}

// FLACWriter encodes a FLAC file as it goes.
type FLACWriter struct {
	enc      *flac.Encoder
	out      *bufferedWriteSeeker
	channels int
	bits     int
	block    [][]int32 // the pending frame
	fill     int
	frames   int64
	closed   bool
}

const flacBlockSize = 4096

// NewFLACWriter starts a FLAC file with PCM16 or PCM24 samples and up to
// eight channels. When w is also an io.Seeker, Close writes the length and
// MD5 checksum into the stream header.
func NewFLACWriter(w io.Writer, sampleRate float64, channels int, e Encoding) (*FLACWriter, error) {
	if channels < 1 || channels > 8 {
		return nil, fmt.Errorf("audiofile: FLAC holds 1 to 8 channels, not %d", channels)
	}
	if e != PCM16 && e != PCM24 {
		return nil, errors.New("audiofile: FLAC files are written with PCM16 or PCM24")
	}
	rate := uint32(sampleRate + 0.5)
	if sampleRate < 1 || rate >= 1<<20 {
		return nil, fmt.Errorf("audiofile: sample rate %g", sampleRate)
	}
	out := &bufferedWriteSeeker{w: w, buf: bufio.NewWriterSize(w, 64<<10)}
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlockSize,
		BlockSizeMax:  flacBlockSize,
		SampleRate:    rate,
		NChannels:     uint8(channels),
		BitsPerSample: uint8(e.bits()),
	}
	enc, err := flac.NewEncoder(out, info)
	if err != nil {
		return nil, err
	}
	fw := &FLACWriter{enc: enc, out: out, channels: channels, bits: e.bits(), block: make([][]int32, channels)}
	for ch := range fw.block {
		fw.block[ch] = make([]int32, flacBlockSize)
	}
	return fw, nil
}

// Write appends frames, one slice per channel, up to the shortest slice.
func (w *FLACWriter) Write(src [][]float32) error {
	if w.closed {
		return errors.New("audiofile: write after close")
	}
	n, err := checkDst(src, w.channels)
	if err != nil {
		return err
	}
	scale := float64(int64(1) << (w.bits - 1))
	for i := 0; i < n; {
		count := min(n-i, flacBlockSize-w.fill)
		for ch := range w.block {
			for k, x := range src[ch][i : i+count] {
				v := float64(x) * scale
				if v != v {
					v = 0
				}
				v = max(-scale, min(scale-1, v))
				if v < 0 {
					v -= 0.5
				} else {
					v += 0.5
				}
				w.block[ch][w.fill+k] = int32(v)
			}
		}
		w.fill += count
		w.frames += int64(count)
		i += count
		if w.fill == flacBlockSize {
			if err := w.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *FLACWriter) flush() error {
	if w.fill == 0 {
		return nil
	}
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(w.fill),
			SampleRate:        w.enc.Info.SampleRate,
			Channels:          frame.Channels(w.channels - 1), // ChannelsMono .. ChannelsLRCLfeLsRsSlSr
			BitsPerSample:     uint8(w.bits),
		},
		Subframes: make([]*frame.Subframe, w.channels),
	}
	for ch := range f.Subframes {
		f.Subframes[ch] = &frame.Subframe{
			SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, // the encoder picks the predictor
			Samples:   w.block[ch][:w.fill],
			NSamples:  w.fill,
		}
	}
	w.fill = 0
	return w.enc.WriteFrame(f)
}

// Close writes the last frame and, on a seekable writer, the final stream
// header. It does not close w.
func (w *FLACWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.flush(); err != nil {
		return err
	}
	if err := w.out.Flush(); err != nil {
		return err
	}
	if _, ok := w.out.w.(io.Seeker); !ok {
		return nil
	}
	end, err := w.out.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if w.frames > 0 {
		if err := w.enc.Close(); err != nil {
			return err
		}
		// Like libFLAC, give the fixed block size as both the minimum and
		// maximum: the encoder records the shorter last block as the
		// minimum, which the format excludes.
		var sizes [4]byte
		binary.BigEndian.PutUint16(sizes[0:], flacBlockSize)
		binary.BigEndian.PutUint16(sizes[2:], flacBlockSize)
		if _, err := w.out.Seek(8, io.SeekStart); err != nil {
			return err
		}
		if _, err := w.out.Write(sizes[:]); err != nil {
			return err
		}
	}
	if err := w.out.Flush(); err != nil {
		return err
	}
	_, err = w.out.Seek(end, io.SeekStart)
	return err
}

// bufferedWriteSeeker buffers writes to w and flushes before each seek.
type bufferedWriteSeeker struct {
	w   io.Writer
	buf *bufio.Writer
}

func (b *bufferedWriteSeeker) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferedWriteSeeker) Flush() error                { return b.buf.Flush() }

func (b *bufferedWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	s, ok := b.w.(io.Seeker)
	if !ok {
		return 0, errors.New("audiofile: the writer cannot seek")
	}
	if err := b.buf.Flush(); err != nil {
		return 0, err
	}
	return s.Seek(offset, whence)
}
