// Package process provides audio processing context and utilities for VST3 audio processing.
package process

import (
	"cmp"
	"slices"

	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/midi"
)

// ParameterChange represents a parameter change at a specific sample offset
type ParameterChange struct {
	ParamID      uint32
	Value        float64
	SampleOffset int
}

// ProcessMode mirrors the VST3 process mode reported by the host.
type ProcessMode int32

const (
	// ProcessModeRealtime is live playback with real-time constraints.
	ProcessModeRealtime ProcessMode = 0
	// ProcessModePrefetch is faster-than-realtime processing for playback.
	ProcessModePrefetch ProcessMode = 1
	// ProcessModeOffline is offline rendering (bounce/export); quality may be
	// raised because there is no real-time deadline.
	ProcessModeOffline ProcessMode = 2
)

// Context is the per-block processing state handed to Processor.ProcessAudio.
//
// Input and Output are the channels of the main (index 0) audio buses. Every
// other bus, including sidechains and auxiliary outputs, is available through
// InputBus/OutputBus. When the host processes in 64-bit, Input64/Output64
// and the *64 bus accessors carry the audio instead and Input/Output are nil;
// this only happens for processors that opt into double precision.
//
// When the host automates parameters inside a block, the runtime splits the
// block at each change and calls ProcessAudio once per segment; BlockOffset
// reports where the current segment starts inside the host block. All event
// and parameter-change offsets are relative to the host block, so a sample
// index i of Output corresponds to block offset BlockOffset()+i.
//
// Slices returned by Context accessors are only valid during the current
// ProcessAudio call.
type Context struct {
	Input       [][]float32
	Output      [][]float32
	Input64     [][]float64
	Output64    [][]float64
	SampleRate  float64
	ProcessMode ProcessMode

	// Transport and timing information
	Transport *TransportInfo

	// Pre-allocated work buffers
	workBuffer []float32
	tempBuffer []float32
	sampleIn   []float32
	sampleOut  []float32

	// Parameter access
	params *param.Registry

	numSamples   int // samples visible to the current ProcessAudio call
	blockSamples int // samples in the host block
	blockOffset  int // start of the current segment inside the host block

	in, out       busSet[float32]
	in64, out64   busSet[float64]
	double        bool // the current block is 64-bit
	inSilence     []uint64
	outSilence    []uint64
	sidechainBus  int
	busesManaged  bool // bus storage is filled by the runtime via BeginBlock
	manualBuffers bool // Input/Output were assigned by hand (tests, tools)

	// Sample-accurate automation
	paramChanges []ParameterChange
	changeCount  int

	// Events
	inEvents       []midi.Event
	inSorted       bool
	windowed       bool
	evStart, evEnd int
	outEvents      []midi.Event
	eventData      []byte

	// Output parameter values (meters, gain reduction, ...)
	outParams []ParameterChange
}

const (
	defaultParameterChangeCapacity = 128
	defaultEventCapacity           = 512
	defaultEventDataCapacity       = 16 * 1024
)

// NewContext creates a new process context with pre-allocated buffers
func NewContext(maxBlockSize int, params *param.Registry) *Context {
	paramChangeCapacity := maxBlockSize
	if paramChangeCapacity < defaultParameterChangeCapacity {
		paramChangeCapacity = defaultParameterChangeCapacity
	}

	return &Context{
		workBuffer:   make([]float32, maxBlockSize),
		tempBuffer:   make([]float32, maxBlockSize),
		params:       params,
		paramChanges: make([]ParameterChange, paramChangeCapacity),
		Transport:    &TransportInfo{},
		sidechainBus: -1,
		inEvents:     make([]midi.Event, 0, defaultEventCapacity),
		inSorted:     true,
		outEvents:    make([]midi.Event, 0, defaultEventCapacity),
		eventData:    make([]byte, 0, defaultEventDataCapacity),
		outParams:    make([]ParameterChange, 0, defaultParameterChangeCapacity),
	}
}

// busSet holds the channel buffers of all audio buses of one direction:
// full is the host block, seg the per-segment views, cur whichever is active.
type busSet[T float32 | float64] struct {
	full, seg, cur [][][]T
}

