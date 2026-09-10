package gozero

import (
	"fmt"
	"net/url"
	"sync"
	"testing"
)

// poolRuntime binds one NonRetaining formatter and one retaining
// collector, so the tests can hold the two promises against each
// other.
func poolRuntime(t *testing.T) (*Runtime, *[][]any) {
	t.Helper()
	rt := NewRuntime()
	if err := rt.Bind("format", fmt.Sprint, NonRetaining()); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("describe", func(v any) string { return fmt.Sprintf("%v", v) }, NonRetaining()); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("url.Parse", url.Parse); err != nil {
		t.Fatal(err)
	}
	var kept [][]any
	if err := rt.Bind("keep", func(vs ...any) int {
		kept = append(kept, vs)
		return len(kept)
	}); err != nil {
		t.Fatal(err)
	}
	return rt, &kept
}

// TestArgumentPools checks the pooled kinds end to end: the pack
// slice and string box of a NonRetaining call are reused between runs
// and every run still computes the right value, sequentially and
// concurrently under the race detector.
func TestArgumentPools(t *testing.T) {
	rt, _ := poolRuntime(t)

	// A spliced string result boxes for the any element of format's
	// pack; both the pack backing and the box come from pools after
	// the first run.
	src := `u := url.Parse("https://example.com/p"); s := format("path=", u.Path); return s`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("not on the direct tier: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		v, err := fn.Exec[string](nil)
		if err != nil || v != "path=/p" {
			t.Fatalf("run %d: v = %q, err = %v", i, v, err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if v, err := fn.Exec[string](nil); err != nil || v != "path=/p" {
					t.Errorf("v = %q, err = %v", v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestPooledLiteralBlock checks a composite literal handed to a
// NonRetaining callee: the block is pooled, and every run sees its
// own fill, not a previous run's.
func TestPooledLiteralBlock(t *testing.T) {
	rt, _ := poolRuntime(t)
	fn, err := rt.Compile(`s := describe(&url.URL{Path: "/block"}); return s`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		v, err := fn.Exec[string](nil)
		if err != nil || v != "/block" {
			t.Fatalf("run %d: v = %q, err = %v", i, v, err)
		}
	}
}

// TestRetainingBindingUnpooled pins the safety boundary: a binding
// without the annotation keeps fresh allocations, so a slice it
// retains is never overwritten by a later run.
func TestRetainingBindingUnpooled(t *testing.T) {
	rt, kept := poolRuntime(t)
	fn, err := rt.Compile(`u := url.Parse("https://example.com/r"); n := keep("first", u.Path); return n`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fn.Exec[int](nil); err != nil {
			t.Fatal(err)
		}
	}
	for i, vs := range *kept {
		if len(vs) != 2 || vs[0] != "first" || vs[1] != "/r" {
			t.Fatalf("retained slice %d corrupted: %v", i, vs)
		}
	}
}

// TestPoolSitesPlanned pins the plan itself: the annotated program
// has sites, the unannotated one has none.
func TestPoolSitesPlanned(t *testing.T) {
	rt, _ := poolRuntime(t)
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
	pooled := compile(`u := url.Parse("https://example.com/p"); s := format("path=", u.Path); return s`)
	if len(pooled.sites) == 0 {
		t.Error("annotated call planned no pool sites")
	}
	plain := compile(`u := url.Parse("https://example.com/p"); n := keep("path=", u.Path); return n`)
	if len(plain.sites) != 0 {
		t.Errorf("unannotated call planned %d pool sites", len(plain.sites))
	}
}
