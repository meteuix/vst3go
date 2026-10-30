package nam

import (
	"encoding/json"
	"errors"
	"fmt"
)

// sequential is NAM's Sequential model: models run one after another.
type sequential struct {
	stages []network
	bufs   []matrix // bufs[i] is stage i's output, for all but the last
}

// newSequential also returns the rate the models agree on, -1 when none
// names one.
func newSequential(s spec) (*sequential, float64, error) {
	var o object
	if err := json.Unmarshal(s.config, &o); err != nil {
		return nil, 0, fmt.Errorf("config: %w", err)
	}
	var models []json.RawMessage
	if err := o.get("models", &models); err != nil {
		return nil, 0, err
	}
	if len(models) == 0 {
		return nil, 0, errors.New("no models")
	}
	m := &sequential{}
	rate := s.rate
	prevOut := 0
	for i, raw := range models {
		cs, err := parseSpec(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("model %d: %w", i, err)
		}
		net, r, err := build(cs, 1)
		if err != nil {
			return nil, 0, fmt.Errorf("model %d: %w", i, err)
		}
		switch {
		case r < 0:
		case rate < 0:
			rate = r
		case r != rate:
			return nil, 0, fmt.Errorf("model %d runs at %v Hz, not %v", i, r, rate)
		}
		in, out := net.channels()
		if i > 0 {
			if in != prevOut {
				return nil, 0, fmt.Errorf("model %d has %d inputs, the model before it %d outputs", i, in, prevOut)
			}
			m.bufs = append(m.bufs, newBlock(prevOut))
		}
		m.stages = append(m.stages, net)
		prevOut = out
	}
	return m, rate, nil
}

func (m *sequential) channels() (int, int) {
	in, _ := m.stages[0].channels()
	_, out := m.stages[len(m.stages)-1].channels()
	return in, out
}

func (m *sequential) process(in, out matrix, n int) {
	x := in
	for i, s := range m.stages {
		dst := out
		if i < len(m.bufs) {
			dst = m.bufs[i]
		}
		s.process(x, dst, n)
		x = dst
	}
}

func (m *sequential) reset() {
	for _, s := range m.stages {
		s.reset()
	}
}

func (m *sequential) prewarm() int {
	n := 0
	for _, s := range m.stages {
		n += s.prewarm()
	}
	return n
}

type containerConfig struct {
	Submodels []struct {
		MaxValue *float64        `json:"max_value"`
		Model    json.RawMessage `json:"model"`
	} `json:"submodels"`
}

// newContainer builds NAM's SlimmableContainer: mono models of increasing
// size, of which the size picks the first whose max_value is above it.
// The container runs that model, so it returns it, with the rate the
// container runs at.
func newContainer(s spec, size float64) (network, float64, error) {
	var c containerConfig
	if err := json.Unmarshal(s.config, &c); err != nil {
		return nil, 0, fmt.Errorf("config: %w", err)
	}
	if len(c.Submodels) == 0 {
		return nil, 0, errors.New("no submodels")
	}
	rate := s.rate
	var active, last network
	for i, sm := range c.Submodels {
		if sm.MaxValue == nil {
			return nil, 0, fmt.Errorf("submodel %d has no max_value", i)
		}
		if i > 0 && *sm.MaxValue <= *c.Submodels[i-1].MaxValue {
			return nil, 0, errors.New("submodels are not sorted by ascending max_value")
		}
		cs, err := parseSpec(sm.Model)
		if err != nil {
			return nil, 0, fmt.Errorf("submodel %d: %w", i, err)
		}
		net, r, err := build(cs, 1)
		if err != nil {
			return nil, 0, fmt.Errorf("submodel %d: %w", i, err)
		}
		if in, out := net.channels(); in != 1 || out != 1 {
			return nil, 0, fmt.Errorf("submodel %d has %d inputs and %d outputs, want 1 and 1", i, in, out)
		}
		switch {
		case r < 0:
		case s.rate >= 0 && r != s.rate:
			return nil, 0, fmt.Errorf("submodel %d runs at %v Hz, not %v", i, r, s.rate)
		case rate < 0:
			rate = r
		}
		if active == nil && size < *sm.MaxValue {
			active = net
		}
		last = net
	}
	if top := *c.Submodels[len(c.Submodels)-1].MaxValue; top < 1 {
		return nil, 0, fmt.Errorf("the last submodel's max_value %v is below 1", top)
	}
	if active == nil {
		active = last
	}
	return active, rate, nil
}

// containerSizes returns the sizes at which a container switches models.
func containerSizes(s spec) ([]float64, error) {
	var c struct {
		Submodels []struct {
			MaxValue float64 `json:"max_value"`
		} `json:"submodels"`
	}
	if err := json.Unmarshal(s.config, &c); err != nil {
		return nil, err
	}
	var sizes []float64
	for _, sm := range c.Submodels[:len(c.Submodels)-1] {
		sizes = append(sizes, sm.MaxValue)
	}
	return sizes, nil
}
