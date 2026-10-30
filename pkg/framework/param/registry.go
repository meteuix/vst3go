package param

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// Registry manages plugin parameters. Lookups run on the audio thread, so
// they take no lock: readers load an immutable snapshot, and the rare
// writers (registration, normally while the plugin is constructed)
// publish a modified copy.
type Registry struct {
	mu    sync.Mutex // serializes writers
	state atomic.Pointer[registryState]
}

// registryState is one immutable snapshot of a Registry.
type registryState struct {
	params map[uint32]*Parameter
	dense  []*Parameter      // params by ID for IDs below denseLimit
	order  []uint32          // registration order, for indexed access
	names  map[string]uint32 // AutoRegistry name lookup
}

// denseLimit bounds the IDs looked up by index instead of through the map:
// plugins number their parameters from 0, and an index is several times
// faster than a map lookup on the audio thread.
const denseLimit = 1024

// lookup returns the parameter with id.
func (s *registryState) lookup(id uint32) (*Parameter, bool) {
	if id < uint32(len(s.dense)) {
		p := s.dense[id]
		return p, p != nil
	}
	if id < denseLimit {
		return nil, false
	}
	p, ok := s.params[id]
	return p, ok
}

// set stores p under its ID.
func (s *registryState) set(p *Parameter) {
	s.params[p.ID] = p
	if p.ID < denseLimit {
		if int(p.ID) >= len(s.dense) {
			s.dense = append(s.dense, make([]*Parameter, int(p.ID)+1-len(s.dense))...)
		}
		s.dense[p.ID] = p
	}
}

// NewRegistry creates a new parameter registry
func NewRegistry() *Registry {
	r := &Registry{}
	r.state.Store(&registryState{params: map[uint32]*Parameter{}, names: map[string]uint32{}})
	return r
}

// edit applies fn to a copy of the state and publishes it; when fn fails
// nothing changes. The caller holds r.mu.
func (r *Registry) edit(fn func(s *registryState) error) error {
	old := r.state.Load()
	s := &registryState{
		params: maps.Clone(old.params),
		dense:  slices.Clone(old.dense),
		order:  slices.Clone(old.order),
		names:  maps.Clone(old.names),
	}
	if err := fn(s); err != nil {
		return err
	}
	r.state.Store(s)
	return nil
}

// Add registers a new parameter
func (r *Registry) Add(params ...*Parameter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.edit(func(s *registryState) error {
		for _, p := range params {
			if _, exists := s.params[p.ID]; exists {
				return fmt.Errorf("parameter ID %d already exists", p.ID)
			}
			s.set(p)
			s.order = append(s.order, p.ID)
		}
		return nil
	})
}

// Get retrieves a parameter by ID
func (r *Registry) Get(id uint32) *Parameter {
	p, _ := r.state.Load().lookup(id)
	return p
}

// GetOK retrieves a parameter by ID and reports whether it exists.
func (r *Registry) GetOK(id uint32) (*Parameter, bool) {
	return r.state.Load().lookup(id)
}

// Has reports whether a parameter with the given ID exists.
func (r *Registry) Has(id uint32) bool {
	_, ok := r.GetOK(id)
	return ok
}

// GetNormalized returns the normalized value of a parameter if it exists.
func (r *Registry) GetNormalized(id uint32) (float64, bool) {
	param, ok := r.GetOK(id)
	if !ok {
		return 0, false
	}
	return param.GetNormalized(), true
}

// GetPlain returns the plain value of a parameter if it exists.
func (r *Registry) GetPlain(id uint32) (float64, bool) {
	param, ok := r.GetOK(id)
	if !ok {
		return 0, false
	}
	return param.GetPlain(), true
}

// GetByIndex retrieves a parameter by index
func (r *Registry) GetByIndex(index int32) *Parameter {
	s := r.state.Load()
	if index < 0 || index >= int32(len(s.order)) {
		return nil
	}
	return s.params[s.order[index]]
}

// Count returns the number of parameters
func (r *Registry) Count() int32 {
	return int32(len(r.state.Load().order))
}

// All returns all parameters in order
func (r *Registry) All() []*Parameter {
	s := r.state.Load()
	result := make([]*Parameter, len(s.order))
	for i, id := range s.order {
		result[i] = s.params[id]
	}
	return result
}
