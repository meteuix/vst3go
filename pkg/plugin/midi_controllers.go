package plugin

import (
	"fmt"
	"math"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/midi"
)

// MIDIControllerParamIDBase is the first parameter ID reserved for the hidden
// MIDI controller parameters of a MIDIControllerProcessor. Plugin parameter
// IDs must stay below it.
const MIDIControllerParamIDBase uint32 = 0x7F000000

// midiControllersPerChannel covers CC 0-127, channel pressure and pitch bend.
const midiControllersPerChannel = 130

// midiControllers implements the hidden-parameter bridge between IMidiMapping
// and MIDI controller events.
type midiControllers struct {
	channels int
	values   []uint64 // float64 bits, indexed like the parameter IDs
}

func newMIDIControllers(channels int, registry *param.Registry) (*midiControllers, error) {
	if channels <= 0 {
		return nil, nil
	}
	if channels > 16 {
		channels = 16
	}
	m := &midiControllers{
		channels: channels,
		values:   make([]uint64, channels*midiControllersPerChannel),
	}
	for i := range m.values {
		m.values[i] = math.Float64bits(m.defaultValue(i))
	}
	if registry != nil {
		for _, p := range registry.All() {
			if m.owns(p.ID) {
				return nil, fmt.Errorf("parameter %d (%s) collides with the MIDI controller ID range", p.ID, p.Name)
			}
		}
	}
	return m, nil
}

func (m *midiControllers) count() int32 {
	if m == nil {
		return 0
	}
	return int32(len(m.values))
}

func (m *midiControllers) owns(id uint32) bool {
	return m != nil && id >= MIDIControllerParamIDBase && id < MIDIControllerParamIDBase+uint32(len(m.values))
}

func (m *midiControllers) paramID(channel, controller int) (uint32, bool) {
	if m == nil || channel < 0 || channel >= m.channels || controller < 0 || controller >= midiControllersPerChannel {
		return 0, false
	}
	return MIDIControllerParamIDBase + uint32(channel*midiControllersPerChannel+controller), true
}

func (m *midiControllers) split(id uint32) (channel, controller int) {
	index := int(id - MIDIControllerParamIDBase)
	return index / midiControllersPerChannel, index % midiControllersPerChannel
}

func (m *midiControllers) defaultValue(index int) float64 {
	if index%midiControllersPerChannel == int(MIDIControllerPitchBend) {
		return 0.5
	}
	return 0
}

func (m *midiControllers) info(index int32) *paramInfo {
	id := MIDIControllerParamIDBase + uint32(index)
	channel, controller := m.split(id)
	title := fmt.Sprintf("MIDI CC %d", controller)
	steps := int32(127)
	switch int16(controller) {
	case MIDIControllerAfterTouch:
		title = "MIDI Aftertouch"
	case MIDIControllerPitchBend:
		title = "MIDI Pitch Bend"
		steps = 16383
	}
	return &paramInfo{
		ID:           id,
		Title:        fmt.Sprintf("%s [Ch %d]", title, channel+1),
		ShortTitle:   title,
		StepCount:    steps,
		DefaultValue: m.defaultValue(int(index)),
		Flags:        param.IsHidden,
	}
}

func (m *midiControllers) get(id uint32) float64 {
	return math.Float64frombits(m.values[id-MIDIControllerParamIDBase])
}

func (m *midiControllers) set(id uint32, value float64) {
	m.values[id-MIDIControllerParamIDBase] = math.Float64bits(clampUnit(value))
}

// event converts a parameter point of a controller parameter to its MIDI event.
func (m *midiControllers) event(id uint32, value float64, offset int32) midi.Event {
	channel, controller := m.split(id)
	value = clampUnit(value)
	switch int16(controller) {
	case MIDIControllerAfterTouch:
		return midi.ChannelPressure(uint8(channel), value).At(offset)
	case MIDIControllerPitchBend:
		// 14-bit parameter steps, center 8192 = 0.
		return midi.PitchBend(uint8(channel), (math.Round(value*16383)-8192)/8192).At(offset)
	default:
		return midi.ControlChange(uint8(channel), uint8(controller), value).At(offset)
	}
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// paramInfo is the host-facing description of one parameter.
type paramInfo struct {
	ID           uint32
	Title        string
	ShortTitle   string
	Units        string
	StepCount    int32
	DefaultValue float64
	UnitID       int32
	Flags        uint32
}
