package gozero

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func scriptRuntime(t *testing.T) (*Runtime, *any) {
	t.Helper()
	rt := NewRuntime()
	var seen any
	for name, fn := range map[string]any{
		"wantAny": func(v any) (string, error) { seen = v; return "ok", nil },
		"wantI64": func(n int64) (string, error) { seen = n; return "ok", nil },
		"encode":  func(v any) (string, error) { b, err := json.Marshal(v); return string(b), err },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &seen
}

// TestScriptStructDecl covers the whole stage-one surface end to end:
// a declared struct built with reflect.StructOf, composite literals
// over it, field reads and writes, var declarations, and json output
// shaped by the declared tags.
func TestScriptStructDecl(t *testing.T) {
	rt, seen := scriptRuntime(t)

	src := `type Point struct {
	X int64 ` + "`json:\"x\"`" + `
	Y int64 ` + "`json:\"y\"`" + `
}
p := Point{X: 3, Y: 4}
wantI64(p.X)
p.X = 5
wantI64(p.X)
return encode(p)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn.Exec[string](nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"x":5,"y":4}` {
		t.Errorf("encoded = %s", got)
	}
	if *seen != int64(5) {
		t.Errorf("callee saw %v", *seen)
	}

	// The pointer form, a var declaration, and compound spellings over
	// the declared name.
	for _, tc := range []struct {
		src  string
		want string
	}{
		{`type P struct { N int64 }; q := &P{N: 1}; return encode(q)`, `{"N":1}`},
		{`type P struct { N int64 }; var w P; w.N = 7; return encode(w)`, `{"N":7}`},
		{`type P struct { N int64 }; var w *P; return encode(w)`, `null`},
		{`type P struct { N int64 }; var xs []P; return encode(xs)`, `null`},
		{`type P struct { N int64 }; var m map[string]P; return encode(m)`, `null`},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		got, err := fn.Exec[string](nil)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v", tc.src, got, err)
		}
	}
}

// TestScriptStructForwardRef checks declaration order does not
// matter, the way it does not in Go.
func TestScriptStructForwardRef(t *testing.T) {
	rt, _ := scriptRuntime(t)
	src := `type Outer struct {
	In Inner
}
type Inner struct { N int64 }
o := Outer{In: Inner{N: 9}}
return encode(o)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn.Exec[string](nil)
	if err != nil || got != `{"In":{"N":9}}` {
		t.Errorf("got %q, %v", got, err)
	}
}

// TestScriptStructEmbedded checks data promotion through an embedded
// script struct: the field reads through the implicit name.
func TestScriptStructEmbedded(t *testing.T) {
	rt, seen := scriptRuntime(t)
	src := `type Base struct { N int64 }
type Wrap struct {
	Base
	M int64
}
w := Wrap{Base: Base{N: 3}, M: 4}
wantI64(w.N)
return encode(w)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json inlines an embedded struct's fields, as it does
	// for a named Go type.
	got, err := fn.Exec[string](nil)
	if err != nil || got != `{"N":3,"M":4}` {
		t.Errorf("got %q, %v", got, err)
	}
	if *seen != int64(3) {
		t.Errorf("promoted read saw %v", *seen)
	}
}

// TestScriptStructErrors pins the compile errors: recursion,
// unknown field types, duplicates, shadowing, embedding a type with
// methods, unexported fields, and the declared name appearing in a
// mismatch instead of the anonymous struct spelling.
func TestScriptStructErrors(t *testing.T) {
	rt, _ := scriptRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"recursive":      {`type X struct { Next *X }; f()`, "undefined or recursive"},
		"mutual":         {`type A struct { B B }; type B struct { A A }; f()`, "undefined or recursive"},
		"unknown field":  {`type X struct { F NoSuch }; f()`, "undefined or recursive"},
		"duplicate decl": {`type X struct{}; type X struct{}; f()`, "type X redeclared"},
		"shadows":        {`type error struct{}; f()`, "shadows a registered type"},
		"dup field":      {`type X struct { N int64; N string }; f()`, "duplicate field N"},
		"unexported":     {`type X struct { n int64 }; f()`, "must be exported"},
		"embed methods":  {`type X struct { error }; f()`, "cannot promote its methods"},
		"named mismatch": {`type Point struct { N int64 }; wantI64(Point{N: 1})`, "cannot use Point as int64"},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestScriptStructJITs runs a declared-struct program down both tiers
// and compares, keeping script types inside the equivalence net.
func TestScriptStructJITs(t *testing.T) {
	rt, _ := scriptRuntime(t)
	src := `type Point struct { X int64 }
p := Point{X: 40}
s := encode(p)
return s`
	jit, slow := compilePair(t, rt, src)
	a, errA := jit.Exec[string](nil)
	b, errB := slow.Exec[string](nil)
	if a != b || (errA == nil) != (errB == nil) {
		t.Errorf("jit = %q (%v), reflect = %q (%v)", a, errA, b, errB)
	}
	if a != `{"X":40}` {
		t.Errorf("got %q", a)
	}
}

// TestScriptStructCanonical checks reflect.StructOf canonicalization:
// two programs declaring the same shape share one reflect type, so
// recompiles do not grow the type universe.
func TestScriptStructCanonical(t *testing.T) {
	rt, seen := scriptRuntime(t)
	run := func(src string) any {
		t.Helper()
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fn.Exec[string](nil); err != nil {
			t.Fatal(err)
		}
		return *seen
	}
	a := run(`type A struct { N int64 }; wantAny(A{N: 1})`)
	b := run(`type B struct { N int64 }; wantAny(B{N: 1})`)
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		t.Errorf("same shape, different types: %v vs %v", reflect.TypeOf(a), reflect.TypeOf(b))
	}
}

// TestScriptIfaceDecl checks an interface declaration registers and a
// var over it holds any value; structural checks arrive with method
// dispatch in a later stage.
func TestScriptIfaceDecl(t *testing.T) {
	rt, seen := scriptRuntime(t)
	src := `type Store interface {
	Get(key string) (string, error)
}
var s Store
wantAny(s)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = "sentinel"
	if _, err := fn.Exec[string](nil); err != nil {
		t.Fatal(err)
	}
	if *seen != nil {
		t.Errorf("zero interface read as %v", *seen)
	}

	// A method signature naming an unknown type errors.
	if _, err := rt.Compile(`type I interface { M(x NoSuch) }; f()`); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("err = %v", err)
	}
}