func (b *busSet[T]) configure(channels []int) {
	b.full = make([][][]T, len(channels))
	b.seg = make([][][]T, len(channels))
	for i, n := range channels {
		b.full[i] = make([][]T, 0, n)
		b.seg[i] = make([][]T, 0, n)
	}
	b.cur = b.full
}

func (b *busSet[T]) reset() {
	for i := range b.full {
		b.full[i] = b.full[i][:0]
	}
	b.cur = b.full
}

func (b *busSet[T]) add(bus int, samples []T) {
	if bus >= 0 && bus < len(b.full) && len(b.full[bus]) < cap(b.full[bus]) {
		b.full[bus] = append(b.full[bus], samples)
	}
}

func (b *busSet[T]) slice(start, end int) {
	for i := range b.full {
		b.seg[i] = b.seg[i][:0]
		for _, ch := range b.full[i] {
			b.seg[i] = append(b.seg[i], ch[start:end])
		}
	}
	b.cur = b.seg
}

func (b *busSet[T]) restore() { b.cur = b.full }

func (b *busSet[T]) main() [][]T {
	if len(b.cur) == 0 || len(b.cur[0]) == 0 {
		return nil
	}
	return b.cur[0]
}

func (b *busSet[T]) bus(index int) [][]T {
	if index < 0 || index >= len(b.cur) {
		return nil
	}
	return b.cur[index]
}

// adopt makes hand-assigned main-bus channels bus 0.
func (b *busSet[T]) adopt(channels [][]T) {
	if len(b.full) != 1 || cap(b.full[0]) < len(channels) {
		b.configure([]int{len(channels)})
	}
	b.full[0] = append(b.full[0][:0], channels...)
	b.cur = b.full
}

// Param returns the current value of a parameter (0-1 normalized)
func (c *Context) Param(id uint32) float64 {
	if c.params == nil {
		return 0
	}
	value, ok := c.params.GetNormalized(id)
	if !ok {
		return 0
	}
	return value
}

// ParamPlain returns the current plain value of a parameter
func (c *Context) ParamPlain(id uint32) float64 {
	if c.params == nil {
		return 0
	}
	value, ok := c.params.GetPlain(id)
	if !ok {
		return 0
	}
	return value
}

// NumSamples returns the number of samples to process in this call.
func (c *Context) NumSamples() int {
	if c.numSamples > 0 {
		return c.numSamples
	}
	for _, chans := range [][][]float32{c.Input, c.Output} {
		if len(chans) > 0 && len(chans[0]) > 0 {
			return len(chans[0])
		}
	}
	for _, chans := range [][][]float64{c.Input64, c.Output64} {
		if len(chans) > 0 && len(chans[0]) > 0 {
			return len(chans[0])
		}
	}
	return 0
}

// BlockOffset returns the start of the current segment inside the host block.
// It is non-zero only when the runtime split the block for automation.
func (c *Context) BlockOffset() int {
	return c.blockOffset
}

// Is64Bit reports whether the current block carries 64-bit audio
// (Input64/Output64) instead of 32-bit audio (Input/Output).
func (c *Context) Is64Bit() bool {
	return c.double
}

// NumInputChannels returns the number of main input channels
func (c *Context) NumInputChannels() int {
	if c.double {
		return len(c.Input64)
	}
	return len(c.Input)
}

// NumOutputChannels returns the number of main output channels
func (c *Context) NumOutputChannels() int {
	if c.double {
		return len(c.Output64)
	}
	return len(c.Output)
}

// WorkBuffer returns a slice of the pre-allocated work buffer
// sized to the current block size - no allocation!
func (c *Context) WorkBuffer() []float32 {
	return c.workBuffer[:c.NumSamples()]
}

// TempBuffer returns a slice of the pre-allocated temp buffer
// sized to the current block size - no allocation!
func (c *Context) TempBuffer() []float32 {
	return c.tempBuffer[:c.NumSamples()]
}

// PassThrough copies main input to main output (for bypass)
func (c *Context) PassThrough() {
	passThrough(c.Input, c.Output)
	passThrough(c.Input64, c.Output64)
}

