package analysis

import (
	"sync"
	"sync/atomic"
)

// published hands readings from the goroutine that runs Process (the
// audio thread) to readers on any goroutine by triple buffering: the
// writer fills its back buffer and swaps it with the middle one, and a
// reader swaps the middle one for its front buffer when it holds newer
// readings. The writer never waits; readers serialize among themselves.
type published[T any] struct {
	bufs   [3]T
	back   int           // writer's buffer
	middle atomic.Uint32 // index of the exchange buffer, plus freshBit when it is newer than front
	readMu sync.Mutex
	front  int // readers' buffer, guarded by readMu
}

const freshBit = 4

// init prepares the three buffers with prepare (to size slices) and
// assigns them.
func (p *published[T]) init(prepare func(*T)) {
	for i := range p.bufs {
		prepare(&p.bufs[i])
	}
	p.back, p.front = 0, 2
	p.middle.Store(1)
}

// write returns the writer's buffer to fill; it holds older readings.
func (p *published[T]) write() *T { return &p.bufs[p.back] }

// publish makes the writer's buffer the newest readings.
func (p *published[T]) publish() {
	old := p.middle.Swap(uint32(p.back) | freshBit)
	p.back = int(old &^ freshBit)
}

// read calls fn with the newest published readings. fn must not keep the
// pointer.
func (p *published[T]) read(fn func(*T)) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if p.middle.Load()&freshBit != 0 {
		old := p.middle.Swap(uint32(p.front))
		p.front = int(old &^ freshBit)
	}
	fn(&p.bufs[p.front])
}

// get returns a copy of the newest readings; use it for readings without
// slices.
func (p *published[T]) get() T {
	var v T
	p.read(func(t *T) { v = *t })
	return v
}
