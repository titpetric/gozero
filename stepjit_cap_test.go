package gozero

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClosureDirect pins the capturing closure-table path: the whole
// program, capture included, compiles to the direct tier, the
// materialized handler observes the captured cell, and a call pays
// exactly one allocation, the box the captured string converts
// through for fmt.Fprint's interface pack element, which is the
// same conversion allocation the native closure pays.
func TestClosureDirect(t *testing.T) {
	rt := funclitRuntime(t)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		t.Fatal(err)
	}
	const src = `greeting := fmt.Sprint("hello"); keep(func(w, r) { fmt.Fprint(w, greeting) });`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the capturing literal should reach the closure table: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	w := &benchResponseWriter{}
	req := httptest.NewRequest("GET", "/greet", nil)
	handler(w, req)
	if w.n != 5 {
		t.Fatalf("the handler wrote %d bytes, want 5", w.n)
	}
	if raceEnabled {
		t.Log("race detector on: the exact allocation assertions are skipped, its runtime allocates per call")
		return
	}

	greeting := fmt.Sprint("hello")
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, greeting)
	})
	got := testing.AllocsPerRun(200, func() { handler(w, req) })
	want := testing.AllocsPerRun(200, func() { native(w, req) })
	t.Logf("closure call: %.0f allocations, native %.0f", got, want)
	if got != 1 {
		t.Fatalf("the direct capturing closure allocates %.0f per call, want 1 (the captured string's interface box)", got)
	}
	if got > want {
		t.Fatalf("the direct capturing closure allocates %.0f per call, more than the native closure's %.0f", got, want)
	}

	// Construction cost per run, against the same program without the
	// capture: the difference is the escaped frame that pooling no
	// longer recycles plus the per-run closure, the design doc's two
	// stated costs. The twin keeps the Sprint statement so its result
	// allocation cancels out of the delta.
	free, err := rt.Compile(`greeting := fmt.Sprint("hello"); keep(func(w, r) { fmt.Fprint(w, "hello") });`)
	if err != nil {
		t.Fatal(err)
	}
	capRuns := testing.AllocsPerRun(200, func() {
		if _, err := fn.Exec[any](nil); err != nil {
			panic(err)
		}
	})
	freeRuns := testing.AllocsPerRun(200, func() {
		if _, err := free.Exec[any](nil); err != nil {
			panic(err)
		}
	})
	t.Logf("per-run construction: capturing %.0f allocations, capture-free %.0f", capRuns, freeRuns)
	if capRuns-freeRuns != 2 {
		t.Fatalf("capture costs %.0f allocations per run over capture-free, want exactly 2: the unpooled frame and the closure", capRuns-freeRuns)
	}
}

// TestClosurePoolOff pins the design doc's frame cost directly: a
// program whose literal captures loses its frame pool, because the
// closure escapes with the frame, while its capture-free twin keeps
// pooling.
func TestClosurePoolOff(t *testing.T) {
	rt := funclitRuntime(t)
	if err := rt.Bind("keep", func(h http.HandlerFunc) {}); err != nil {
		t.Fatal(err)
	}
	compile := func(src string) *jitProgram {
		t.Helper()
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		p, err := rt.compiler.compileProgram(prog)
		if err != nil {
			t.Fatal(err)
		}
		jp, err := jitCompileProgram(p)
		if err != nil {
			t.Fatal(err)
		}
		return jp
	}
	capturing := compile(`greeting := fmt.Sprint("hello"); keep(func(w, r) { fmt.Fprint(w, greeting) });`)
	free := compile(`greeting := fmt.Sprint("hello"); keep(func(w, r) { fmt.Fprint(w, "x") });`)
	if capturing.pool != nil {
		t.Fatal("a capturing program must not pool its frame: the closure escaped with it")
	}
	if free.pool == nil {
		t.Fatal("the capture-free twin should keep its frame pool")
	}
}
