package gozero

import (
	"net/http"
	"net/url"
	"testing"
)

// TestStructNodeAllocatesPerEvaluation pins the direct tier's literal
// allocation: two runs of one compiled &T{} must not share the struct.
// The same property is checked through Compile in TestStructLiteral;
// this one requires the JIT and runs its tier directly, so a fallback
// cannot make it pass by allocating through reflect.
func TestStructNodeAllocatesPerEvaluation(t *testing.T) {
	rt, _ := typeRuntime(t)
	const src = `r := &http.Request{Method: "X"}; return r;`
	jit, _ := compilePair(t, rt, src)
	run := func() *http.Request {
		t.Helper()
		v, err := jit(t.Context(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		r, ok := v.(*http.Request)
		if !ok {
			t.Fatalf("returned %T, want *http.Request", v)
		}
		return r
	}
	a, b := run(), run()
	if a == b {
		t.Fatal("two runs returned the same *http.Request")
	}
	a.Method = "mutated"
	if b.Method != "X" {
		t.Errorf("mutating one run's value reached the other: Method = %q", b.Method)
	}
}

// shapeInner and shapeOuter give the tests a struct-typed field, which
// the standard library types used elsewhere do not offer with exported
// fields at both levels.
type shapeInner struct {
	N int64
	S string
}

type shapeOuter struct {
	Label string
	In    shapeInner
}

// TestStructLiteralNestedValueField checks a nested value literal on
// both tiers: its fields write in place at summed offsets on the JIT,
// and through reflect on the fallback, and the callee must see the
// same value either way.
func TestStructLiteralNestedValueField(t *testing.T) {
	rt := NewRuntime()
	var seen any
	if err := rt.Bind("takes", func(v any) (*url.URL, error) { seen = copyBoxed(v); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindType("shape.Inner", shapeInner{}); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindType("shape.Outer", shapeOuter{}); err != nil {
		t.Fatal(err)
	}
	const src = `w := shape.Outer{Label: "x", In: shape.Inner{N: 7, S: "s"}}; takes(w);`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a nested value literal should JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	want := shapeOuter{Label: "x", In: shapeInner{N: 7, S: "s"}}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if got, ok := seen.(shapeOuter); !ok || got != want {
		t.Errorf("callee saw %#v, want %#v", seen, want)
	}
}

// TestStructLiteralValueIntoAny checks that a value literal handed to
// an any parameter arrives as the value type, on the JIT tier: the
// interface's data word points at the fresh struct the literal built,
// with the value's own itab rather than the pointer's.
func TestStructLiteralValueIntoAny(t *testing.T) {
	rt := NewRuntime()
	var seen any
	if err := rt.Bind("takes", func(v any) (*url.URL, error) { seen = copyBoxed(v); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindType("shape.Inner", shapeInner{}); err != nil {
		t.Fatal(err)
	}
	const src = `takes(shape.Inner{N: 3, S: "v"});`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a value literal into any should JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if got, ok := seen.(shapeInner); !ok || got != (shapeInner{N: 3, S: "v"}) {
		t.Errorf("callee saw %#v, want shapeInner{N: 3, S: %q}", seen, "v")
	}
}
