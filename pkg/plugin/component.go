package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/th-release/vst3go/pkg/framework/bus"
	"github.com/th-release/vst3go/pkg/framework/param"
	"github.com/th-release/vst3go/pkg/framework/plugin"
	"github.com/th-release/vst3go/pkg/framework/process"
	"github.com/th-release/vst3go/pkg/framework/state"
)

var (
	errInvalidArgument = errors.New("invalid argument")
	errNotSupported    = errors.New("not supported")
)

const (
	defaultSampleRate   = 48000
	defaultMaxBlockSize = 4096
)

// componentImpl adapts a Processor to the VST3 component and controller
// contract. All host-thread (non-audio) methods are serialized by mu; the
// audio thread only touches processCtx and the processor.
type componentImpl struct {
	processor  Processor
	pluginInfo plugin.Info
	params     *param.Registry
	buses      *bus.Configuration
	midiCC     *midiControllers
	mapper     MIDIMappingProcessor

	mu           sync.Mutex
	processCtx   *process.Context
	sampleRate   float64
	maxBlockSize int32
	processMode  process.ProcessMode
	active       bool
	processing   atomic.Bool

	wrapper *componentWrapper // host notifications; nil in pure-Go tests
}

func newComponent(processor Processor, info plugin.Info) (*componentImpl, error) {
	params := processor.GetParameters()
	if params == nil {
		return nil, fmt.Errorf("processor %q has no parameter registry", info.Name)
	}
	buses := processor.GetBuses()
	if buses == nil {
		return nil, fmt.Errorf("processor %q has no bus configuration", info.Name)
	}

	c := &componentImpl{
		processor:    processor,
		pluginInfo:   info,
		params:       params,
		buses:        buses,
		sampleRate:   defaultSampleRate,
		maxBlockSize: defaultMaxBlockSize,
	}
	if mapper, ok := processor.(MIDIMappingProcessor); ok {
		c.mapper = mapper
	}
	if cc, ok := processor.(MIDIControllerProcessor); ok {
		midiCC, err := newMIDIControllers(cc.MIDIControllerChannels(), params)
		if err != nil {
			return nil, err
		}
		c.midiCC = midiCC
	}
	c.rebuildProcessContext()
	return c, nil
}

// rebuildProcessContext allocates a context sized for the current block size
// and bus layout. Caller holds mu (or has exclusive access).
func (c *componentImpl) rebuildProcessContext() {
	ctx := process.NewContext(int(c.maxBlockSize), c.params)
	inputs := c.audioChannelCounts(bus.DirectionInput)
	outputs := c.audioChannelCounts(bus.DirectionOutput)
	ctx.ConfigureBuses(inputs, outputs, c.sidechainIndex())
	ctx.SampleRate = c.sampleRate
	ctx.ProcessMode = c.processMode
	c.processCtx = ctx
}

func (c *componentImpl) audioChannelCounts(direction bus.Direction) []int {
	count := c.buses.GetBusCount(bus.MediaTypeAudio, direction)
	channels := make([]int, count)
	for i := range channels {
		if info := c.buses.GetBusInfo(bus.MediaTypeAudio, direction, int32(i)); info != nil {
			channels[i] = int(info.ChannelCount)
		}
	}
	return channels
}

func (c *componentImpl) sidechainIndex() int {
	count := c.buses.GetBusCount(bus.MediaTypeAudio, bus.DirectionInput)
	for i := int32(0); i < count; i++ {
		if info := c.buses.GetBusInfo(bus.MediaTypeAudio, bus.DirectionInput, i); info != nil && info.BusType == bus.TypeAux {
			return int(i)
		}
	}
	return -1
}

func (c *componentImpl) stateManager() *state.Manager {
	manager := state.NewManager(c.params)
	if stateful, ok := c.processor.(StatefulProcessor); ok {
		manager.SetCustomLoadFunc(stateful.LoadCustomState)
		manager.SetCustomSaveFunc(stateful.SaveCustomState)
	}
	return manager
}

// IComponent

func (c *componentImpl) Initialize() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processor.Initialize(c.sampleRate, c.maxBlockSize)
}

func (c *componentImpl) busInfo(mediaType, direction, index int32) *bus.Info {
	if mediaType != int32(bus.MediaTypeAudio) && mediaType != int32(bus.MediaTypeEvent) {
		return nil
	}
	if direction != int32(bus.DirectionInput) && direction != int32(bus.DirectionOutput) {
		return nil
	}
	return c.buses.GetBusInfo(bus.MediaType(mediaType), bus.Direction(direction), index)
}

