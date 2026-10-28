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
// InputBus/OutputBus. When the host automates parameters inside a block, the
// runtime splits the block at each change and calls ProcessAudio once per
// segment; BlockOffset reports where the current segment starts inside the
// host block. All event and parameter-change offsets are relative to the host
// block, so a sample index i of Output corresponds to block offset
// BlockOffset()+i.
//
// Slices returned by Context accessors are only valid during the current
// ProcessAudio call.
type Context struct {
	Input       [][]float32
	Output      [][]float32
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

	// Audio buses. fullIn/fullOut hold the host block buffers; segIn/segOut
	// hold per-segment views; curIn/curOut point to whichever is active.
	fullIn, fullOut [][][]float32
	segIn, segOut   [][][]float32
	curIn, curOut   [][][]float32
	inSilence       []uint64
	outSilence      []uint64
	sidechainBus    int
	busesManaged    bool // bus storage is filled by the runtime via BeginBlock

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
	if len(c.Input) > 0 && len(c.Input[0]) > 0 {
		return len(c.Input[0])
	}
	if len(c.Output) > 0 && len(c.Output[0]) > 0 {
		return len(c.Output[0])
	}
	return 0
}

// BlockOffset returns the start of the current segment inside the host block.
// It is non-zero only when the runtime split the block for automation.
func (c *Context) BlockOffset() int {
	return c.blockOffset
}

// NumInputChannels returns the number of main input channels
func (c *Context) NumInputChannels() int {
	return len(c.Input)
}

// NumOutputChannels returns the number of main output channels
func (c *Context) NumOutputChannels() int {
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
	numChannels := c.NumInputChannels()
	if c.NumOutputChannels() < numChannels {
		numChannels = c.NumOutputChannels()
	}

	for ch := 0; ch < numChannels; ch++ {
		copy(c.Output[ch], c.Input[ch])
	}
	for ch := numChannels; ch < c.NumOutputChannels(); ch++ {
		clear(c.Output[ch])
	}
}

// Clear zeros the main output buffers
func (c *Context) Clear() {
	for ch := range c.Output {
		clear(c.Output[ch])
	}
}

// Bus access

// NumInputBuses returns the number of audio input buses.
func (c *Context) NumInputBuses() int {
	if c.curIn == nil {
		return boolToInt(c.Input != nil)
	}
	return len(c.curIn)
}

// NumOutputBuses returns the number of audio output buses.
func (c *Context) NumOutputBuses() int {
	if c.curOut == nil {
		return boolToInt(c.Output != nil)
	}
	return len(c.curOut)
}

// InputBus returns the channels of an audio input bus. Inactive buses have
// zero channels.
func (c *Context) InputBus(index int) [][]float32 {
	if c.curIn == nil {
		if index == 0 {
			return c.Input
		}
		return nil
	}
	if index < 0 || index >= len(c.curIn) {
		return nil
	}
	return c.curIn[index]
}

// OutputBus returns the channels of an audio output bus. Inactive buses have
// zero channels.
func (c *Context) OutputBus(index int) [][]float32 {
	if c.curOut == nil {
		if index == 0 {
			return c.Output
		}
		return nil
	}
	if index < 0 || index >= len(c.curOut) {
		return nil
	}
	return c.curOut[index]
}

// Sidechain returns the channels of the first auxiliary input bus, or nil
// when the plugin has no sidechain or the host left it inactive.
func (c *Context) Sidechain() [][]float32 {
	if c.sidechainBus < 0 {
		return nil
	}
	bus := c.InputBus(c.sidechainBus)
	if len(bus) == 0 {
		return nil
	}
	return bus
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
	c.fullIn, c.segIn = makeBusStorage(inputChannels)
	c.fullOut, c.segOut = makeBusStorage(outputChannels)
	c.inSilence = make([]uint64, len(inputChannels))
	c.outSilence = make([]uint64, len(outputChannels))
	c.sidechainBus = sidechainBus
	c.curIn = c.fullIn
	c.curOut = c.fullOut
	c.busesManaged = true
}

// adoptManualBuffers lets hand-built contexts (tests, offline tools) assign
// Input/Output directly: the channels become bus 0 of the bus storage.
func (c *Context) adoptManualBuffers() {
	if c.busesManaged {
		return
	}
	in, out := c.Input, c.Output
	if len(c.fullIn) != 1 || cap(c.fullIn[0]) < len(in) {
		c.fullIn, c.segIn = makeBusStorage([]int{len(in)})
		c.inSilence = make([]uint64, 1)
	}
	if len(c.fullOut) != 1 || cap(c.fullOut[0]) < len(out) {
		c.fullOut, c.segOut = makeBusStorage([]int{len(out)})
		c.outSilence = make([]uint64, 1)
	}
	c.fullIn[0] = append(c.fullIn[0][:0], in...)
	c.fullOut[0] = append(c.fullOut[0][:0], out...)
	c.curIn = c.fullIn
	c.curOut = c.fullOut
}

func makeBusStorage(channels []int) (full, seg [][][]float32) {
	full = make([][][]float32, len(channels))
	seg = make([][][]float32, len(channels))
	for i, n := range channels {
		full[i] = make([][]float32, 0, n)
		seg[i] = make([][]float32, 0, n)
	}
	return full, seg
}

