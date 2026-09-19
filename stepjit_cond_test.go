package gozero

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// headerPairRuntime binds width-fixing scalar sources, bool sources,
// a recorder and a failing call: the surface the L3 equivalence and
// oracle tests need on both tiers.
func headerPairRuntime(t *testing.T, seen *[]string) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"i8":     func(v int64) int8 { return int8(v) },
		"i16":    func(v int64) int16 { return int16(v) },
		"i32":    func(v int64) int32 { return int32(v) },
		"i64":    func(v int64) int64 { return v },
		"u8":     func(v int64) uint8 { return uint8(v) },
		"u16":    func(v int64) uint16 { return uint16(v) },
		"u32":    func(v int64) uint32 { return uint32(v) },
		"u64":    func(v int64) uint64 { return uint64(v) },
		"f32":    func(v float64) float32 { return float32(v) },
		"f64":    func(v float64) float64 { return v },
		"yes":    func() bool { return true },
		"no":     func() bool { return false },
		"record": func(s string) string { *seen = append(*seen, s); return s },
		"boom":   func() (int64, error) { return 0, errors.New("boom") },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestPredMatchesReflect runs composed headers down both tiers and
// requires identical results, identical recorded effects and errors
// on the same programs. Short-circuit order is the contract under
// test: a right side the left side decided must not run, so its
// effects and its errors must not happen on either tier.
func TestPredMatchesReflect(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want any
		seen []string
		err  bool
	}{
		"and both": {
			src:  `a := yes(); b := yes(); s := "f"; if a && b { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"or short-circuits": {
			src:  `s := "f"; if yes() || record("skipped") == "x" { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"and short-circuits": {
			src:  `s := "f"; if no() && record("skipped") == "x" { s = "t"; }; return s;`,
			want: "f", seen: []string{},
		},
		"rhs runs when needed": {
			src:  `s := "f"; if no() || record("ran") == "ran" { s = "t"; }; return s;`,
			want: "t", seen: []string{"ran", "ran"},
		},
		"not": {
			src:  `s := "f"; if !no() { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"not comparison": {
			src:  `n := i64(5); s := "f"; if !(n == 4) { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"precedence and over or": {
			src:  `s := "f"; if no() && yes() || yes() { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"parens override": {
			src:  `s := "f"; if no() && (yes() || yes()) { s = "t"; }; return s;`,
			want: "f", seen: []string{},
		},
		"arith operand": {
			src:  `n := i64(7); s := "f"; if n * 3 + 1 > 20 { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"skipped error never surfaces": {
			src:  `s := "f"; if no() && boom() == 1 { s = "t"; }; record("after"); return s;`,
			want: "f", seen: []string{"after", "after"},
		},
		"reached error surfaces": {
			src: `s := "f"; if yes() && boom() == 1 { s = "t"; }; record("after");`,
			err: true, seen: []string{},
		},
		"arith operand error surfaces": {
			src: `s := "f"; if boom() + 1 > 0 { s = "t"; };`,
			err: true, seen: []string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			rt := headerPairRuntime(t, &seen)
			jit, slow := compilePair(t, rt, tc.src)
			for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
				got, err := fn(t.Context(), nil, nil)
				if tc.err {
					if err == nil {
						t.Errorf("%s: expected an error", tier)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", tier, err)
				}
				if got != tc.want {
					t.Errorf("%s: got %v, want %v", tier, got, tc.want)
				}
			}
			if fmt.Sprint(seen) != fmt.Sprint(tc.seen) {
				t.Errorf("effects %v, want %v", seen, tc.seen)
			}
		})
	}
}

// TestHeaderOracle pins header evaluation against the same
// expression compiled in Go: each case pairs a script header with a
// Go closure computing the identical expression at the identical
// static types, and both tiers must agree with the closure. The
// wraparound, signed division and float rounding rows are where a
// misreading of operator semantics would hide.
func TestHeaderOracle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup string
		head  string
		want  bool
	}{
		{"i8 wraps over max", `a := i8(127);`, `a + 1 < 0`,
			func() bool { a := int8(127); return a+1 < 0 }()},
		{"i8 wraps under min", `a := i8(-128);`, `a - 1 > 0`,
			func() bool { a := int8(-128); return a-1 > 0 }()},
		{"i8 mul wraps", `a := i8(100);`, `a * 3 == 44`,
			func() bool { a := int8(100); return a*3 == 44 }()},
		{"i32 wraps", `a := i32(2147483647);`, `a + 1 < 0`,
			func() bool { a := int32(math.MaxInt32); return a+1 < 0 }()},
		{"u8 wraps over", `a := u8(255);`, `a + 1 == 0`,
			func() bool { a := uint8(255); return a+1 == 0 }()},
		{"u8 wraps under", `a := u8(0);`, `a - 1 == 255`,
			func() bool { a := uint8(0); return a-1 == 255 }()},
		{"u8 mul wraps", `a := u8(200);`, `a * 2 == 144`,
			func() bool { a := uint8(200); return a*2 == 144 }()},
		{"signed div truncates", `a := i32(-7);`, `a / 2 == -3`,
			func() bool { a := int32(-7); return a/2 == -3 }()},
		{"signed mod sign", `a := i32(-7);`, `a % 2 == -1`,
			func() bool { a := int32(-7); return a%2 == -1 }()},
		{"div negative divisor", `a := i32(7); b := i32(-2);`, `a / b == -3 && a % b == 1`,
			func() bool { a, b := int32(7), int32(-2); return a/b == -3 && a%b == 1 }()},
		{"min over minus one", `a := i8(-128); b := i8(-1);`, `a / b == -128`,
			func() bool { a, b := int8(-128), int8(-1); return a/b == -128 }()},
		{"unsigned div", `a := u8(200); b := u8(3);`, `a / b == 66 && a % b == 2`,
			func() bool { a, b := uint8(200), uint8(3); return a/b == 66 && a%b == 2 }()},
		{"u16 width", `a := u16(65535);`, `a + 1 == 0`,
			func() bool { a := uint16(65535); return a+1 == 0 }()},
		{"u64 full width", `a := u64(-1);`, `a + 1 == 0`,
			func() bool { a := uint64(math.MaxUint64); return a+1 == 0 }()},
		{"precedence mul first", `n := i64(2);`, `n + 3 * 4 == 14`,
			func() bool { n := int64(2); return n+3*4 == 14 }()},
		{"parens group", `n := i64(2);`, `(n + 3) * 4 == 20`,
			func() bool { n := int64(2); return (n+3)*4 == 20 }()},
		{"left assoc minus", `n := i64(10);`, `n - 2 - 3 == 5`,
			func() bool { n := int64(10); return n-2-3 == 5 }()},
		{"folded rhs adopts", `a := i8(100);`, `a > 12 * 10 - 25`,
			func() bool { a := int8(100); return a > 12*10-25 }()},
		{"float64 sum compares", `x := f64(0.1); y := f64(0.2);`, `x + y > 0.3`,
			func() bool { x, y := 0.1, 0.2; return x+y > 0.3 }()},
		{"float32 rounds per op", `x := f32(0.1); y := f32(0.2);`, `x + y == 0.3`,
			func() bool { x, y := float32(0.1), float32(0.2); return x+y == 0.3 }()},
		{"float division", `x := f64(1.0);`, `x / 3.0 * 3.0 == 1.0`,
			func() bool { x := 1.0; return x/3.0*3.0 == 1.0 }()},
		{"composition", `a := i8(-1); b := u8(200);`, `a < 0 && b > 100 || a > 0`,
			func() bool { a, b := int8(-1), uint8(200); return a < 0 && b > 100 || a > 0 }()},
		{"not binds tight", `a := i64(1);`, `!(a == 2) && a == 1`,
			func() bool { a := int64(1); return !(a == 2) && a == 1 }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			rt := headerPairRuntime(t, &seen)
			src := tc.setup + ` s := "f"; if ` + tc.head + ` { s = "t"; }; return s;`
			want := "f"
			if tc.want {
				want = "t"
			}
			jit, slow := compilePair(t, rt, src)
			for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
				got, err := fn(t.Context(), nil, nil)
				if err != nil {
					t.Fatalf("%s: %v", tier, err)
				}
				if got != want {
					t.Errorf("%s: %s is %v, Go says %v", tier, tc.head, got, want)
				}
			}
		})
	}
}