func (c *componentImpl) GetBusCount(mediaType, direction int32) int32 {
	if mediaType != int32(bus.MediaTypeAudio) && mediaType != int32(bus.MediaTypeEvent) {
		return 0
	}
	if direction != int32(bus.DirectionInput) && direction != int32(bus.DirectionOutput) {
		return 0
	}
	return c.buses.GetBusCount(bus.MediaType(mediaType), bus.Direction(direction))
}

func (c *componentImpl) ActivateBus(mediaType, direction, index int32, state bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busInfo(mediaType, direction, index) == nil {
		return errInvalidArgument
	}
	return c.buses.SetBusActive(bus.MediaType(mediaType), bus.Direction(direction), index, state)
}

func (c *componentImpl) SetActive(active bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if active && !c.active {
		// Bus arrangements and activation are final now; size the context.
		c.rebuildProcessContext()
	}
	c.active = active
	if !active {
		c.processCtx.ResetParameterChanges()
		c.processCtx.ClearAllEvents()
	}
	return c.processor.SetActive(active)
}

func (c *componentImpl) SetState(stateData []byte) error {
	if err := c.stateManager().Load(bytes.NewReader(stateData)); err != nil {
		return fmt.Errorf("set component state: %w", err)
	}
	return nil
}

func (c *componentImpl) GetState() ([]byte, error) {
	var buf bytes.Buffer
	if err := c.stateManager().Save(&buf); err != nil {
		return nil, fmt.Errorf("get component state: %w", err)
	}
	return buf.Bytes(), nil
}

// IAudioProcessor

func (c *componentImpl) SetBusArrangements(inputs, outputs []bus.SpeakerArrangement) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if int32(len(inputs)) != c.buses.GetBusCount(bus.MediaTypeAudio, bus.DirectionInput) ||
		int32(len(outputs)) != c.buses.GetBusCount(bus.MediaTypeAudio, bus.DirectionOutput) {
		return false
	}
	if c.arrangementsMatch(bus.DirectionInput, inputs) && c.arrangementsMatch(bus.DirectionOutput, outputs) {
		return true
	}

	flexible, ok := c.processor.(BusArrangementProcessor)
	if !ok || !flexible.AcceptBusArrangements(inputs, outputs) {
		return false
	}
	for i, arrangement := range inputs {
		if err := c.buses.SetArrangement(bus.DirectionInput, int32(i), arrangement); err != nil {
			return false
		}
	}
	for i, arrangement := range outputs {
		if err := c.buses.SetArrangement(bus.DirectionOutput, int32(i), arrangement); err != nil {
			return false
		}
	}
	c.rebuildProcessContext()
	return true
}

func (c *componentImpl) arrangementsMatch(direction bus.Direction, arrangements []bus.SpeakerArrangement) bool {
	for i, arrangement := range arrangements {
		info := c.buses.GetBusInfo(bus.MediaTypeAudio, direction, int32(i))
		if info == nil || info.SpeakerArrangement() != arrangement {
			return false
		}
	}
	return true
}

func (c *componentImpl) GetBusArrangement(direction, index int32) (bus.SpeakerArrangement, bool) {
	info := c.busInfo(int32(bus.MediaTypeAudio), direction, index)
	if info == nil {
		return 0, false
	}
	return info.SpeakerArrangement(), true
}

func (c *componentImpl) GetLatencySamples() uint32 {
	latency := c.processor.GetLatencySamples()
	if latency < 0 {
		return 0
	}
	return uint32(latency)
}

func (c *componentImpl) GetTailSamples() uint32 {
	// TailInfinite (-1) maps to kInfiniteTail (0xFFFFFFFF).
	return uint32(c.processor.GetTailSamples())
}

func (c *componentImpl) SetupProcessing(sampleRate float64, maxBlockSize int32, mode process.ProcessMode) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if sampleRate <= 0 || maxBlockSize < 0 {
		return errInvalidArgument
	}
	c.sampleRate = sampleRate
	if maxBlockSize > 0 {
		c.maxBlockSize = maxBlockSize
	}
	c.processMode = mode
	c.rebuildProcessContext()

	if err := c.processor.Initialize(c.sampleRate, c.maxBlockSize); err != nil {
		return fmt.Errorf("setup processing: initialize processor: %w", err)
	}
	return nil
}

func (c *componentImpl) SetProcessing(state bool) {
	c.processing.Store(state)
}

// IEditController

