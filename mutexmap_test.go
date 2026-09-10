package gozero

import (
	"sync"
	"testing"
)

// TestMutexMap checks the map through the language: bound behind a
// constructor, written and read on the direct tier, shared between
// concurrent runs of the same compiled program under the race
// detector.
func TestMutexMap(t *testing.T) {
	rt := NewRuntime()
	shared := NewMutexMap()
	if err := rt.Bind("store", func() *MutexMap { return shared }); err != nil {
		t.Fatal(err)
	}

	src := `m := store(); m.Set("k", 42); v := m.Get("k"); return v`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("not on the direct tier: %v", err)
	}
	v, err := rt.Eval[int](src, nil)
	if err != nil || v != 42 {
		t.Fatalf("v = %v, err = %v; want 42", v, err)
	}

	// One compiled program, many goroutines, one shared map: the
	// binding is the synchronization.
	fn, err := rt.Compile(`m := store(); m.Set("g", 7); v := m.Get("g"); return v`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if v, err := fn.Exec[int](nil); err != nil || v != 7 {
					t.Errorf("v = %v, err = %v", v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := shared.Get("g"); got != 7 {
		t.Fatalf("shared map holds %d, want 7", got)
	}
}

// TestMutexMap_Set covers the write path directly.
func TestMutexMap_Set(t *testing.T) {
	m := NewMutexMap()
	m.Set("a", 1)
	m.Set("a", 2)
	if got := m.Get("a"); got != 2 {
		t.Fatalf("Get(a) = %d, want 2", got)
	}
}

// TestMutexMap_Get covers the read path, including a missing key.
func TestMutexMap_Get(t *testing.T) {
	m := NewMutexMap()
	if got := m.Get("missing"); got != 0 {
		t.Fatalf("Get(missing) = %d, want 0", got)
	}
	m.Set("b", 9)
	if got := m.Get("b"); got != 9 {
		t.Fatalf("Get(b) = %d, want 9", got)
	}
}
