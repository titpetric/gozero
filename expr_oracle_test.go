package gozero

import (
	"math"
	"testing"
)

// The oracle: every entry pairs a script expression with the same
// expression compiled in Go, so a shared misreading of operator
// semantics between the tiers cannot hide. The script side receives
// its operands through width-fixing bindings; the Go side computes
// with the language itself.
func TestExprOracle(t *testing.T) {
	rt := exprRuntime(t)
	run := func(src string) any {
		t.Helper()
		v, err := rt.Eval[any](src, nil)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		return v
	}

	for _, tc := range []struct {
		src  string
		want any
	}{
		// Wraparound at every signed width against Go's own overflow.
		{`n := i8(127); return n + 1;`, func() int8 { n := int8(127); return n + 1 }()},
		{`n := i8(-128); return n - 1;`, func() int8 { n := int8(-128); return n - 1 }()},
		{`n := i8(100); return n * 3;`, func() int8 { n := int8(100); return n * 3 }()},
		{`n := i32(2147483647); return n + 1;`, func() int32 { n := int32(math.MaxInt32); return n + 1 }()},
		{`n := u8(255); return n + 1;`, func() uint8 { n := uint8(255); return n + 1 }()},
		{`n := u8(0); return n - 1;`, func() uint8 { n := uint8(0); return n - 1 }()},

		// Signed division and modulo with negatives.
		{`n := i32(-7); return n / 2;`, int32(-7) / 2},
		{`n := i32(-7); return n % 2;`, int32(-7) % 2},
		{`n := i32(7); m := i32(-2); return n / m;`, int32(7) / int32(-2)},
		{`n := i32(7); m := i32(-2); return n % m;`, int32(7) % int32(-2)},
		{`n := i8(-128); m := i8(-1); return n / m;`, func() int8 { n, m := int8(-128), int8(-1); return n / m }()},

		// Shift edges: past the width, and signed right shifts.
		{`n := i8(1); return n << 7;`, func() int8 { n := int8(1); return n << 7 }()},
		{`n := i8(1); return n << 9;`, func() int8 { n, s := int8(1), 9; return n << s }()},
		{`n := i8(-8); return n >> 1;`, int8(-8) >> 1},
		{`n := i8(-1); return n >> 20;`, func() int8 { n, s := int8(-1), 20; return n >> s }()},
		{`n := u8(128); return n >> 1;`, uint8(128) >> 1},

		// Bitwise, including and-not.
		{`n := i8(6); m := i8(3); return n &^ m;`, int8(6) &^ int8(3)},
		{`n := u8(6); m := u8(5); return n ^ m;`, uint8(6) ^ uint8(5)},

		// Float semantics: NaN never compares, float32 rounds per op.
		{`f := 0.0; g := f / f; return g == g;`, func() bool { f := 0.0; g := f / f; return g == g }()},
		{`f := 0.0; g := f / f; return g != g;`, true},
		{`f := 1.0; return f / 0.0;`, math.Inf(1)},
		{`f := f32(0.1); g := f32(0.2); return f + g;`, float32(0.1) + float32(0.2)},

		// String ordering is byte-wise.
		{`s := "abc"; return s < "abd";`, "abc" < "abd"},
		{`s := "Z"; return s < "a";`, "Z" < "a"},
		{`s := "ab"; return s >= "ab";`, true},

		// Unary over the widths.
		{`n := i8(-128); return -n;`, func() int8 { n := int8(-128); return -n }()},
		{`n := u8(1); return -n;`, func() uint8 { n := uint8(1); return -n }()},
		{`n := u8(0); return ^n;`, ^uint8(0)},
	} {
		if got := run(tc.src); got != tc.want {
			t.Errorf("%q: got %v (%T), Go says %v (%T)", tc.src, got, got, tc.want, tc.want)
		}
	}
}
