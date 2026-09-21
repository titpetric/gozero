package gozero

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestFuncLitDirect pins the closure-table path: the whole program,
// literal included, compiles to the direct tier, and calling the
// materialized handler allocates nothing beyond what the body's own
// bindings allocate, which for this body is nothing.
func TestFuncLitDirect(t *testing.T) {
	rt := funclitRuntime(t)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		t.Fatal(err)
	}
	const src = `keep(func(w, r) { fmt.Fprint(w, "ok") });`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the literal should reach the closure table: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	w := &benchResponseWriter{}
	req := httptest.NewRequest("GET", "/health", nil)
	handler(w, req)
	if w.n != 2 {
		t.Fatalf("the handler wrote %d bytes, want 2", w.n)
	}
	if raceDetector {
		// The body takes its frame, its pack slice and its string box
		// from pools, and the race detector's sync.Pool drops one Put
		// in four at random: the call then measures around 0.95
		// allocations, which AllocsPerRun's integer division reports
		// as 0 or 1 depending on the draw. The pin holds in every
		// other build.
		return
	}
	if n := testing.AllocsPerRun(200, func() { handler(w, req) }); n != 0 {
		t.Fatalf("the direct closure allocates %.0f per call, want 0", n)
	}
}

// TestFuncLitLateCalls hands the materialized value to a binding that
// keeps it, the HandleFunc lifetime, and calls it after the defining
// run finished: repeatedly, and from both tiers, with the same
// effects observed through the recorder each time.
func TestFuncLitLateCalls(t *testing.T) {
	rt := funclitRuntime(t)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		t.Fatal(err)
	}
	const src = `keep(func(w, r) { fmt.Fprint(w, "ok ", r.URL.Path) });`
	jit, slow := funclitPair(t, rt, src)
	req := httptest.NewRequest("GET", "/late", nil)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		handler = nil
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if handler == nil {
			t.Fatalf("%s: the binding saw no handler", name)
		}
		for i := 0; i < 3; i++ {
			rec := httptest.NewRecorder()
			handler(rec, req)
			if got := rec.Body.String(); got != "ok /late" {
				t.Fatalf("%s call %d: got %q", name, i, got)
			}
		}
	}
}

// TestFuncLitConcurrent calls the direct closure from many goroutines
// at once: the body's frame comes from a pool, and every invocation
// must see its own.
func TestFuncLitConcurrent(t *testing.T) {
	rt := funclitRuntime(t)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile(`keep(func(w, r) { fmt.Fprint(w, "ok ", r.URL.Path) });`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/c", nil)
			for j := 0; j < 200; j++ {
				rec := httptest.NewRecorder()
				handler(rec, req)
				if got := rec.Body.String(); got != "ok /c" {
					t.Errorf("got %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