// TestHeaderRuntimeDivZero pins the runtime half of division by
// zero: a divisor the compiler could not see is Go's own runtime
// panic, surfacing as *PanicError through the guard on both tiers.
func TestHeaderRuntimeDivZero(t *testing.T) {
	var seen []string
	rt := headerPairRuntime(t, &seen)
	src := `b := i64(0); s := "f"; if 7 / b == 0 { s = "t"; }; return s;`
	jit, slow := compilePair(t, rt, src)
	for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		_, err := guard(fn)(t.Context(), nil, nil)
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want a *PanicError", tier, err)
		}
	}
}

// TestHeaderSupports pins the tiering: a composed header with
// arithmetic operands is fully direct.
func TestHeaderSupports(t *testing.T) {
	var seen []string
	rt := headerPairRuntime(t, &seen)
	src := `n := 200; m := 3; s := ""; if n / m > 50 && !(n % 2 == 1) { s = "y"; }; return s;`
	if err := rt.Supports(src); err != nil {
		t.Errorf("a composed header program should be direct: %v", err)
	}
}

// BenchmarkPredHeader prices boolean composition against the same
// logic as nested ifs: one structured node with a short-circuit
// pair against two structured nodes.
func BenchmarkPredHeader(b *testing.B) {
	rt := NewRuntime()
	// The bool source is an i64_b predicate so the call has a shape;
	// a niladic func() bool is outside the table and would bridge.
	if err := rt.Bind("pos", func(v int64) bool { return v > 0 }); err != nil {
		b.Fatal(err)
	}
	for name, src := range map[string]string{
		"composed": `a := pos(1); b := pos(2); s := "x"; if a && b { s = "y"; }; return s;`,
		"nested":   "a := pos(1)\nb := pos(2)\ns := \"x\"\nif a {\n\tif b {\n\t\ts = \"y\"\n\t}\n}\nreturn s\n",
	} {
		if err := rt.Supports(src); err != nil {
			b.Fatalf("%s should be direct: %v", name, err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn.ExecContext[any](b.Context(), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkArithHeader prices an arithmetic header against the L1
// way of writing the same guard, a bound predicate doing the same
// arithmetic in Go. The delta is the cost of the operator nodes
// against one direct call.
func BenchmarkArithHeader(b *testing.B) {
	rt := NewRuntime()
	if err := rt.Bind("over20", func(v int64) bool { return v*3+1 > 20 }); err != nil {
		b.Fatal(err)
	}
	for name, src := range map[string]string{
		"arith":     `n := 7; s := "a"; if n * 3 + 1 > 20 { s = "b"; }; return s;`,
		"predicate": `n := 7; s := "a"; if over20(n) { s = "b"; }; return s;`,
	} {
		if err := rt.Supports(src); err != nil {
			b.Fatalf("%s should be direct: %v", name, err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn.ExecContext[any](b.Context(), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestHeaderDeclines pins what still leaves the direct tier: no
// operand shape does, but the compile rules named in vm_cond.go
// reject before any tier is chosen, and Supports carries their text.
func TestHeaderDeclines(t *testing.T) {
	var seen []string
	rt := headerPairRuntime(t, &seen)
	err := rt.Supports(`s := record("x"); if s + "y" == "xy" { record("z"); };`)
	if err == nil || !strings.Contains(err.Error(), "numeric arithmetic") {
		t.Errorf("string + should reject by name, got %v", err)
	}
}