func passThrough[T float32 | float64](in, out [][]T) {
	n := min(len(in), len(out))
	for ch := 0; ch < n; ch++ {
		copy(out[ch], in[ch])
	}
	for ch := n; ch < len(out); ch++ {
		clear(out[ch])
	}
}

// Clear zeros the main output buffers
func (c *Context) Clear() {
	for ch := range c.Output {
		clear(c.Output[ch])
	}
	for ch := range c.Output64 {
		clear(c.Output64[ch])
	}
}

// Bus access

// NumInputBuses returns the number of audio input buses.
func (c *Context) NumInputBuses() int {
	if c.double {
		return len(c.in64.cur)
	}
	return len(c.in.cur)
}

// NumOutputBuses returns the number of audio output buses.
func (c *Context) NumOutputBuses() int {
	if c.double {
		return len(c.out64.cur)
	}
	return len(c.out.cur)
}

// InputBus returns the channels of an audio input bus. Inactive buses have
// zero channels.
func (c *Context) InputBus(index int) [][]float32 { return c.in.bus(index) }

// OutputBus returns the channels of an audio output bus. Inactive buses have
// zero channels.
func (c *Context) OutputBus(index int) [][]float32 { return c.out.bus(index) }

// InputBus64 is InputBus for 64-bit blocks.
func (c *Context) InputBus64(index int) [][]float64 { return c.in64.bus(index) }

// OutputBus64 is OutputBus for 64-bit blocks.
func (c *Context) OutputBus64(index int) [][]float64 { return c.out64.bus(index) }

// Sidechain returns the channels of the first auxiliary input bus, or nil
// when the plugin has no sidechain or the host left it inactive.
func (c *Context) Sidechain() [][]float32 {
	if c.sidechainBus < 0 {
		return nil
	}
	if bus := c.InputBus(c.sidechainBus); len(bus) > 0 {
		return bus
	}
	return nil
}

// Sidechain64 is Sidechain for 64-bit blocks.
func (c *Context) Sidechain64() [][]float64 {
	if c.sidechainBus < 0 {
		return nil
	}
	if bus := c.InputBus64(c.sidechainBus); len(bus) > 0 {
		return bus
	}
	return nil
}

// InputSilenceFlags returns the host silence bitmask of an input bus: bit n
// set means channel n contains only zeros.
func (c *Context) InputSilenceFlags(bus int) uint64 {
	if bus < 0 || bus >= len(c.inSilence) {
		return 0
	}
	return c.inSilence[bus]
}

// SetOutputSilenceFlags marks output channels as silent (bit n = channel n)
// so the host can skip downstream processing. The flags apply to the whole
// host block and are reset to zero at the start of every block.
func (c *Context) SetOutputSilenceFlags(bus int, flags uint64) {
	if bus >= 0 && bus < len(c.outSilence) {
		c.outSilence[bus] = flags
	}
}

// OutputSilenceFlags returns the silence bitmask set for an output bus.
func (c *Context) OutputSilenceFlags(bus int) uint64 {
	if bus < 0 || bus >= len(c.outSilence) {
		return 0
	}
	return c.outSilence[bus]
}

// Runtime-facing block setup. These methods are called by the VST3 wrapper;
// plugin code normally never needs them except in tests.

// ConfigureBuses allocates per-bus channel storage. sidechainBus is the index
// of the first auxiliary input bus, or -1. It allocates and must not be
// called from the audio thread.
func (c *Context) ConfigureBuses(inputChannels, outputChannels []int, sidechainBus int) {
	c.in.configure(inputChannels)
	c.out.configure(outputChannels)
	c.in64.configure(inputChannels)
	c.out64.configure(outputChannels)
	c.inSilence = make([]uint64, len(inputChannels))
	c.outSilence = make([]uint64, len(outputChannels))
	c.sidechainBus = sidechainBus
	c.busesManaged = true
}

// BeginBlock resets the per-block state for a 32-bit host block of
// numSamples.
func (c *Context) BeginBlock(numSamples int) {
	c.beginBlock(numSamples, false)
}

