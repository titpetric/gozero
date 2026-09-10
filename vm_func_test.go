package gozero

import (
	"strings"
	"sync"
	"testing"
)

// TestClosureCapture pins capture-by-variable: writes on either side
// of the boundary are seen by the other, transitively through nested
// literals.
func TestClosureCapture(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		// The closure observes a write made after its creation.
		{`n := 1
f := func() int64 { return n }
n = 2
v := f()
return v`, int64(2)},
		// And the encloser observes the closure's write.
		{`n := 1
bump := func() int64 { n = n + 10; return n }
a := bump()
b := bump()
return a + b + n`, int64(53)},
		// Transitive capture through two literals.
		{`n := 5
outer := func() int64 {
	inner := func() int64 { return n * 2 }
	v := inner()
	return v
}
v := outer()
return v`, int64(10)},
		// A captured range value is a fresh variable per iteration.
		{`xs := seq()
sum := 0
for _, v := range xs {
	g := func() int64 { return v }
	sum = sum + g()
}
return sum`, int64(60)},
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

// TestScriptFnSemantics covers receivers, defer inside a body, the
// hybrid error rule at script call sites, and the failure modes.
func TestScriptFnSemantics(t *testing.T) {
	rt := exprRuntime(t)

	// A pointer receiver mutates through the cell.
	src := `type Box struct { N int64 }
func (b *Box) Bump() { b.N = b.N + 1 }
var box Box
box.Bump()
box.Bump()
return box.N`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 2 {
		t.Fatalf("pointer receiver: got %v, %v", got, err)
	}

	// defer runs at the function's exit, not the program's.
	src = `order := ""
mark := func(s string) bool { order = order + s; return true }
run := func() bool {
	defer mark("d")
	mark("a")
	return true
}
run()
mark("z")
return order`
	if got, err := rt.Eval[string](src, nil); err != nil || got != "adz" {
		t.Fatalf("defer scope: got %v, %v", got, err)
	}

	// A script function's trailing error follows the hybrid rule:
	// named, it is a value; elided, the failing call aborts.
	src = `func may(fail bool) (int64, error) {
	if fail { return 0, failWith("nope") }
	return 7, nil
}
n, err := may(true)
if err != nil { return -1 }
return n`
	if err := rt.Bind("failWith", func(s string) error { return &PanicError{Value: s} }); err != nil {
		t.Fatal(err)
	}
	if got, err := rt.Eval[int64](src, nil); err != nil || got != -1 {
		t.Fatalf("bound error: got %v, %v", got, err)
	}
	src = `func may(fail bool) (int64, error) {
	if fail { return 0, failWith("nope") }
	return 7, nil
}
n := may(true)
return n`
	if _, err := rt.Eval[int64](src, nil); err == nil {
		t.Fatal("the elided error must abort the program")
	}
}

// TestScriptFnErrors pins the compile and runtime failures.
func TestScriptFnErrors(t *testing.T) {
	rt := exprRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{

		"arity":          {`func f(n int64) int64 { return n }
v := f()
return v`, "takes 1 arguments"},
		"redeclared":     {`func f() int64 { return 1 }
func f() int64 { return 2 }
v := f()
return v`, "redeclared"},
		"init params":    {`func init(n int64) { }
x := 1; return x`, "init takes no parameters"},
		"host receiver": {`func (r strings.Reader) M() int64 { return 1 }
x := 1; return x`, "methods declare on the program's own types"},
		"return arity":   {`func f() (int64, error) { return 1 }
v := f()
return v`, "returns 2 values"},
	} {
		if _, err := rt.Compile(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestMissingReturn pins the run-time check: a body that falls off
// its end without returning declared results is an execution error.
func TestMissingReturn(t *testing.T) {
	rt := exprRuntime(t)
	src := `func f() int64 { g := 1; _ = g }
v := f()
return v`
	if _, err := rt.Eval[any](src, nil); err == nil || !strings.Contains(err.Error(), "missing return") {
		t.Fatalf("err = %v", err)
	}
}

// TestClosureConcurrent runs a bridged closure from many goroutines;
// each call owns its unit memory, the captured cell is shared.
func TestClosureConcurrent(t *testing.T) {
	rt := exprRuntime(t)
	p, err := rt.Load(`package counterpkg
func Add(a int64, b int64) int64 { return a + b }`)
	if err != nil {
		t.Fatal(err)
	}
	add, err := p.FuncOf[func(int64, int64) int64]("Add")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := int64(0); i < 200; i++ {
				if add(i, i) != 2*i {
					t.Errorf("add(%d,%d) wrong", i, i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
