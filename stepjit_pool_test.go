package gozero

import (
	"fmt"
	"net/url"
	"sync"
	"testing"
)

// poolRuntime binds a formatter and a collector. The collector
// follows the binding contract: its arguments are borrowed, so it
// copies what it keeps.
func poolRuntime(t *testing.T) (*Runtime, *[][]any) {
	t.Helper()
	rt := NewRuntime()
	if err := rt.Bind("format", fmt.Sprint); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("describe", func(v any) string { return fmt.Sprintf("%v", v) }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("url.Parse", url.Parse); err != nil {
		t.Fatal(err)
	}
	var kept [][]any
	if err := rt.Bind("keep", func(vs ...any) int {
		// vs and its elements are borrowed: the slice is cloned and
		// each element re-boxed by the copy, per the contract in
		// Bind's documentation.
		own := make([]any, len(vs))
		for i, v := range vs {
			own[i] = fmt.Sprint(v)
		}
		kept = append(kept, own)
		return len(kept)
	}); err != nil {
		t.Fatal(err)
	}
	return rt, &kept
}

// TestArgumentPools checks the pooled kinds end to end: the pack
// slice and string box of a call are reused between runs and every
// run still computes the right value, sequentially and concurrently
// under the race detector.
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

// TestPooledLiteralBlock checks a composite literal in argument
// position: the block is pooled, and every run sees its own fill,
// not a previous run's.
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

// TestBindingContractCopy pins the contract from the caller's side:
// a binding that copies its borrowed arguments observes stable
// values across later runs of the same program.
func TestBindingContractCopy(t *testing.T) {
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
			t.Fatalf("copied slice %d corrupted: %v", i, vs)
		}
	}
}

// TestPoolSitesPlanned pins the plan: a program with packs and boxes
// has pool sites, and a program whose arguments are all constants
// and plain values has none.
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
		t.Error("a pack-and-box program planned no pool sites")
	}
	plain := compile(`u := url.Parse("https://example.com/p"); return u`)
	if len(plain.sites) != 0 {
		t.Errorf("a constant-argument program planned %d pool sites", len(plain.sites))
	}
}
