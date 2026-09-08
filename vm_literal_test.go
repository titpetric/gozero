package gozero

import (
	"strings"
	"testing"
)

// TestNumericLiteralConversion checks that a literal takes the type of
// the parameter it fills. The parser only produces int64 and float64,
// so without the conversion a binding taking int could not be called at
// all.
func TestNumericLiteralConversion(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`takesInt(42);`, int(42)},
		{`takesI8(-5);`, int8(-5)},
		{`takesU32(7);`, uint32(7)},
		{`takesF32(1.5);`, float32(1.5)},
		{`takesF32(7);`, float32(7)},
		{`takesI64(9);`, int64(9)},
		// An empty interface takes the width the parser produced.
		{`takesAny(9);`, int64(9)},
		{`takesAny(1.5);`, float64(1.5)},
	} {
		*seen = nil
		if _, err := runProgram(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("%s: binding saw %v (%T), want %v (%T)", tc.src, *seen, *seen, tc.want, tc.want)
		}
	}
}

// TestNumericLiteralRange checks that a literal the parameter cannot
// hold is a compile error rather than a wrap.
func TestNumericLiteralRange(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, tc := range []struct{ src, want string }{
		{`takesI8(300);`, "overflows int8"},
		{`takesU32(-1);`, "is negative"},
		{`takesInt(1.5);`, "decimal point"},
		{`takesBool(1);`, "cannot use a number as bool"},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected a compile error", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.src, err, tc.want)
		}
	}
}
