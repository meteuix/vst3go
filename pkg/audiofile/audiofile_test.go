package audiofile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The fixtures in testdata hold this signal (see testdata/generate.sh).
const fixtureFrames = 2048

func fixture(n, ch int) float64 {
	v := 0.5 * math.Sin(2*math.Pi*440*float64(n)/44100)
	if ch == 1 {
		return -v
	}
	return v
}

func TestDecodeFixtures(t *testing.T) {
	for _, tc := range []struct {
		file   string
		format Format
		bits   int
		float  bool
		tol    float64
	}{
		{"ref16.wav", WAV, 16, false, 1.0 / (1 << 15)},
		{"ref24.wav", WAV, 24, false, 1.0 / (1 << 23)},
		{"u8.wav", WAV, 8, false, 1.0/(1<<7) + 1.0/(1<<15)},
		{"float32.wav", WAV, 32, true, 1.0 / (1 << 23)},
		{"float64.wav", WAV, 64, true, 1.0 / (1 << 23)},
		{"rf64.wav", WAV, 24, false, 1.0 / (1 << 23)},
		{"aiff24.aif", AIFF, 24, false, 1.0 / (1 << 23)},
		{"float32.aifc", AIFF, 32, true, 1.0 / (1 << 23)},
		{"flac16.flac", FLAC, 16, false, 1.0 / (1 << 15)},
		{"flac24.flac", FLAC, 24, false, 1.0 / (1 << 23)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			info := r.Info()
			if info.Format != tc.format || info.SampleRate != 44100 || info.Channels != 2 || info.Frames != fixtureFrames ||
				info.BitDepth != tc.bits || info.Float != tc.float || info.RootNote != -1 || len(info.Loops) != 0 {
				t.Fatalf("info = %+v", info)
			}
			b, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if b.Frames() != fixtureFrames || b.SampleRate != 44100 {
				t.Fatalf("loaded %d frames at %g Hz", b.Frames(), b.SampleRate)
			}
			for ch := range b.Channels {
				for n, x := range b.Channels[ch] {
					if d := math.Abs(float64(x) - fixture(n, ch)); d > tc.tol*1.01 {
						t.Fatalf("channel %d frame %d = %g, want %g", ch, n, x, fixture(n, ch))
					}
				}
			}
			checkSeek(t, r, b, 0)
		})
	}
}

// checkSeek reads from a few positions and compares with the whole decode.
func checkSeek(t *testing.T, r Reader, whole *Buffer, tol float64) {
	t.Helper()
	dst := [][]float32{make([]float32, 100), make([]float32, 100)}
	for _, at := range []int64{1000, 0, 1999, 4} {
		if err := r.SeekFrame(at); err != nil {
			t.Fatalf("seek %d: %v", at, err)
		}
		got := 0
		for got < 100 && int(at)+got < whole.Frames() {
			n, err := r.Read([][]float32{dst[0][got:], dst[1][got:]})
			if err != nil {
				t.Fatalf("read at %d: %v", at, err)
			}
			got += n
		}
		for ch := range dst {
			for i := 0; i < got; i++ {
				if d := math.Abs(float64(dst[ch][i] - whole.Channels[ch][int(at)+i])); d > tol {
					t.Fatalf("after seek %d: channel %d frame %d = %g, want %g", at, ch, i, dst[ch][i], whole.Channels[ch][int(at)+i])
				}
			}
		}
	}
	if err := r.SeekFrame(r.Info().Frames); err != nil {
		t.Fatalf("seek to the end: %v", err)
	}
	if n, err := r.Read(dst); n != 0 || err != io.EOF {
		t.Fatalf("read at the end = %d, %v", n, err)
	}
	if err := r.SeekFrame(r.Info().Frames + 1); err == nil {
		t.Fatal("seek past the end succeeded")
	}
	if _, err := r.Read(dst[:1]); err == nil {
		t.Fatal("read into one channel of two succeeded")
	}
}

