package plugin

import "sync/atomic"

// mpscQueue is a bounded lock-free queue for many producers and one
// consumer (D. Vyukov's bounded queue). The consumer never waits for a
// producer, so the audio thread can drain it; a Go channel would take the
// channel's lock on every receive.
type mpscQueue[T any] struct {
	cells []mpscCell[T]
	mask  uint64
	_     [56]byte // keep the producer and consumer positions apart
	enq   atomic.Uint64
	_     [56]byte
	deq   atomic.Uint64
}

type mpscCell[T any] struct {
	seq   atomic.Uint64
	value T
}

// newMPSCQueue returns a queue holding size values (a power of two).
func newMPSCQueue[T any](size int) *mpscQueue[T] {
	if size < 2 || size&(size-1) != 0 {
		panic("mpscQueue: size must be a power of two")
	}
	q := &mpscQueue[T]{cells: make([]mpscCell[T], size), mask: uint64(size - 1)}
	for i := range q.cells {
		q.cells[i].seq.Store(uint64(i))
	}
	return q
}

// push adds v and reports false when the queue is full. Safe from any
// goroutine.
func (q *mpscQueue[T]) push(v T) bool {
	pos := q.enq.Load()
	for {
		c := &q.cells[pos&q.mask]
		switch d := int64(c.seq.Load() - pos); {
		case d == 0:
			if q.enq.CompareAndSwap(pos, pos+1) {
				c.value = v
				c.seq.Store(pos + 1)
				return true
			}
			pos = q.enq.Load()
		case d < 0:
			return false
		default:
			pos = q.enq.Load()
		}
	}
}

// pop removes the oldest value. Only one goroutine may pop at a time.
func (q *mpscQueue[T]) pop() (T, bool) {
	pos := q.deq.Load()
	c := &q.cells[pos&q.mask]
	if c.seq.Load() != pos+1 {
		var zero T
		return zero, false
	}
	v := c.value
	c.seq.Store(pos + q.mask + 1)
	q.deq.Store(pos + 1)
	return v, true
}
