package plugin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

// programListID is the ID of the single program list a ProgramListProcessor
// publishes.
const programListID int32 = 0

// rootUnitID is the VST3 root unit (kRootUnitId).
const rootUnitID int32 = 0

// programDataMagic starts the IProgramListData payload of one program.
var programDataMagic = [8]byte{'V', 'S', 'T', '3', 'G', 'O', 'P', 'G'}

var errProgramData = errors.New("invalid program data")

// paramValue is one parameter value of a program.
type paramValue struct {
	id         uint32
	normalized float64
}

// programs holds the factory presets of a ProgramListProcessor. Selecting a
// program sets every non-read-only, non-bypass parameter: to the program's
// value when listed, else to its default.
type programs struct {
	list  ProgramList
	param *param.Parameter

	// values holds, per program, the normalized values to apply. The slice
	// is replaced as a whole (IProgramListData) and read on the audio thread.
	values atomic.Pointer[[][]paramValue]

	audioProgram atomic.Int32 // last program applied on the audio thread
	uiProgram    atomic.Int32 // last program applied on the host thread
}

func newPrograms(processor ProgramListProcessor, registry *param.Registry) (*programs, error) {
	list := processor.ProgramList()
	if len(list.Programs) == 0 {
		return nil, fmt.Errorf("program list %q has no programs", list.Name)
	}
	if list.Name == "" {
		list.Name = "Factory Presets"
	}
	if registry.Has(ProgramParamID) {
		return nil, fmt.Errorf("parameter ID %#x is reserved for the program selector", ProgramParamID)
	}

	steps := int32(len(list.Programs) - 1)
	selector := &param.Parameter{
		ID:        ProgramParamID,
		Name:      "Program",
		ShortName: "Prog",
		Min:       0,
		Max:       float64(steps),
		StepCount: steps,
		Flags:     param.CanAutomate | param.IsList | param.IsProgramChange,
		UnitID:    rootUnitID,
	}
	names := make([]string, len(list.Programs))
	for i, program := range list.Programs {
		names[i] = program.Name
	}
	selector.SetFormatter(func(plain float64) string {
		index := int(math.Round(plain))
		if index < 0 || index >= len(names) {
			return ""
		}
		return names[index]
	}, func(text string) (float64, error) {
		for i, name := range names {
			if name == text {
				return float64(i), nil
			}
		}
		return 0, fmt.Errorf("unknown program %q", text)
	})
	if err := registry.Add(selector); err != nil {
		return nil, err
	}

	p := &programs{list: list, param: selector}
	values := make([][]paramValue, len(list.Programs))
	for i, program := range list.Programs {
		values[i] = programValues(registry, program.Values)
	}
	p.values.Store(&values)
	p.audioProgram.Store(-1)
	p.uiProgram.Store(-1)
	return p, nil
}

// programValues resolves plain program values to the normalized values of
// every program-controlled parameter.
func programValues(registry *param.Registry, plain map[uint32]float64) []paramValue {
	var values []paramValue
	for _, p := range registry.All() {
		if p.ID == ProgramParamID || p.Flags&(param.IsReadOnly|param.IsBypass) != 0 {
			continue
		}
		normalized := clampUnit(p.DefaultValue)
		if value, ok := plain[p.ID]; ok {
			normalized = p.Normalize(value)
		}
		values = append(values, paramValue{id: p.ID, normalized: normalized})
	}
	return values
}

func (p *programs) count() int32 {
	if p == nil {
		return 0
	}
	return int32(len(p.list.Programs))
}

// index converts a normalized selector value to a program index.
func (p *programs) index(normalized float64) int32 {
	return int32(p.param.Denormalize(normalized))
}

// valuesOf returns the values of a program; nil for an invalid index.
func (p *programs) valuesOf(index int32) []paramValue {
	values := *p.values.Load()
	if index < 0 || int(index) >= len(values) {
		return nil
	}
	return values[index]
}

// syncSelection records the current selector value as applied, so that
// restoring a state does not overwrite the restored values with the program.
func (p *programs) syncSelection() {
	if p == nil {
		return
	}
	index := p.index(p.param.GetNormalized())
	p.audioProgram.Store(index)
	p.uiProgram.Store(index)
}

// encode serializes the values of a program for IProgramListData.
func (p *programs) encode(index int32) ([]byte, error) {
	values := p.valuesOf(index)
	if values == nil {
		return nil, errInvalidArgument
	}
	var buf bytes.Buffer
	buf.Write(programDataMagic[:])
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(values)))
	for _, v := range values {
		_ = binary.Write(&buf, binary.LittleEndian, v.id)
		_ = binary.Write(&buf, binary.LittleEndian, v.normalized)
	}
	return buf.Bytes(), nil
}

// decode replaces the values of a program with IProgramListData content.
// Parameters missing from data keep their current program value.
func (p *programs) decode(index int32, data []byte, registry *param.Registry) error {
	old := p.valuesOf(index)
	if old == nil {
		return errInvalidArgument
	}
	r := bytes.NewReader(data)
	var magic [8]byte
	var count uint32
	if _, err := r.Read(magic[:]); err != nil || magic != programDataMagic {
		return errProgramData
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) > r.Len()/12 {
		return errProgramData
	}
	loaded := make(map[uint32]float64, count)
	for i := uint32(0); i < count; i++ {
		var id uint32
		var normalized float64
		if binary.Read(r, binary.LittleEndian, &id) != nil || binary.Read(r, binary.LittleEndian, &normalized) != nil {
			return errProgramData
		}
		if registry.Has(id) {
			loaded[id] = clampUnit(normalized)
		}
	}

	updated := make([]paramValue, len(old))
	for i, v := range old {
		if normalized, ok := loaded[v.id]; ok {
			v.normalized = normalized
		}
		updated[i] = v
	}
	current := *p.values.Load()
	next := make([][]paramValue, len(current))
	copy(next, current)
	next[index] = updated
	p.values.Store(&next)
	return nil
}
