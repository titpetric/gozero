package gozero

import (
	"reflect"
	"testing"
)

// TestFoldBinary exercises the constant folder directly: kinds mix
// and unify the way untyped constants do, and impossible operations
// error at compile time.
func TestFoldBinary(t *testing.T) {
	fold := func(op string, x, y arg) arg {
		t.Helper()
		out, ok, err := foldBinary(op, x, y)
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", op, ok, err)
		}
		return out
	}
	i := func(n int64) arg { return arg{kind: argInt, i: n} }
	f := func(v float64) arg { return arg{kind: argFloat, f: v} }
	s := func(v string) arg { return arg{kind: argString, str: v} }

	if got := fold("+", i(2), i(3)); got.i != 5 {
		t.Errorf("2+3 = %v", got)
	}
	if got := fold("*", i(2), f(1.5)); got.kind != argFloat || got.f != 3 {
		t.Errorf("2*1.5 = %+v", got)
	}
	if got := fold("+", s("a"), s("b")); got.str != "ab" {
		t.Errorf("a+b = %+v", got)
	}
	if got := fold("<", s("a"), s("b")); !got.b {
		t.Errorf("a<b = %+v", got)
	}
	if got := fold("&^", i(6), i(3)); got.i != 4 {
		t.Errorf("6&^3 = %+v", got)
	}
	if _, _, err := foldBinary("/", i(1), i(0)); err == nil {
		t.Error("1/0 must be a compile error")
	}
	if _, _, err := foldBinary("+", s("a"), i(1)); err == nil {
		t.Error("string+int must be a compile error")
	}
	if _, _, err := foldBinary("<<", i(1), i(-1)); err == nil {
		t.Error("a negative constant shift must be a compile error")
	}
}

// TestBinEvalWidths drives the evaluator closures straight, one per
// width family, checking the typed store truncates the way native
// arithmetic wraps.
func TestBinEvalWidths(t *testing.T) {
	i8 := reflect.TypeFor[int8]()
	add := binEval("+", i8)
	got := add(reflect.ValueOf(int8(127)), reflect.ValueOf(int8(1)))
	if got.Interface() != int8(-128) {
		t.Errorf("int8 127+1 = %v", got)
	}

	u16 := reflect.TypeFor[uint16]()
	mul := binEval("*", u16)
	got = mul(reflect.ValueOf(uint16(300)), reflect.ValueOf(uint16(300)))
	if got.Interface() != uint16(300*300%65536) {
		t.Errorf("uint16 300*300 = %v", got)
	}

	if binEval("%", reflect.TypeFor[float64]()) != nil {
		t.Error("float %% must have no evaluator")
	}
	if binEval("&&", reflect.TypeFor[int64]()) != nil {
		t.Error("&& on ints must have no evaluator")
	}
	if unEval("!", reflect.TypeFor[int64]()) != nil {
		t.Error("! on ints must have no evaluator")
	}
}