// BeginBlock64 resets the per-block state for a 64-bit host block.
func (c *Context) BeginBlock64(numSamples int) {
	c.beginBlock(numSamples, true)
}

func (c *Context) beginBlock(numSamples int, double bool) {
	c.numSamples = numSamples
	c.blockSamples = numSamples
	c.blockOffset = 0
	c.double = double
	c.changeCount = 0
	c.inEvents = c.inEvents[:0]
	c.inSorted = true
	c.windowed = false
	c.outEvents = c.outEvents[:0]
	c.eventData = c.eventData[:0]
	c.outParams = c.outParams[:0]
	c.in.reset()
	c.out.reset()
	c.in64.reset()
	c.out64.reset()
	clear(c.inSilence)
	clear(c.outSilence)
	c.Input, c.Output, c.Input64, c.Output64 = nil, nil, nil, nil
}

// AddInputChannel appends a host channel buffer to an input bus. Channels
// beyond the configured count are ignored.
func (c *Context) AddInputChannel(bus int, samples []float32) { c.in.add(bus, samples) }

// AddOutputChannel appends a host channel buffer to an output bus. Channels
// beyond the configured count are ignored.
func (c *Context) AddOutputChannel(bus int, samples []float32) { c.out.add(bus, samples) }

// AddInputChannel64 is AddInputChannel for 64-bit blocks.
func (c *Context) AddInputChannel64(bus int, samples []float64) { c.in64.add(bus, samples) }

// AddOutputChannel64 is AddOutputChannel for 64-bit blocks.
func (c *Context) AddOutputChannel64(bus int, samples []float64) { c.out64.add(bus, samples) }

// SetInputSilenceFlags records the host silence flags of an input bus.
func (c *Context) SetInputSilenceFlags(bus int, flags uint64) {
	if bus >= 0 && bus < len(c.inSilence) {
		c.inSilence[bus] = flags
	}
}

// EventDataBuffer returns n bytes of per-block scratch memory for event
// payloads such as SysEx. It only allocates when a block needs more than the
// pre-allocated capacity.
func (c *Context) EventDataBuffer(n int) []byte {
	start := len(c.eventData)
	if start+n > cap(c.eventData) {
		return make([]byte, n)
	}
	c.eventData = c.eventData[:start+n]
	return c.eventData[start : start+n : start+n]
}

// AppendEventText converts UTF-16 event text (chord names, note expression
// text) to UTF-8 in the per-block scratch memory.
func (c *Context) AppendEventText(text []uint16) []byte {
	start := len(c.eventData)
	if start+3*len(text) > cap(c.eventData) {
		return midi.AppendUTF16(nil, text)
	}
	c.eventData = midi.AppendUTF16(c.eventData, text)
	return c.eventData[start:len(c.eventData):len(c.eventData)]
}

// adoptManualBuffers lets hand-built contexts (tests, offline tools) assign
// Input/Output (or Input64/Output64) directly: they become bus 0.
func (c *Context) adoptManualBuffers() {
	c.double = c.Input64 != nil || c.Output64 != nil
	c.in.adopt(c.Input)
	c.out.adopt(c.Output)
	c.in64.adopt(c.Input64)
	c.out64.adopt(c.Output64)
	if len(c.inSilence) != 1 {
		c.inSilence = make([]uint64, 1)
		c.outSilence = make([]uint64, 1)
	}
	c.manualBuffers = true
}

// Dispatch runs process over the current block. Parameter changes are applied
// in sample order: the block is split at every change offset so each
// ProcessAudio call sees the parameter values valid for its segment, together
// with the input events that fall inside the segment. A block with zero
// samples (a host parameter flush) only applies the changes.
func (c *Context) Dispatch(process func(*Context)) {
	c.sortInputEvents()
	c.SortParameterChanges()

	if !c.busesManaged {
		total := c.NumSamples()
		c.adoptManualBuffers()
		c.blockSamples = total
	}
	total := c.blockSamples
	c.setMainBuses()
	changes := c.paramChanges[:c.changeCount]

	if total == 0 {
		for _, change := range changes {
			c.ApplyParameterChange(change)
		}
		return
	}

	next := 0
	pos := 0
	for pos < total {
		for next < len(changes) && changes[next].SampleOffset <= pos {
			c.ApplyParameterChange(changes[next])
			next++
		}

		end := total
		if next < len(changes) && changes[next].SampleOffset < total {
			end = changes[next].SampleOffset
		}

		c.runSegment(process, pos, end, total)
		pos = end
	}

	for ; next < len(changes); next++ {
		c.ApplyParameterChange(changes[next])
	}

	c.restoreFullBlock(total)
}

