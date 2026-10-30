package sampler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Disk streaming. A Streamer decodes each sample file once into a cache
// file of raw float32 audio (every half-rate copy, channel after channel),
// keeps the start of every sample and its loops in memory, and reads the
// rest ahead of the voices on its own goroutine. A voice playing a streamed
// zone holds one of the Streamer's streams: two windows of streamWindow
// frames that the goroutine fills while the voice plays from the other, so
// the audio thread never touches the disk, takes no lock and never waits.
// A window that is not filled in time plays as silence and counts as an
// underrun.

const (
	// streamWindow is the frames of one window, in the frames of the
	// half-rate copy the voice reads.
	streamWindow = 1 << 14
	// streamMargin is the frames a window or a part kept in memory holds
	// beyond its ends, for the interpolation kernel's taps: enough for
	// reads up to two octaves above the copy.
	streamMargin = 128
	// streamPoll is how often the goroutine looks for windows to fill.
	streamPoll = time.Millisecond

	// DefaultPreload is the frames from the start of every streamed sample
	// kept in memory, which play while the first windows load.
	DefaultPreload = 1 << 15
	// DefaultStreams is the most streamed zones playing at once.
	DefaultStreams = 64

	cacheMagic = "VST3GO-SAMPLE-CACHE-1\n"
)

// StreamerOptions configure a Streamer; zero fields take the defaults.
type StreamerOptions struct {
	// Preload is the frames of every sample kept in memory from its start
	// (DefaultPreload). Notes start from it, so it must cover the time the
	// disk takes to deliver the first window.
	Preload int
	// Streams is the most zones playing from disk at once
	// (DefaultStreams); each takes about 270 KB. A zone that finds no free
	// stream plays its preloaded start, then silence.
	Streams int
}

// Streamer plays samples from disk. Create one per instrument (or plugin
// instance) with NewStreamer, load samples with LoadSample or LoadSFZ, and
// Close it once no voice plays its samples.
type Streamer struct {
	dir     string
	preload int
	streams []stream

	underruns atomic.Uint64
	errMu     sync.Mutex
	err       error

	filesMu sync.Mutex
	disks   map[string]*diskSample // by cache file

	stop chan struct{}
	done chan struct{}
}

// NewStreamer returns a Streamer that keeps its cache files in dir
// (created if missing) and starts its reading goroutine.
func NewStreamer(dir string, opts StreamerOptions) (*Streamer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	st := &Streamer{dir: dir, preload: opts.Preload, disks: map[string]*diskSample{}, stop: make(chan struct{}), done: make(chan struct{})}
	if st.preload <= 0 {
		st.preload = DefaultPreload
	}
	n := opts.Streams
	if n <= 0 {
		n = DefaultStreams
	}
	st.streams = make([]stream, n)
	for i := range st.streams {
		for w := range st.streams[i].windows {
			for ch := range st.streams[i].windows[w].data {
				st.streams[i].windows[w].data[ch] = make([]float32, streamWindow+2*streamMargin)
			}
		}
	}
	go st.run()
	return st, nil
}

// Close stops the reading goroutine and closes the cache files. Voices
// still playing streamed zones go silent after their preloaded parts.
func (st *Streamer) Close() error {
	select {
	case <-st.stop:
		return nil
	default:
	}
	close(st.stop)
	<-st.done
	st.filesMu.Lock()
	defer st.filesMu.Unlock()
	var err error
	for _, d := range st.disks {
		err = errors.Join(err, d.file.Close())
	}
	clear(st.disks)
	return err
}

// Underruns returns how many times a voice reached a window that was not
// loaded yet (the disk is too slow, or Preload too short).
func (st *Streamer) Underruns() uint64 { return st.underruns.Load() }

// Err returns the first error reading a cache file while streaming.
func (st *Streamer) Err() error {
	st.errMu.Lock()
	defer st.errMu.Unlock()
	return st.err
}

func (st *Streamer) fail(err error) {
	st.errMu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.errMu.Unlock()
}

