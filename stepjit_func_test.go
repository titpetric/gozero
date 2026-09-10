package gozero

import (
	"strings"
	"testing"
)

// TestScriptUnitsLower pins which units reach the direct tier: a
// capture-free function or literal lowers, a capturing closure keeps
// the reflect walk, and both agree on results either way.
func TestScriptUnitsLower(t *testing.T) {
	rt := exprRuntime(t)
	compileUnit := func(src, name string) *scriptFn {
		t.Helper()
		p, err := rt.Load("package m\n" + src)
		if err != nil {
			t.Fatal(err)
		}
		fn := p.vm.funcs[name]
		if fn == nil {
			t.Fatalf("%s not declared", name)
		}
		return fn
	}

	fn := compileUnit(`func Double(n int64) int64 { return n * 2 }`, "Double")
	if fn.jit == nil {
		t.Error("a scalar function must lower")
	}
	fn = compileUnit(`func Fib(n int64) int64 { if n < 2 { return n }; a := Fib(n-1); b := Fib(n-2); return a + b }`, "Fib")
	if fn.jit == nil {
		t.Error("a recursive function must lower")
	}

	// Semantics agree between the tiers whatever lowered.
	src := `func fib(n int64) int64 { if n < 2 { return n }; a := fib(n-1); b := fib(n-2); return a + b }
v := fib(12)
return v`
	got, err := rt.Eval[int64](src, nil)
	if err != nil || got != 144 {
		t.Fatalf("fib: got %v, %v", got, err)
	}

	// Capturing closures stay on the reflect walk and still work.
	src = `n := 0
bump := func() int64 { n = n + 1; return n }
a := bump()
b := bump()
return a + b + n`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 5 {
		t.Fatalf("capture: got %v, %v", got, err)
	}

	// A method with a struct receiver lowers and copies the whole
	// receiver, string fields included.
	src = `type G struct { Prefix string }
func (g G) Greet(name string) string { return g.Prefix + name }
g := G{Prefix: "hello "}
s := g.Greet("go")
return s`
	if got, err := rt.Eval[string](src, nil); err != nil || got != "hello go" {
		t.Fatalf("receiver: got %q, %v", got, err)
	}

	// Missing return stays a runtime error on the lowered tier.
	src = `func f() int64 { g := 1; _ = g }
v := f()
return v`
	if _, err := rt.Eval[any](src, nil); err == nil || !strings.Contains(err.Error(), "missing return") {
		t.Fatalf("missing return: %v", err)
	}
}