func (c *Context) setMainBuses() {
	if c.double {
		c.Input, c.Output = nil, nil
		c.Input64, c.Output64 = c.in64.main(), c.out64.main()
		return
	}
	c.Input, c.Output = c.in.main(), c.out.main()
	c.Input64, c.Output64 = nil, nil
}

func (c *Context) runSegment(process func(*Context), start, end, total int) {
	if start == 0 && end == total {
		process(c)
		return
	}

	c.blockOffset = start
	c.numSamples = end - start
	if c.double {
		c.in64.slice(start, end)
		c.out64.slice(start, end)
	} else {
		c.in.slice(start, end)
		c.out.slice(start, end)
	}
	c.setMainBuses()

	c.windowed = true
	c.evStart = c.evEnd
	for c.evStart < len(c.inEvents) && int(c.inEvents[c.evStart].Offset) < start {
		c.evStart++
	}
	c.evEnd = c.evStart
	for c.evEnd < len(c.inEvents) && int(c.inEvents[c.evEnd].Offset) < end {
		c.evEnd++
	}
	if end == total {
		c.evEnd = len(c.inEvents)
	}

	process(c)
}

func (c *Context) restoreFullBlock(total int) {
	c.blockOffset = 0
	c.numSamples = total
	c.windowed = false
	c.evStart, c.evEnd = 0, 0
	c.in.restore()
	c.out.restore()
	c.in64.restore()
	c.out64.restore()
	c.setMainBuses()
	if !c.busesManaged {
		// Hand-built contexts derive the length from Input/Output each block.
		c.numSamples = 0
		c.blockSamples = 0
	}
}

// Parameter changes

// AddParameterChange adds a parameter change for sample-accurate processing
// This method is used during the parameter change collection phase
func (c *Context) AddParameterChange(paramID uint32, value float64, sampleOffset int) {
	if sampleOffset < 0 {
		sampleOffset = 0
	}
	c.ensureParameterChangeCapacity(c.changeCount + 1)
	c.paramChanges[c.changeCount] = ParameterChange{
		ParamID:      paramID,
		Value:        value,
		SampleOffset: sampleOffset,
	}
	c.changeCount++
}

// ResetParameterChanges clears the parameter change list for the next processing block
func (c *Context) ResetParameterChanges() {
	c.changeCount = 0
}

func (c *Context) ensureParameterChangeCapacity(required int) {
	if required <= len(c.paramChanges) {
		return
	}

	newCapacity := len(c.paramChanges)
	if newCapacity == 0 {
		newCapacity = defaultParameterChangeCapacity
	}
	for newCapacity < required {
		newCapacity *= 2
	}

	grown := make([]ParameterChange, newCapacity)
	copy(grown, c.paramChanges[:c.changeCount])
	c.paramChanges = grown
}

// SortParameterChanges stably sorts parameter changes by sample offset, so
// several points for the same offset keep their host order.
func (c *Context) SortParameterChanges() {
	if c.changeCount > 1 {
		slices.SortStableFunc(c.paramChanges[:c.changeCount], func(a, b ParameterChange) int {
			return cmp.Compare(a.SampleOffset, b.SampleOffset)
		})
	}
}

// GetParameterChanges returns the parameter changes of the host block.
func (c *Context) GetParameterChanges() []ParameterChange {
	return c.paramChanges[:c.changeCount]
}

// HasParameterChanges returns true if there are parameter changes in this block
func (c *Context) HasParameterChanges() bool {
	return c.changeCount > 0
}