// BeginBlock resets the per-block state for a host block of numSamples.
func (c *Context) BeginBlock(numSamples int) {
	c.numSamples = numSamples
	c.blockSamples = numSamples
	c.blockOffset = 0
	c.changeCount = 0
	c.inEvents = c.inEvents[:0]
	c.inSorted = true
	c.windowed = false
	c.outEvents = c.outEvents[:0]
	c.eventData = c.eventData[:0]
	c.outParams = c.outParams[:0]
	for i := range c.fullIn {
		c.fullIn[i] = c.fullIn[i][:0]
		c.inSilence[i] = 0
	}
	for i := range c.fullOut {
		c.fullOut[i] = c.fullOut[i][:0]
		c.outSilence[i] = 0
	}
	c.curIn = c.fullIn
	c.curOut = c.fullOut
	c.Input = nil
	c.Output = nil
}

// AddInputChannel appends a host channel buffer to an input bus. Channels
// beyond the configured count are ignored.
func (c *Context) AddInputChannel(bus int, samples []float32) {
	if bus >= 0 && bus < len(c.fullIn) && len(c.fullIn[bus]) < cap(c.fullIn[bus]) {
		c.fullIn[bus] = append(c.fullIn[bus], samples)
	}
}

// AddOutputChannel appends a host channel buffer to an output bus. Channels
// beyond the configured count are ignored.
func (c *Context) AddOutputChannel(bus int, samples []float32) {
	if bus >= 0 && bus < len(c.fullOut) && len(c.fullOut[bus]) < cap(c.fullOut[bus]) {
		c.fullOut[bus] = append(c.fullOut[bus], samples)
	}
}

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

// Dispatch runs process over the current block. Parameter changes are applied
// in sample order: the block is split at every change offset so each
// ProcessAudio call sees the parameter values valid for its segment, together
// with the input events that fall inside the segment. A block with zero
// samples (a host parameter flush) only applies the changes.
func (c *Context) Dispatch(process func(*Context)) {
	c.sortInputEvents()
	c.SortParameterChanges()

	total := c.NumSamples()
	if !c.busesManaged {
		c.adoptManualBuffers()
		c.blockSamples = total
	}
	total = c.blockSamples
	c.Input = mainBus(c.fullIn)
	c.Output = mainBus(c.fullOut)
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

func (c *Context) runSegment(process func(*Context), start, end, total int) {
	if start == 0 && end == total {
		process(c)
		return
	}

	c.blockOffset = start
	c.numSamples = end - start
	c.curIn = sliceBuses(c.segIn, c.fullIn, start, end)
	c.curOut = sliceBuses(c.segOut, c.fullOut, start, end)
	c.Input = mainBus(c.curIn)
	c.Output = mainBus(c.curOut)

	c.windowed = true
	c.evStart = c.evEnd
	for c.evStart < len(c.inEvents) && int(c.inEvents[c.evStart].SampleOffset()) < start {
		c.evStart++
	}
	c.evEnd = c.evStart
	for c.evEnd < len(c.inEvents) && int(c.inEvents[c.evEnd].SampleOffset()) < end {
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
	if !c.busesManaged {
		// Hand-built contexts derive the length from Input/Output each block.
		c.numSamples = 0
		c.blockSamples = 0
	}
	c.windowed = false
	c.evStart, c.evEnd = 0, 0
	c.curIn = c.fullIn
	c.curOut = c.fullOut
	c.Input = mainBus(c.fullIn)
	c.Output = mainBus(c.fullOut)
}

func mainBus(buses [][][]float32) [][]float32 {
	if len(buses) == 0 {
		return nil
	}
	return buses[0]
}

func sliceBuses(dst, src [][][]float32, start, end int) [][][]float32 {
	for b := range src {
		dst[b] = dst[b][:0]
		for _, ch := range src[b] {
			dst[b] = append(dst[b], ch[start:end])
		}
	}
	return dst
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	if n := len(c.inEvents); n > 0 && c.inEvents[n-1].SampleOffset() > event.SampleOffset() {
		c.inSorted = false
	}
	c.inEvents = append(c.inEvents, event)
}

func (c *Context) sortInputEvents() {
	if c.inSorted {
		return
	}
	slices.SortStableFunc(c.inEvents, func(a, b midi.Event) int {
		return cmp.Compare(a.SampleOffset(), b.SampleOffset())
	})
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
		return cmp.Compare(e.SampleOffset(), t)
	})
	hi := lo
	for hi < len(events) && events[hi].SampleOffset() < endSample {
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

// AddOutputEvent queues an event for the host. Its SampleOffset is relative
// to the host block (add BlockOffset() when computing it from a segment
// index). The plugin needs an event output bus for the host to receive it.
func (c *Context) AddOutputEvent(event midi.Event) {
	c.outEvents = append(c.outEvents, event)
}

// GetOutputEvents returns the events queued for the host this block, sorted
// by sample offset. The slice is owned by the context; do not retain it.
func (c *Context) GetOutputEvents() []midi.Event {
	slices.SortStableFunc(c.outEvents, func(a, b midi.Event) int {
		return cmp.Compare(a.SampleOffset(), b.SampleOffset())
	})
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
