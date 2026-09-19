package gozero

import (
	"reflect"
	"testing"
)

// TestBinEvalWidths pins the closure arithmetic at the narrow widths:
// the typed store truncates exactly as compiled Go wraps, the signed
// division edge holds, and comparisons run sign-aware.
func TestBinEvalWidths(t *testing.T) {
	i8 := func(n int8) reflect.Value { return reflect.ValueOf(n) }
	u8 := func(n uint8) reflect.Value { return reflect.ValueOf(n) }
	for name, tc := range map[string]struct {
		op   string
		x, y reflect.Value
		want any
	}{
		"int8 add wraps":  {"+", i8(127), i8(1), func() int8 { n := int8(127); return n + 1 }()},
		"int8 mul wraps":  {"*", i8(100), i8(3), func() int8 { n := int8(100); return n * 3 }()},
		"uint8 sub wraps": {"-", u8(0), u8(1), func() uint8 { n := uint8(0); return n - 1 }()},
		"min over -1":     {"/", i8(-128), i8(-1), func() int8 { n, k := int8(-128), int8(-1); return n / k }()},
		"neg mod":         {"%", i8(-7), i8(2), int8(-7) % 2},
		"shl past width":  {"<<", i8(1), reflect.ValueOf(int64(10)), func() int8 { n, s := int8(1), 10; return n << s }()},
		"shr sign fill":   {">>", i8(-1), reflect.ValueOf(int64(20)), func() int8 { n, s := int8(-1), 20; return n >> s }()},
		"signed lt":       {"<", i8(-1), i8(1), int8(-1) < 1},
		"unsigned gt":     {">", u8(255), u8(1), uint8(255) > 1},
		"and not":         {"&^", i8(6), i8(3), int8(6) &^ 3},
	} {
		fn := binEval(tc.op, tc.x.Type())
		if fn == nil {
			t.Errorf("%s: no evaluator for %s over %s", name, tc.op, tc.x.Type())
			continue
		}
		if got := fn(tc.x, tc.y).Interface(); got != tc.want {
			t.Errorf("%s: got %v (%T), want %v (%T)", name, got, got, tc.want, tc.want)
		}
	}
}

// TestBinEvalUndefined proves the closure table declines what the
// admission rules never send it, so a future caller cannot slip an
// operator past the type check.
func TestBinEvalUndefined(t *testing.T) {
	for _, tc := range []struct {
		op string
		t  reflect.Type
	}{
		{"+", reflect.TypeFor[bool]()},
		{"<", reflect.TypeFor[bool]()},
		{"%", reflect.TypeFor[float64]()},
		{"&", reflect.TypeFor[string]()},
		{"==", reflect.TypeFor[*int]()},
	} {
		if binEval(tc.op, tc.t) != nil {
			t.Errorf("binEval(%q, %s) should be nil", tc.op, tc.t)
		}
	}
	if unEval("-", reflect.TypeFor[string]()) != nil {
		t.Error(`unEval("-", string) should be nil`)
	}
	if unEval("!", reflect.TypeFor[int]()) != nil {
		t.Error(`unEval("!", int) should be nil`)
	}
}

// TestUnEvalWidths pins the unary closures at the width edges.
func TestUnEvalWidths(t *testing.T) {
	neg := unEval("-", reflect.TypeFor[int8]())
	if got := neg(reflect.ValueOf(int8(-128))).Interface(); got != func() int8 { n := int8(-128); return -n }() {
		t.Errorf("-int8(-128): got %v", got)
	}
	inv := unEval("^", reflect.TypeFor[uint8]())
	if got := inv(reflect.ValueOf(uint8(0))).Interface(); got != ^uint8(0) {
		t.Errorf("^uint8(0): got %v", got)
	}
	not := unEval("!", reflect.TypeFor[bool]())
	if got := not(reflect.ValueOf(true)).Interface(); got != false {
		t.Errorf("!true: got %v", got)
	}
}

// TestShiftCountPanics pins the runtime fault a negative variable
// count raises, with the message compiled Go uses.
func TestShiftCountPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		if s, ok := r.(string); !ok || s != "runtime error: negative shift amount" {
			t.Fatalf("panicked with %v", r)
		}
	}()
	shiftCount(reflect.ValueOf(int64(-1)))
}