func (c *componentImpl) GetParameterCount() int32 {
	return c.params.Count() + c.midiCC.count()
}

func (c *componentImpl) GetParameterInfo(index int32) (*paramInfo, error) {
	if index < 0 {
		return nil, errInvalidArgument
	}
	count := c.params.Count()
	if index >= count {
		if index-count < c.midiCC.count() {
			return c.midiCC.info(index - count), nil
		}
		return nil, errInvalidArgument
	}

	p := c.params.GetByIndex(index)
	if p == nil {
		return nil, errInvalidArgument
	}
	return &paramInfo{
		ID:           p.ID,
		Title:        p.Name,
		ShortTitle:   p.ShortName,
		Units:        p.Unit,
		StepCount:    p.StepCount,
		DefaultValue: clampUnit(p.DefaultValue),
		UnitID:       p.UnitID,
		Flags:        p.Flags,
	}, nil
}

func (c *componentImpl) GetParamStringByValue(id uint32, value float64) (string, error) {
	if c.midiCC.owns(id) {
		info := c.midiCC.info(int32(id - MIDIControllerParamIDBase))
		return fmt.Sprintf("%.0f", clampUnit(value)*float64(info.StepCount)), nil
	}
	if p, ok := c.params.GetOK(id); ok {
		return p.FormatValue(clampUnit(value)), nil
	}
	return "", errInvalidArgument
}

func (c *componentImpl) GetParamValueByString(id uint32, str string) (float64, error) {
	if c.midiCC.owns(id) {
		var v float64
		if _, err := fmt.Sscanf(str, "%g", &v); err != nil {
			return 0, err
		}
		info := c.midiCC.info(int32(id - MIDIControllerParamIDBase))
		return clampUnit(v / float64(info.StepCount)), nil
	}
	if p, ok := c.params.GetOK(id); ok {
		return p.ParseValue(str)
	}
	return 0, errInvalidArgument
}

func (c *componentImpl) NormalizedParamToPlain(id uint32, normalized float64) float64 {
	if p, ok := c.params.GetOK(id); ok {
		return p.Denormalize(normalized)
	}
	return normalized
}

func (c *componentImpl) PlainParamToNormalized(id uint32, plain float64) float64 {
	if p, ok := c.params.GetOK(id); ok {
		return p.Normalize(plain)
	}
	return plain
}

func (c *componentImpl) GetParamNormalized(id uint32) float64 {
	if c.midiCC.owns(id) {
		return c.midiCC.get(id)
	}
	if value, ok := c.params.GetNormalized(id); ok {
		return value
	}
	return 0
}

func (c *componentImpl) SetParamNormalized(id uint32, value float64) error {
	if c.midiCC.owns(id) {
		c.midiCC.set(id, value)
		return nil
	}
	if p, ok := c.params.GetOK(id); ok {
		p.SetNormalized(value)
		return nil
	}
	return errInvalidArgument
}

// MIDIControllerAssignment implements IMidiMapping.
func (c *componentImpl) MIDIControllerAssignment(busIndex int32, channel, controller int16) (uint32, bool) {
	if c.mapper != nil {
		if id, ok := c.mapper.MIDIControllerParameter(busIndex, channel, controller); ok {
			return id, true
		}
	}
	if busIndex != 0 {
		return 0, false
	}
	return c.midiCC.paramID(int(channel), int(controller))
}

// Editor support

func (c *componentImpl) EditorModel() (*EditorModel, error) {
	return BuildEditorModel(c.pluginInfo, c.params)
}

func (c *componentImpl) EditorSnapshot() (*EditorSnapshot, error) {
	return BuildEditorSnapshot(c.pluginInfo, c.params)
}

func (c *componentImpl) ApplyEditorSnapshot(snapshot *EditorSnapshot) error {
	return snapshot.Apply(c.params)
}

// EditParameter performs a plugin-initiated parameter change and reports it
// to the host as a complete begin/perform/end edit gesture.
func (c *componentImpl) EditParameter(id uint32, value float64) error {
	p, ok := c.params.GetOK(id)
	if !ok {
		return errInvalidArgument
	}
	value = clampUnit(value)
	if c.wrapper == nil {
		p.SetNormalized(value)
		return nil
	}
	c.wrapper.notifyParamBeginEdit(id)
	p.SetNormalized(value)
	c.wrapper.notifyParamPerformEdit(id, value)
	c.wrapper.notifyEditorParameterChanged(id, p.GetNormalized(), p.GetPlain())
	c.wrapper.notifyParamEndEdit(id)
	return nil
}
