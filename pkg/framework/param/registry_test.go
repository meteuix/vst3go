package param

import (
	"sync"
	"testing"
)

func TestRegistryAddRejectsDuplicateID(t *testing.T) {
	reg := NewRegistry()

	first := &Parameter{ID: 1, Name: "First"}
	second := &Parameter{ID: 1, Name: "Second"}

	if err := reg.Add(first); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}

	if err := reg.Add(second); err == nil {
		t.Fatal("Add(second) should fail on duplicate ID")
	}

	if got := reg.Count(); got != 1 {
		t.Fatalf("Count() = %d, want 1", got)
	}
}

func TestRegistrySafeAccessors(t *testing.T) {
	reg := NewRegistry()
	param := &Parameter{ID: 7, Name: "Gain", Min: -12, Max: 12}
	param.SetPlain(3)

	if err := reg.Add(param); err != nil {
		t.Fatalf("Add(param) error = %v", err)
	}

	if !reg.Has(7) {
		t.Fatal("Has(7) = false, want true")
	}

	if got, ok := reg.GetOK(7); !ok || got != param {
		t.Fatal("GetOK(7) did not return the registered parameter")
	}

	if got, ok := reg.GetNormalized(7); !ok || got != param.GetNormalized() {
		t.Fatalf("GetNormalized(7) = (%f, %v), want (%f, true)", got, ok, param.GetNormalized())
	}

	if got, ok := reg.GetPlain(7); !ok || got != 3 {
		t.Fatalf("GetPlain(7) = (%f, %v), want (3, true)", got, ok)
	}

	if _, ok := reg.GetOK(99); ok {
		t.Fatal("GetOK(99) should report missing parameter")
	}

	if _, ok := reg.GetNormalized(99); ok {
		t.Fatal("GetNormalized(99) should report missing parameter")
	}

	if _, ok := reg.GetPlain(99); ok {
		t.Fatal("GetPlain(99) should report missing parameter")
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	params := []*Parameter{
		{ID: 1, Name: "One", Min: 0, Max: 1},
		{ID: 2, Name: "Two", Min: -12, Max: 12},
		{ID: 3, Name: "Three", Min: 0, Max: 127},
	}

	for _, param := range params {
		if err := reg.Add(param); err != nil {
			t.Fatalf("Add(%d) error = %v", param.ID, err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed uint32) {
			defer wg.Done()

			for j := 0; j < 1_000; j++ {
				id := params[(int(seed)+j)%len(params)].ID
				_, _ = reg.GetOK(id)
				_, _ = reg.GetNormalized(id)
				_, _ = reg.GetPlain(id)
				_ = reg.Has(id)
				_ = reg.Get(id)
				_ = reg.GetByIndex(int32(j % len(params)))
				_ = reg.Count()
				_ = reg.All()
			}
		}(uint32(i))
	}

	wg.Wait()
}

func TestRegistryReadsTakeNoLock(t *testing.T) {
	reg := NewRegistry()
	gain := &Parameter{ID: 1, Name: "Gain"}
	if err := reg.Add(gain); err != nil {
		t.Fatal(err)
	}
	// The audio thread looks parameters up while a writer holds the lock.
	reg.mu.Lock()
	allocs := testing.AllocsPerRun(100, func() {
		if reg.Get(1) != gain || reg.GetByIndex(0) != gain || reg.Count() != 1 {
			t.Fatal("lookup while a writer holds the lock")
		}
	})
	reg.mu.Unlock()
	if allocs != 0 {
		t.Fatalf("lookups allocate %v times", allocs)
	}

	// Readers race a writer without data races (go test -race).
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
				if reg.Get(1) != gain || reg.GetByIndex(0) != gain {
					t.Error("registered parameter lost")
					return
				}
			}
		}
	}()
	for id := uint32(2); id < 200; id++ {
		if err := reg.Add(&Parameter{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestRegistryFailedAddChangesNothing(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Add(&Parameter{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(&Parameter{ID: 2}, &Parameter{ID: 1}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if reg.Count() != 1 || reg.Has(2) {
		t.Fatalf("failed Add left %d parameters", reg.Count())
	}
}