func TestDecodeVorbis(t *testing.T) {
	r, err := Open("testdata/vorbis.ogg")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info := r.Info()
	if info.Format != OggVorbis || info.SampleRate != 44100 || info.Channels != 2 || info.Frames != fixtureFrames || info.BitDepth != 0 {
		t.Fatalf("info = %+v", info)
	}
	b, err := Load("testdata/vorbis.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if b.Frames() != fixtureFrames {
		t.Fatalf("decoded %d frames", b.Frames())
	}
	// Vorbis is gapless: the decode lines up with the source.
	for ch := range b.Channels {
		var errSum float64
		for n, x := range b.Channels[ch] {
			d := float64(x) - fixture(n, ch)
			errSum += d * d
		}
		if rms := math.Sqrt(errSum / fixtureFrames); rms > 0.01 {
			t.Fatalf("channel %d: rms error %g", ch, rms)
		}
	}
	checkSeek(t, r, b, 1e-4)
}

func TestDecodeMP3(t *testing.T) {
	data, err := os.ReadFile("testdata/id3.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("ID3")) {
		t.Fatal("the fixture has no ID3v2 tag")
	}
	b, err := DecodeBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	info := r.Info()
	if info.Format != MP3 || info.SampleRate != 44100 || info.Channels != 2 || info.Frames != int64(b.Frames()) {
		t.Fatalf("info = %+v, %d frames decoded", info, b.Frames())
	}
	// MP3 adds encoder delay; find it and compare the aligned signal.
	best, bestLag := 0.0, 0
	for lag := 0; lag+fixtureFrames <= b.Frames(); lag++ {
		var dot float64
		for n := 0; n < fixtureFrames; n++ {
			dot += float64(b.Channels[0][lag+n]) * fixture(n, 0)
		}
		if dot > best {
			best, bestLag = dot, lag
		}
	}
	var num, den, side float64
	for n := 200; n < fixtureFrames-200; n++ {
		l, r := float64(b.Channels[0][bestLag+n]), float64(b.Channels[1][bestLag+n])
		num += l * fixture(n, 0)
		den += fixture(n, 0) * fixture(n, 0)
		side += (l + r) * (l + r)
	}
	// The decode keeps the LAME info frame (1152 frames of silence) and the
	// encoder delay (576 + 529 frames). lame --decode, which trims both,
	// gives the same 0.95 gain on this fixture.
	if bestLag != 1152+1105 {
		t.Fatalf("lag %d", bestLag)
	}
	if gain := num / den; gain < 0.94 || gain > 1.02 {
		t.Fatalf("gain %g at lag %d", gain, bestLag)
	}
	if side/den > 0.01 {
		t.Fatalf("right is not the inverted left: %g", side/den)
	}
	checkSeek(t, r, b, 1e-3)
}

func TestDecodeErrors(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("hello, world"),
		[]byte("RIFF\x04\x00\x00\x00WAVE"),
		[]byte("FORM\x04\x00\x00\x00AIFF"),
		[]byte("fLaC\x00\x00"),
	} {
		if _, err := DecodeBytes(data); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: err = %v", data, err)
		}
	}
	if _, err := Open("testdata/missing.wav"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	// A truncated data chunk decodes what is there.
	data, err := os.ReadFile("testdata/ref16.wav")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeBytes(data[:len(data)-401])
	if err != nil {
		t.Fatal(err)
	}
	if b.Frames() != fixtureFrames-101 {
		t.Fatalf("truncated file: %d frames", b.Frames())
	}
}

// signal is a test signal with values past full scale and a NaN.
func signal(channels, frames int) [][]float32 {
	s := make([][]float32, channels)
	for ch := range s {
		s[ch] = make([]float32, frames)
		for n := range s[ch] {
			s[ch][n] = float32(0.9 * math.Sin(float64(n)*0.01*float64(ch+1)+float64(ch)))
		}
	}
	s[0][10], s[0][11], s[0][12] = 1.5, -1.5, float32(math.NaN())
	return s
}

