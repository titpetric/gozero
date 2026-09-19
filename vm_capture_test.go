package gozero

import (
	"sync"
	"testing"
)

// closureRuntime is funclitRuntime plus the bindings the capture
// tests share: keep stores a closure for calls after the run, call
// invokes one during it, sink records what a closure saw.
type closureRecorder struct {
	mu  sync.Mutex
	got []string
}

func (r *closureRecorder) sink(s string) {
	r.mu.Lock()
	r.got = append(r.got, s)
	r.mu.Unlock()
}

func (r *closureRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.got
	r.got = nil
	return out
}

func closureRuntime(tb testing.TB) (*Runtime, *closureRecorder, *func()) {
	tb.Helper()
	rt := funclitRuntime(tb)
	rec := &closureRecorder{}
	held := new(func())
	if err := rt.Bind("keep", func(f func()) { *held = f }); err != nil {
		tb.Fatal(err)
	}
	if err := rt.Bind("call", func(f func()) { f() }); err != nil {
		tb.Fatal(err)
	}
	if err := rt.Bind("sink", rec.sink); err != nil {
		tb.Fatal(err)
	}
	return rt, rec, held
}

// TestClosureWriteThrough pins the cell semantics on both tiers, in
// both directions and across the run boundary: the closure sees the
// enclosing program's later writes, the enclosing program sees the
// closure's, and a call after the run reads the value the run left.
func TestClosureWriteThrough(t *testing.T) {
	rt, rec, held := closureRuntime(t)
	const src = `
		x := fmt.Sprint("first");
		keep(func() { sink(x) });
		call(func() { sink(x); x = "written-inside" });
		sink(x);
		x = "second";
	`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		*held = nil
		rec.take()
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// During the run: the second literal read the first write, its
		// own write was seen by the sink after it.
		if got := rec.take(); len(got) != 2 || got[0] != "first" || got[1] != "written-inside" {
			t.Fatalf("%s: during the run sink saw %v, want [first written-inside]", name, got)
		}
		// After the run: the kept closure reads the final value, three
		// calls read it three times.
		for i := 0; i < 3; i++ {
			(*held)()
		}
		if got := rec.take(); len(got) != 3 || got[0] != "second" || got[1] != "second" || got[2] != "second" {
			t.Fatalf("%s: after the run sink saw %v, want [second second second]", name, got)
		}
	}
}

// TestClosureSharedCell pins that two literals capturing the same
// name share one cell: what one writes after the run, the other
// reads, on both tiers.
func TestClosureSharedCell(t *testing.T) {
	rt, rec, held := closureRuntime(t)
	writer := new(func())
	if err := rt.Bind("keep2", func(f func()) { *writer = f }); err != nil {
		t.Fatal(err)
	}
	const src = `
		x := fmt.Sprint("start");
		keep(func() { sink(x) });
		keep2(func() { x = "poked" });
	`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		*held, *writer = nil, nil
		rec.take()
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		(*held)()
		(*writer)()
		(*held)()
		if got := rec.take(); len(got) != 2 || got[0] != "start" || got[1] != "poked" {
			t.Fatalf("%s: sink saw %v, want [start poked]", name, got)
		}
	}
}

// TestClosureGoroutines calls a capturing closure from many
// goroutines after the run: every call reads the same cell, and the
// race detector watches the frame the closure escaped with.
func TestClosureGoroutines(t *testing.T) {
	rt, rec, held := closureRuntime(t)
	const src = `
		x := fmt.Sprint("shared");
		keep(func() { sink(x) });
	`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		*held = nil
		rec.take()
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					(*held)()
				}
			}()
		}
		wg.Wait()
		got := rec.take()
		if len(got) != 400 {
			t.Fatalf("%s: %d calls recorded, want 400", name, len(got))
		}
		for _, s := range got {
			if s != "shared" {
				t.Fatalf("%s: a goroutine read %q, want shared", name, s)
			}
		}
	}
}

// TestClosureFieldWrite writes a field of a captured pointer inside
// the body; the enclosing program reads it back after serving, on
// both tiers.
func TestClosureFieldWrite(t *testing.T) {
	rt, rec, _ := closureRuntime(t)
	const src = `
		req := httptest.NewRequest("GET", "/before");
		call(func() { req.Host = "poked.example.com" });
		sink(req.Host);
	`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		rec.take()
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := rec.take(); len(got) != 1 || got[0] != "poked.example.com" {
			t.Fatalf("%s: sink saw %v, want [poked.example.com]", name, got)
		}
	}
}

// TestClosureTransitive captures across two literal boundaries: the
// innermost body reads a name of the top-level program, the cell
// travelling through the literal in between. The direct tier declines
// a capture of a captured name by design; the program still runs on
// the reflect evaluator and Supports names the reason.
func TestClosureTransitive(t *testing.T) {
	rt, rec, _ := closureRuntime(t)
	const src = `
		x := fmt.Sprint("deep");
		call(func() { call(func() { sink(x) }) });
		x = "after";
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 1 || got[0] != "deep" {
		t.Fatalf("sink saw %v, want [deep]", got)
	}
	err = rt.Supports(src)
	if err == nil {
		t.Fatal("Supports should name the capture-of-a-capture decline")
	}
}