// ApplyParameterChange applies a parameter change immediately
func (c *Context) ApplyParameterChange(change ParameterChange) {
	if c.params == nil {
		return
	}
	if p := c.params.Get(change.ParamID); p != nil {
		p.SetNormalized(change.Value)
	}
}

// SetOutputParameter reports a processor-computed value (for example a meter
// or gain-reduction readout) to the host at the start of the current segment.
// The parameter should be registered with the IsReadOnly flag.
func (c *Context) SetOutputParameter(paramID uint32, normalized float64) {
	if normalized < 0 {
		normalized = 0
	} else if normalized > 1 {
		normalized = 1
	}
	c.outParams = append(c.outParams, ParameterChange{
		ParamID:      paramID,
		Value:        normalized,
		SampleOffset: c.blockOffset,
	})
}

// OutputParameterChanges returns the output parameter values set this block.
func (c *Context) OutputParameterChanges() []ParameterChange {
	return c.outParams
}

// Events

// AddInputEvent adds an event to the input queue of the current block.
func (c *Context) AddInputEvent(event midi.Event) {
	if n := len(c.inEvents); n > 0 && c.inEvents[n-1].Offset > event.Offset {
		c.inSorted = false
	}
	c.inEvents = append(c.inEvents, event)
}

func compareEventOffset(a, b midi.Event) int {
	return cmp.Compare(a.Offset, b.Offset)
}

func (c *Context) sortInputEvents() {
	if c.inSorted {
		return
	}
	slices.SortStableFunc(c.inEvents, compareEventOffset)
	c.inSorted = true
}

func (c *Context) visibleInputEvents() []midi.Event {
	c.sortInputEvents()
	if c.windowed {
		return c.inEvents[c.evStart:c.evEnd]
	}
	return c.inEvents
}

// GetAllInputEvents returns the input events of the current segment, sorted
// by sample offset. The slice is owned by the context; do not retain it.
func (c *Context) GetAllInputEvents() []midi.Event {
	return c.visibleInputEvents()
}

// GetInputEvents returns the input events of the current segment whose block
// offset lies in [startSample, endSample).
func (c *Context) GetInputEvents(startSample, endSample int32) []midi.Event {
	events := c.visibleInputEvents()
	lo, _ := slices.BinarySearchFunc(events, startSample, func(e midi.Event, t int32) int {
		return cmp.Compare(e.Offset, t)
	})
	hi := lo
	for hi < len(events) && events[hi].Offset < endSample {
		hi++
	}
	return events[lo:hi]
}

// HasInputEvents returns true if the current segment has input events.
func (c *Context) HasInputEvents() bool {
	return len(c.visibleInputEvents()) > 0
}

// ClearInputEvents clears the input event queue
func (c *Context) ClearInputEvents() {
	c.inEvents = c.inEvents[:0]
	c.inSorted = true
	c.evStart, c.evEnd = 0, 0
}

// AddOutputEvent queues an event for the host. Its Offset is relative to
// the host block (add BlockOffset() when computing it from a segment index).
// The plugin needs an event output bus for the host to receive it.
func (c *Context) AddOutputEvent(event midi.Event) {
	c.outEvents = append(c.outEvents, event)
}

// GetOutputEvents returns the events queued for the host this block, sorted
// by sample offset. The slice is owned by the context; do not retain it.
func (c *Context) GetOutputEvents() []midi.Event {
	slices.SortStableFunc(c.outEvents, compareEventOffset)
	return c.outEvents
}

// ClearOutputEvents clears the output event queue
func (c *Context) ClearOutputEvents() {
	c.outEvents = c.outEvents[:0]
}

// ClearAllEvents clears both input and output event queues
func (c *Context) ClearAllEvents() {
	c.ClearInputEvents()
	c.ClearOutputEvents()
}

// ProcessEvents feeds the segment's events in [startSample, endSample) to processor.
func (c *Context) ProcessEvents(processor midi.EventProcessor, startSample, endSample int32) {
	for _, event := range c.GetInputEvents(startSample, endSample) {
		processor.ProcessEvent(event)
	}
}