func want(x float32, e Encoding) float64 {
	if x != x {
		return 0
	}
	if e == Float32 {
		return float64(x)
	}
	full := float64(int64(1) << (e.bits() - 1))
	return max(-1, min((full-1)/full, float64(x)))
}

func TestWriteRoundTrip(t *testing.T) {
	const frames = 10000
	for _, format := range []Format{WAV, AIFF, FLAC} {
		for _, e := range []Encoding{PCM16, PCM24, PCM32, Float32} {
			if format == FLAC && (e == PCM32 || e == Float32) {
				continue
			}
			for _, channels := range []int{1, 2, 3} {
				src := signal(channels, frames)
				var out seekBuffer
				w, err := NewWriter(&out, format, 48000, channels, e)
				if err != nil {
					t.Fatal(err)
				}
				// Uneven writes, across FLAC blocks.
				for at := 0; at < frames; {
					n := min(frames-at, 1+at%5000)
					part := make([][]float32, channels)
					for ch := range part {
						part[ch] = src[ch][at : at+n]
					}
					if err := w.Write(part); err != nil {
						t.Fatal(err)
					}
					at += n
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				name := format.String() + "/" + string(rune('0'+e)) + "/" + string(rune('0'+channels))
				b, err := DecodeBytes(out.data)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				r, _ := NewReader(bytes.NewReader(out.data))
				info := r.Info()
				if info.Format != format || info.SampleRate != 48000 || info.Channels != channels || info.Frames != frames ||
					info.BitDepth != e.bits() || info.Float != (e == Float32) {
					t.Fatalf("%s: info = %+v", name, info)
				}
				compare(t, name, b, src, e)
			}
		}
	}
}

func compare(t *testing.T, name string, b *Buffer, src [][]float32, e Encoding) {
	t.Helper()
	tol := 0.0
	if e != Float32 {
		tol = 0.5/float64(int64(1)<<(e.bits()-1)) + 1e-9
	}
	if len(b.Channels) != len(src) || b.Frames() != len(src[0]) {
		t.Fatalf("%s: %d channels of %d frames", name, len(b.Channels), b.Frames())
	}
	for ch := range src {
		for n, x := range src[ch] {
			if d := math.Abs(float64(b.Channels[ch][n]) - want(x, e)); d > tol {
				t.Fatalf("%s: channel %d frame %d = %g, want %g", name, ch, n, b.Channels[ch][n], want(x, e))
			}
		}
	}
}

func TestSaveAndExternalDecoders(t *testing.T) {
	dir := t.TempDir()
	src := &Buffer{SampleRate: 44100, Channels: signal(3, 30000)}
	for _, tc := range []struct {
		name string
		e    Encoding
	}{
		{"out16.wav", PCM16}, {"out24.wav", PCM24}, {"outf.wav", Float32},
		{"out16.aiff", PCM16}, {"out24.aif", PCM24}, {"outf.aifc", Float32},
		{"out16.flac", PCM16}, {"out24.flac", PCM24},
	} {
		path := filepath.Join(dir, tc.name)
		if err := Save(path, src, tc.e); err != nil {
			t.Fatal(err)
		}
		b, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, tc.name, b, src.Channels, tc.e)
		// Encode writes the same bytes to a plain writer.
		format, _ := FormatOf(path)
		var mem bytes.Buffer
		if err := Encode(&mem, format, src, tc.e); err != nil {
			t.Fatal(err)
		}
		saved, _ := os.ReadFile(path)
		if !bytes.Equal(mem.Bytes(), saved) {
			t.Fatalf("%s: Encode differs from Save", tc.name)
		}
		if tool, err := exec.LookPath("afconvert"); err == nil {
			ref := filepath.Join(dir, tc.name+".ref.wav")
			if out, err := exec.Command(tool, "-f", "WAVE", "-d", "LEF32", path, ref).CombinedOutput(); err != nil {
				t.Fatalf("afconvert %s: %v\n%s", tc.name, err, out)
			}
			b, err := Load(ref)
			if err != nil {
				t.Fatal(err)
			}
			compare(t, tc.name+" (afconvert)", b, src.Channels, tc.e)
		}
		if tool, err := exec.LookPath("flac"); err == nil && format == FLAC {
			if out, err := exec.Command(tool, "-t", "-s", path).CombinedOutput(); err != nil {
				t.Fatalf("flac -t %s: %v\n%s", tc.name, err, out)
			}
		}
		if tool, err := exec.LookPath("sox"); err == nil {
			if out, err := exec.Command(tool, path, "-n", "stat").CombinedOutput(); err != nil {
				t.Fatalf("sox %s: %v\n%s", tc.name, err, out)
			}
		}
	}
	if _, err := Create(filepath.Join(dir, "x.mp3"), 44100, 2, PCM16); err == nil {
		t.Fatal("created an mp3 file")
	}
	if _, err := Create(filepath.Join(dir, "x.flac"), 44100, 2, Float32); err == nil {
		t.Fatal("created a float flac file")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.flac")); !os.IsNotExist(err) {
		t.Fatal("a failed Create left its file")
	}
}

