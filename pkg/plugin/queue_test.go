package plugin

import (
	"sync"
	"testing"
)

func TestMPSCQueueOrderAndCapacity(t *testing.T) {
	q := newMPSCQueue[int](4)
	if _, ok := q.pop(); ok {
		t.Fatal("pop from an empty queue")
	}
	for round := range 3 { // wraps the positions around the cells
		for i := range 4 {
			if !q.push(round*10 + i) {
				t.Fatalf("round %d: push %d refused", round, i)
			}
		}
		if q.push(99) {
			t.Fatal("push into a full queue")
		}
		for i := range 4 {
			if v, ok := q.pop(); !ok || v != round*10+i {
				t.Fatalf("round %d: pop %d = %v, %v", round, i, v, ok)
			}
		}
		if _, ok := q.pop(); ok {
			t.Fatal("pop past the end")
		}
	}
}

func TestMPSCQueueConcurrentProducers(t *testing.T) {
	const producers, each = 8, 5000
	q := newMPSCQueue[[2]int](64)
	var wg sync.WaitGroup
	for p := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; {
				if q.push([2]int{p, i}) {
					i++
				}
			}
		}()
	}
	// Every producer's values arrive exactly once and in order.
	next := make([]int, producers)
	for got := 0; got < producers*each; {
		v, ok := q.pop()
		if !ok {
			continue
		}
		if v[1] != next[v[0]] {
			t.Fatalf("producer %d: got %d, want %d", v[0], v[1], next[v[0]])
		}
		next[v[0]]++
		got++
	}
	wg.Wait()
	if _, ok := q.pop(); ok {
		t.Fatal("extra value")
	}
}

func TestMPSCQueuePopDoesNotAllocate(t *testing.T) {
	q := newMPSCQueue[clapEdit](16)
	allocs := testing.AllocsPerRun(100, func() {
		q.push(clapEdit{kind: editPerform, id: 3, plain: 0.5})
		q.pop()
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func TestInstancesLookupIsLockFree(t *testing.T) {
	var r instances[int]
	if r.get(1) != nil {
		t.Fatal("lookup in an empty table")
	}
	a, b := 1, 2
	r.add(1, &a)
	r.add(2, &b)
	if r.get(1) != &a || r.get(2) != &b {
		t.Fatal("lookup after add")
	}
	// A writer holding the lock does not hold up readers.
	r.mu.Lock()
	allocs := testing.AllocsPerRun(100, func() {
		if r.get(2) != &b {
			t.Fatal("lookup while a writer holds the lock")
		}
	})
	r.mu.Unlock()
	if allocs != 0 {
		t.Fatalf("lookup allocates %v times", allocs)
	}
	if r.remove(1) != &a || r.get(1) != nil || r.get(2) != &b {
		t.Fatal("remove")
	}
	if r.remove(1) != nil {
		t.Fatal("second remove")
	}

	// Readers race writers without data races (go test -race).
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if r.get(2) != &b {
					t.Error("stable entry lost")
					return
				}
			}
		}
	}()
	for i := uintptr(10); i < 200; i++ {
		v := int(i)
		r.add(i, &v)
		r.remove(i)
	}
	close(stop)
	wg.Wait()
}
