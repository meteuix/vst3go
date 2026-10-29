package plugin

import (
	"maps"
	"sync"
	"sync/atomic"
)

// instances maps the integer handles C holds to Go instances. Lookups run
// on every host call, including the audio thread, so they take no lock:
// readers load an immutable snapshot, and the rare writers (instance
// creation and destruction) publish a modified copy.
type instances[T any] struct {
	mu   sync.Mutex // serializes writers
	snap atomic.Pointer[map[uintptr]*T]
}

func (r *instances[T]) get(id uintptr) *T {
	m := r.snap.Load()
	if m == nil {
		return nil
	}
	return (*m)[id]
}

func (r *instances[T]) add(id uintptr, v *T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[uintptr]*T{}
	if old := r.snap.Load(); old != nil {
		m = maps.Clone(*old)
	}
	m[id] = v
	r.snap.Store(&m)
}

// remove deletes id and returns what it mapped to.
func (r *instances[T]) remove(id uintptr) *T {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.snap.Load()
	if old == nil {
		return nil
	}
	v, ok := (*old)[id]
	if !ok {
		return nil
	}
	m := maps.Clone(*old)
	delete(m, id)
	r.snap.Store(&m)
	return v
}