// LoadSample returns a streamed Sample of an audio file (see the package
// function LoadSample for the formats, root note and loop). The first load
// of a file decodes it into the cache directory; later loads by Streamers
// on the same directory only read the preloaded parts, and loads by this
// Streamer share them. The Streamer keeps the cache file open until Close.
// It reads files; call it off the audio thread.
func (st *Streamer) LoadSample(path string) (*Sample, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d", abs, info.Size(), info.ModTime().UnixNano()))
	cache := filepath.Join(st.dir, hex.EncodeToString(key[:16])+".smp")
	if s, err := st.open(cache); err == nil {
		return s, nil
	}
	s, err := LoadSample(path)
	if err != nil {
		return nil, err
	}
	if err := writeCache(cache, s); err != nil {
		return nil, fmt.Errorf("%s: sample cache: %w", filepath.Base(path), err)
	}
	return st.open(cache)
}

// LoadSFZ reads an SFZ instrument (see the package function LoadSFZ) with
// streamed samples.
func (st *Streamer) LoadSFZ(path string) (*SFZ, error) { return loadSFZ(path, st.LoadSample) }

// The cache file: cacheMagic, a header of little-endian fields (channels,
// levels, sample rate, root note, loop start and end, then the frames of
// each level), then for each level each channel's frames as little-endian
// float32.

type cacheHeader struct {
	Channels, Levels   uint32
	SampleRate, Root   float64
	LoopStart, LoopEnd int64
}

func writeCache(path string, s *Sample) error {
	var head bytes.Buffer
	head.WriteString(cacheMagic)
	h := cacheHeader{uint32(s.channels), uint32(len(s.levels)), s.sampleRate, s.rootNote, int64(s.loopStart), int64(s.loopEnd)}
	if err := binary.Write(&head, binary.LittleEndian, h); err != nil {
		return err
	}
	for _, level := range s.levels {
		if err := binary.Write(&head, binary.LittleEndian, int64(len(level[0]))); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sample-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(head.Bytes())
	for _, level := range s.levels {
		for _, data := range level {
			if err == nil {
				err = binary.Write(tmp, binary.LittleEndian, data)
			}
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// open returns a Sample of a cache file, reading its header and preloaded
// start on the first open.
func (st *Streamer) open(path string) (*Sample, error) {
	st.filesMu.Lock()
	defer st.filesMu.Unlock()
	d := st.disks[path]
	if d == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if d, err = st.openFile(f); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		st.disks[path] = d
	}
	s := &Sample{sampleRate: d.sampleRate, rootNote: d.root, frames: d.frames[0], channels: d.channels, disk: d}
	if err := s.SetLoop(d.loopStart, d.loopEnd); err != nil {
		return nil, err
	}
	return s, nil
}

func (st *Streamer) openFile(f *os.File) (*diskSample, error) {
	magic := make([]byte, len(cacheMagic))
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != cacheMagic {
		return nil, errors.New("not a sample cache file")
	}
	var h cacheHeader
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if h.Channels < 1 || h.Channels > 2 || h.Levels < 1 || h.Levels > maxLevels || !(h.SampleRate > 0) {
		return nil, errors.New("corrupt sample cache header")
	}
	d := &diskSample{st: st, file: f, channels: int(h.Channels), sampleRate: h.SampleRate, root: h.Root,
		loopStart: int(h.LoopStart), loopEnd: int(h.LoopEnd)}
	d.frames = make([]int, h.Levels)
	d.offsets = make([]int64, h.Levels)
	offset := int64(len(cacheMagic) + binary.Size(h) + 8*int(h.Levels))
	for l := range d.frames {
		var n int64
		if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
			return nil, err
		}
		if n < 1 || n > math.MaxInt32 {
			return nil, errors.New("corrupt sample cache header")
		}
		d.frames[l] = int(n)
		d.offsets[l] = offset
		offset += 4 * n * int64(h.Channels)
	}
	if info, err := f.Stat(); err != nil || info.Size() != offset {
		return nil, errors.New("truncated sample cache file")
	}
	d.parts.Store(&[]part{})
	if err := d.keep(0, min(st.preload, d.frames[0])); err != nil {
		return nil, err
	}
	return d, nil
}

// diskSample is the cache file of streamed Samples and the parts of it
// kept in memory. Parts are only added, by publishing a new list, so
// voices read them without a lock.
type diskSample struct {
	st                 *Streamer
	file               *os.File
	channels           int
	sampleRate, root   float64
	loopStart, loopEnd int     // the file's loop
	frames             []int   // per level
	offsets            []int64 // of each level's first channel
	mu                 sync.Mutex
	parts              atomic.Pointer[[]part]
}

// part is a range of the sample kept in memory: start[l] is its first
// frame in level l, data[l][ch] the frames.
type part struct {
	start []int
	data  [][][]float32
}

// keep reads frames start to end (of the full-rate sample) of every level
// into memory, with the kernel's margin around them, unless a part holds
// them already.
func (d *diskSample) keep(start, end int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	parts := *d.parts.Load()
	lo, hi := max(0, start-streamMargin), min(d.frames[0], end+streamMargin)
	for _, p := range parts {
		if p.start[0] <= lo && hi <= p.start[0]+len(p.data[0][0]) {
			return nil
		}
	}
	p := part{start: make([]int, len(d.frames)), data: make([][][]float32, len(d.frames))}
	for l, n := range d.frames {
		lo := max(0, start>>l-streamMargin)
		hi := min(n, (end+(1<<l)-1)>>l+streamMargin)
		p.start[l] = lo
		p.data[l] = make([][]float32, d.channels)
		for ch := range p.data[l] {
			p.data[l][ch] = make([]float32, hi-lo)
			if err := d.read(l, ch, lo, p.data[l][ch]); err != nil {
				return err
			}
		}
	}
	parts = append(slices.Clip(parts), p)
	d.parts.Store(&parts)
	return nil
}

// read fills dst with frames from start of a level's channel.
func (d *diskSample) read(level, ch, start int, dst []float32) error {
	if len(dst) == 0 {
		return nil
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&dst[0])), 4*len(dst))
	off := d.offsets[level] + 4*(int64(ch)*int64(d.frames[level])+int64(start))
	if _, err := d.file.ReadAt(buf, off); err != nil {
		return err
	}
	if !littleEndian {
		for i, v := range dst {
			dst[i] = math.Float32frombits(bits.ReverseBytes32(math.Float32bits(v)))
		}
	}
	return nil
}