func TestFLACStream(t *testing.T) {
	// Without seeking the stream header keeps an unknown length.
	var out bytes.Buffer
	w, err := NewFLACWriter(&out, 44100, 2, PCM16)
	if err != nil {
		t.Fatal(err)
	}
	src := signal(2, 5000)
	if err := w.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := DecodeBytes(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	compare(t, "stream", b, src, PCM16)
}

func TestEmptyFile(t *testing.T) {
	for _, format := range []Format{WAV, AIFF, FLAC} {
		var out seekBuffer
		w, err := NewWriter(&out, format, 44100, 2, PCM16)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := w.Write([][]float32{{0}, {0}}); err == nil {
			t.Fatalf("%v: write after close succeeded", format)
		}
		r, err := NewReader(bytes.NewReader(out.data))
		if err != nil {
			t.Fatalf("%v: %v", format, err)
		}
		if r.Info().Frames != 0 {
			t.Fatalf("%v: %d frames", format, r.Info().Frames)
		}
		if n, err := r.Read([][]float32{make([]float32, 8), make([]float32, 8)}); n != 0 || err != io.EOF {
			t.Fatalf("%v: read = %d, %v", format, n, err)
		}
	}
}

// chunk appends a RIFF or IFF chunk.
func chunk(order binary.AppendByteOrder, id string, body []byte) []byte {
	b := append([]byte(id), order.AppendUint32(nil, uint32(len(body)))...)
	b = append(b, body...)
	if len(body)&1 != 0 {
		b = append(b, 0)
	}
	return b
}

func TestWAVSampleLoops(t *testing.T) {
	var out seekBuffer
	w, _ := NewWAVWriter(&out, 44100, 1, PCM16)
	_ = w.Write([][]float32{make([]float32, 1000)})
	_ = w.Close()
	le := binary.LittleEndian
	smpl := make([]byte, 36+2*24)
	le.PutUint32(smpl[12:], 57) // root note
	le.PutUint32(smpl[28:], 2)  // loops
	for i, l := range []Loop{{100, 200}, {300, 5000}} {
		le.PutUint32(smpl[36+24*i+8:], uint32(l.Start))
		le.PutUint32(smpl[36+24*i+12:], uint32(l.End))
	}
	data := append(out.data, chunk(le, "smpl", smpl)...)
	le.PutUint32(data[4:], uint32(len(data)-8))
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	info := r.Info()
	// smpl loop ends are inclusive; Loop.End is past the last frame, and
	// loops stay inside the file.
	if info.RootNote != 57 || len(info.Loops) != 2 || info.Loops[0] != (Loop{100, 201}) || info.Loops[1] != (Loop{300, 1000}) {
		t.Fatalf("info = %+v", info)
	}
}

func TestAIFFInstrument(t *testing.T) {
	var out seekBuffer
	w, _ := NewAIFFWriter(&out, 22050, 1, PCM24)
	_ = w.Write([][]float32{make([]float32, 1000)})
	_ = w.Close()
	be := binary.BigEndian
	mark := be.AppendUint16(nil, 3)
	for _, m := range []struct {
		id   uint16
		pos  uint32
		name string
	}{{1, 10, "a"}, {2, 500, "loop end"}, {7, 800, ""}} {
		mark = be.AppendUint16(mark, m.id)
		mark = be.AppendUint32(mark, m.pos)
		mark = append(mark, byte(len(m.name)))
		mark = append(mark, m.name...)
		if (1+len(m.name))&1 != 0 {
			mark = append(mark, 0)
		}
	}
	inst := []byte{60, 0, 0, 127, 1, 127, 0, 0}
	inst = be.AppendUint16(inst, 1) // sustain loop forward from marker 1 to 2
	inst = be.AppendUint16(inst, 1)
	inst = be.AppendUint16(inst, 2)
	inst = be.AppendUint16(inst, 0) // no release loop
	inst = be.AppendUint16(inst, 7)
	inst = be.AppendUint16(inst, 1)
	data := append(out.data, chunk(be, "MARK", mark)...)
	data = append(data, chunk(be, "INST", inst)...)
	be.PutUint32(data[4:], uint32(len(data)-8))
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	info := r.Info()
	if info.SampleRate != 22050 || info.Frames != 1000 || info.RootNote != 60 || len(info.Loops) != 1 || info.Loops[0] != (Loop{10, 500}) {
		t.Fatalf("info = %+v", info)
	}
}

func TestAIFCLittleEndian(t *testing.T) {
	be := binary.BigEndian
	comm := be.AppendUint16(nil, 2)
	comm = be.AppendUint32(comm, 3)
	comm = be.AppendUint16(comm, 16)
	rate := floatToExtended(96000)
	comm = append(comm, rate[:]...)
	comm = append(comm, "sowt"...)
	comm = append(comm, 0, 0)
	ssnd := make([]byte, 8)
	for _, v := range []int16{16384, -16384, -32768, 32767, 0, 1} {
		ssnd = binary.LittleEndian.AppendUint16(ssnd, uint16(v))
	}
	body := append([]byte("AIFC"), chunk(be, "COMM", comm)...)
	body = append(body, chunk(be, "SSND", ssnd)...)
	b, err := DecodeBytes(chunk(be, "FORM", body))
	if err != nil {
		t.Fatal(err)
	}
	if b.SampleRate != 96000 || b.Frames() != 3 ||
		b.Channels[0][0] != 0.5 || b.Channels[1][0] != -0.5 || b.Channels[0][1] != -1 ||
		b.Channels[1][1] != 32767.0/32768 || b.Channels[0][2] != 0 || b.Channels[1][2] != 1.0/32768 {
		t.Fatalf("decoded %+v", b)
	}
}

func TestExtendedFloat(t *testing.T) {
	for _, v := range []float64{8000, 11025, 44100, 48000, 88200, 96000, 192000, 22050.5} {
		b := floatToExtended(v)
		if got := extendedToFloat(b[:]); got != v {
			t.Fatalf("%g round trips to %g", v, got)
		}
	}
	// 44100 Hz as written by other tools.
	if got := extendedToFloat([]byte{0x40, 0x0E, 0xAC, 0x44, 0, 0, 0, 0, 0, 0}); got != 44100 {
		t.Fatalf("44100 decodes as %g", got)
	}
}