var littleEndian = func() bool {
	x := uint16(1)
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

func (d *diskSample) memoryBytes() int {
	n := 0
	for _, p := range *d.parts.Load() {
		for _, level := range p.data {
			for _, data := range level {
				n += 4 * len(data)
			}
		}
	}
	return n
}

// span returns the memory holding frames first to last of a level and
// the frame data[0] is: a kept part or a loaded window of s.
func (d *diskSample) span(s *stream, level, first, last int) (data [][]float32, base int, ok bool) {
	parts := *d.parts.Load()
	for i := range parts {
		p := &parts[i]
		lo := p.start[level]
		if first >= lo && last < lo+len(p.data[level][0]) {
			return p.data[level], lo, true
		}
	}
	if s == nil {
		return nil, 0, false
	}
	k := first / streamWindow
	for _, k := range [2]int{k, k + 1} {
		w := &s.windows[k&1]
		lo := k*streamWindow - streamMargin
		if first >= lo && last < lo+streamWindow+2*streamMargin && w.holds(level, k) {
			return w.data[:d.channels], lo, true
		}
	}
	return nil, 0, false
}

// kept reports whether frame j of a level is in a kept part, and the end
// of that part's frames then.
func (d *diskSample) kept(level, j int) (end int, ok bool) {
	parts := *d.parts.Load()
	for i := range parts {
		p := &parts[i]
		lo := p.start[level]
		if hi := lo + len(p.data[level][0]); j >= lo && j < hi {
			return hi, true
		}
	}
	return 0, false
}

// stream is a voice's pair of windows on one streamed sample.
type stream struct {
	inUse   atomic.Bool
	source  atomic.Pointer[diskSample]
	seq     uint32 // the last request's number, audio thread
	windows [2]window
}

// window holds frames k*streamWindow-streamMargin to
// (k+1)*streamWindow+streamMargin of a level. The audio thread asks for
// it by storing a request (number, level, k) in want and gives up what
// the window held; the goroutine fills data, then stores the request in
// have. Request numbers are never reused, so have equals want only when
// data holds that request and the goroutine is done with it.
type window struct {
	want, have atomic.Uint64
	data       [2][]float32
}

// windowBits is the low bits of a request, naming the level and k.
const windowBits = 1<<32 - 1

// windowOf encodes level and k as the low bits of a request.
func windowOf(level, k int) uint64 {
	return uint64(level)<<28 | uint64(k)
}

// request numbers a new request for frames k of a level. Audio thread.
func (s *stream) request(level, k int) uint64 {
	s.seq++
	if s.seq == 0 {
		s.seq = 1
	}
	return uint64(s.seq)<<32 | windowOf(level, k)
}

// holds reports whether the window holds frames k of a level for the
// stream's current owner. Audio thread.
func (w *window) holds(level, k int) bool {
	want := w.want.Load()
	return want != 0 && want&windowBits == windowOf(level, k) && w.have.Load() == want
}

// acquire takes a free stream for d; nil when every stream plays.
// Audio thread; it does not allocate or block.
func (st *Streamer) acquire(d *diskSample) *stream {
	for i := range st.streams {
		s := &st.streams[i]
		if s.inUse.CompareAndSwap(false, true) {
			s.source.Store(d)
			return s
		}
	}
	return nil
}

// release returns the stream to its Streamer.
func (s *stream) release() {
	for w := range s.windows {
		s.windows[w].want.Store(0)
	}
	s.inUse.Store(false)
}

// schedule asks for the windows a voice reading level frames from pos on
// needs next: the one holding the first frame at or after pos that no
// kept part holds, and the one after it.
func (s *stream) schedule(level int, pos float64) {
	d := s.source.Load()
	j := int(pos)
	for range len(*d.parts.Load()) + 1 {
		end, ok := d.kept(level, j)
		if !ok {
			break
		}
		// Parts hold a margin of frames beyond their range.
		j = max(j, end-streamMargin)
	}
	if j >= d.frames[level] {
		return
	}
	k := j / streamWindow
	for _, k := range [2]int{k, k + 1} {
		if k*streamWindow >= d.frames[level] {
			return
		}
		w := &s.windows[k&1]
		if want := w.want.Load(); want == 0 || want&windowBits != windowOf(level, k) {
			w.want.Store(s.request(level, k))
		}
	}
}

// run fills requested windows until Close.
func (st *Streamer) run() {
	defer close(st.done)
	for {
		select {
		case <-st.stop:
			return
		default:
		}
		filled := false
		for i := range st.streams {
			s := &st.streams[i]
			for w := range s.windows {
				win := &s.windows[w]
				want := win.want.Load()
				if want == 0 || want == win.have.Load() {
					continue
				}
				// The source belongs to the request only while the
				// request stands: a new owner stores its source before
				// its requests.
				d := s.source.Load()
				if win.want.Load() != want {
					continue
				}
				st.fill(d, win, want)
				win.have.Store(want)
				filled = true
			}
		}
		if !filled {
			time.Sleep(streamPoll)
		}
	}
}

// fill reads the window of a request.
func (st *Streamer) fill(d *diskSample, w *window, req uint64) {
	level, k := int(req>>28&15), int(req&(1<<28-1))
	n := d.frames[level]
	lo := k*streamWindow - streamMargin
	from, to := max(0, lo), min(n, lo+streamWindow+2*streamMargin)
	for ch := range d.channels {
		data := w.data[ch]
		clear(data[:max(0, from-lo)])
		if to > from {
			if err := d.read(level, ch, from, data[from-lo:to-lo]); err != nil {
				st.fail(err)
				clear(data[from-lo : to-lo])
			}
		}
		clear(data[max(0, to-lo):])
	}
}
