package gozero

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"testing"
)

// binopRuntime binds what the operator programs format their results
// with, plus the operand sources the tests need: a NaN, a named
// string type, and a pointer for the comparison rejections.
func binopRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	if err := rt.BindScope("fmt", map[string]any{"Sprintf": fmt.Sprintf}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("nan", math.NaN); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("word", func() word { return "left" }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("urlOf", func() *url.URL { return &url.URL{Path: "/x"} }); err != nil {
		t.Fatal(err)
	}
	return rt
}

// word is a named string type: Go concatenates and compares it like
// any string, and a literal operand adopts it the way an untyped
// constant converts.
type word string

// binopPrograms pins the operator semantics against the Go compiler:
// every want is computed by the same expression in compiled Go, so a
// divergence in wraparound, rounding or NaN ordering fails here
// rather than passing as a shared misunderstanding between the two
// tiers. Both tiers run the table.
var binopPrograms = map[string]struct{ src, want string }{
	"string concat": {
		`a := "con"; b := "cat"; s := a + b; return s;`,
		func() string { a, b := "con", "cat"; return a + b }(),
	},
	"concat literal right": {
		`a := "x"; s := a + "!"; return s;`,
		func() string { a := "x"; return a + "!" }(),
	},
	"concat literal left": {
		`a := "x"; s := "pre-" + a; return s;`,
		func() string { a := "x"; return "pre-" + a }(),
	},
	"concat empty side": {
		`a := "x"; b := ""; s := a + b; return s;`,
		func() string { a, b := "x", ""; return a + b }(),
	},
	"concat rebinds its own name": {
		`s := "a"; t := "b"; s = s + t; s = s + t; return s;`,
		func() string { s, t := "a", "b"; s = s + t; s = s + t; return s }(),
	},
	"named string type": {
		`a := word(); b := word(); s := a + b; ok := s == "leftleft"; r := fmt.Sprintf("%T %v %v", s, s, ok); return r;`,
		func() string {
			a, b := word("left"), word("left")
			s := a + b
			ok := s == "leftleft"
			return fmt.Sprintf("%T %v %v", s, s, ok)
		}(),
	},
	"int64 add": {
		`n := 40; m := n + 2; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int64(40); m := n + 2; return fmt.Sprintf("%d", m) }(),
	},
	"int64 add negative literal": {
		`n := 40; m := n + -2; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int64(40); m := n + -2; return fmt.Sprintf("%d", m) }(),
	},
	"int8 wraps": {
		`var n int8; n = 120; m := n + 10; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int8(120); m := n + 10; return fmt.Sprintf("%d", m) }(),
	},
	"int16 wraps": {
		`var n int16; n = 32767; m := n + 1; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int16(32767); m := n + 1; return fmt.Sprintf("%d", m) }(),
	},
	"int32 wraps": {
		`n := int32(2147483647); m := n + 1; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int32(2147483647); m := n + 1; return fmt.Sprintf("%d", m) }(),
	},
	"uint8 wraps": {
		`var w uint8; w = 255; z := w + 1; s := fmt.Sprintf("%d", z); return s;`,
		func() string { w := uint8(255); z := w + 1; return fmt.Sprintf("%d", z) }(),
	},
	"uint16 wraps": {
		`var w uint16; w = 65535; z := w + 2; s := fmt.Sprintf("%d", z); return s;`,
		func() string { w := uint16(65535); z := w + 2; return fmt.Sprintf("%d", z) }(),
	},
	"uint64 wraps": {
		`var w uint64; w--; z := w + 1; s := fmt.Sprintf("%d", z); return s;`,
		func() string { var w uint64; w--; z := w + 1; return fmt.Sprintf("%d", z) }(),
	},
	"float64 add": {
		`x := 0.1; y := 0.2; z := x + y; s := fmt.Sprintf("%v", z); return s;`,
		func() string { x, y := 0.1, 0.2; z := x + y; return fmt.Sprintf("%v", z) }(),
	},
	"float32 rounds once": {
		`x := float32(0.1); y := float32(0.2); z := x + y; s := fmt.Sprintf("%T %v", z, z); return s;`,
		func() string { x, y := float32(0.1), float32(0.2); z := x + y; return fmt.Sprintf("%T %v", z, z) }(),
	},
	"float32 overflows to Inf": {
		`x := float32(340000000000000000000000000000000000000.0); z := x + x; s := fmt.Sprintf("%v", z); return s;`,
		func() string { x := float32(340000000000000000000000000000000000000.0); z := x + x; return fmt.Sprintf("%v", z) }(),
	},
	"int equal": {
		`n := 42; ok := n == 42; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { n := int64(42); ok := n == 42; return fmt.Sprintf("%v", ok) }(),
	},
	"int not equal": {
		`n := 42; ok := n != 42; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { n := int64(42); ok := n != 42; return fmt.Sprintf("%v", ok) }(),
	},
	"string equal": {
		`a := "x"; b := "y"; ok := a == b; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { a, b := "x", "y"; ok := a == b; return fmt.Sprintf("%v", ok) }(),
	},
	"string not equal literal": {
		`a := "x"; ok := a != "y"; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { a := "x"; ok := a != "y"; return fmt.Sprintf("%v", ok) }(),
	},
	"bool equal literal": {
		`a := true; ok := a == false; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { a := true; ok := a == false; return fmt.Sprintf("%v", ok) }(),
	},
	"bool of comparison compares on": {
		`n := 1; a := n == 1; b := n == 2; ok := a != b; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { n := int64(1); a := n == 1; b := n == 2; ok := a != b; return fmt.Sprintf("%v", ok) }(),
	},
	"NaN is unequal to itself": {
		`x := nan(); eq := x == x; ne := x != x; s := fmt.Sprintf("%v %v", eq, ne); return s;`,
		func() string { x := math.NaN(); eq := x == x; ne := x != x; return fmt.Sprintf("%v %v", eq, ne) }(),
	},
	"uint8 comparison after wrap": {
		`var w uint8; w = 255; z := w + 1; ok := z == 0; s := fmt.Sprintf("%v", ok); return s;`,
		func() string { w := uint8(255); z := w + 1; ok := z == 0; return fmt.Sprintf("%v", ok) }(),
	},
	// The folded cases below write their wants as the same constant
	// expression in this Go file, so the fold is pinned against the Go
	// compiler's own constant arithmetic, not against a reimplementation.
	"folded operand": {
		`n := 40; m := n + 2*1; s := fmt.Sprintf("%d", m); return s;`,
		func() string { n := int64(40); m := n + 2*1; return fmt.Sprintf("%d", m) }(),
	},
	"whole constant fold": {
		`x := (2 + 3) * 4; s := fmt.Sprintf("%d", x); return s;`,
		fmt.Sprintf("%d", (2+3)*4),
	},
	"fold exceeds int64 mid-expression": {
		`big := 1<<70 / (1 << 65); s := fmt.Sprintf("%d", big); return s;`,
		fmt.Sprintf("%d", 1<<70/(1<<65)),
	},
	"float fold rounds once like Go": {
		`tenth := 0.1 + 0.2; ok := tenth == 0.3; s := fmt.Sprintf("%v %v", tenth, ok); return s;`,
		fmt.Sprintf("%v %v", 0.1+0.2, 0.1+0.2 == 0.3),
	},
	"folded comparison feeds a runtime one": {
		`lim := 3 * 4; n := 12; hit := n == lim; s := fmt.Sprintf("%v", hit); return s;`,
		func() string { n := int64(12); hit := n == 3*4; return fmt.Sprintf("%v", hit) }(),
	},
}

// TestBinopReflect runs the oracle table on the reflect evaluator,
// which is the semantic reference for the direct tier.
func TestBinopReflect(t *testing.T) {
	rt := binopRuntime(t)
	for name, tc := range binopPrograms {
		t.Run(name, func(t *testing.T) {
			prog, err := (&Parser{}).Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			p, err := rt.compiler.compileProgram(prog)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := p.run(context.Background(), nil, nil)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %s", got, tc.want)
			}
		})
	}
}

// TestBinopCompileErrors pins every rule that rejects an operator
// statement at compile time, by message.
func TestBinopCompileErrors(t *testing.T) {
	rt := binopRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"mismatched types": {
			`n := 1; x := 2.5; z := n + x;`,
			"mismatched types int64 and float64",
		},
		"named against unnamed string": {
			`a := word(); b := "s"; c := "s"; d := b + c; z := a + d;`,
			"mismatched types gozero.word and string",
		},
		"plus on bool": {
			`a := true; b := false; c := a + b;`,
			"operator + is not defined on bool",
		},
		"plus on pointer": {
			`u := urlOf(); v := urlOf(); w := u + v;`,
			"operator + is not defined on *url.URL",
		},
		"compare pointers": {
			`u := urlOf(); v := urlOf(); ok := u == v;`,
			"== and != compare booleans, integers, floats and strings, not *url.URL",
		},
		"stack name operand": {
			`s := outside + "x";`,
			"outside has no static type here; a name read from the stack cannot be an operand, bind it with := first",
		},
		"undefined name operand": {
			`s := ghost + "x";`,
			"ghost has no static type here",
		},
		"literal overflows the named side": {
			`var n int8; m := n + 300;`,
			"300 overflows int8",
		},
		"negative literal on unsigned": {
			`var w uint8; z := w + -1;`,
			"cannot use -1 as uint8, it is negative",
		},
		"float literal on integer": {
			`n := 1; m := n + 2.5;`,
			"cannot use 2.5 as int64, it has a decimal point",
		},
		"string literal on integer": {
			`n := 1; m := n + "x";`,
			`cannot use "x" as int64`,
		},
		"result type differs from the target": {
			`x := 2.5; n := 1; x = n == 1;`,
			"cannot use bool as float64",
		},
		"comparison into a string name": {
			`s := "x"; n := 1; s = n == 1;`,
			"cannot use bool as string",
		},
		"multi-assign folded": {
			`a, b := 1 + 2;`,
			"a literal assigns to exactly one name",
		},
		"multi-assign runtime": {
			`n := 1; a, b := n + 2;`,
			"an operator assigns to exactly one name",
		},
		"assign without declare": {
			`z = 1 + 2;`,
			"z is not defined, use := or var",
		},
	} {
		t.Run(name, func(t *testing.T) {
			prog, err := (&Parser{}).Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = rt.compiler.compileProgram(prog)
			if err == nil {
				t.Fatalf("expected a compile error for %q", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the rule %q", err, tc.want)
			}
		})
	}
}

// TestBinopSharedLiteral proves a rerun starts from the source, not
// from the previous run's slots: the same compiled program returns
// the same concatenation twice, and the prebuilt literal the runs
// share is never mutated by the self-append.
func TestBinopSharedLiteral(t *testing.T) {
	rt := binopRuntime(t)
	fn, err := rt.Compile(`s := "a"; t := "b"; s = s + t; return s;`)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		got, err := fn.ExecContext[string](context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ab" {
			t.Fatalf("run %d: got %q, want ab", run, got)
		}
	}
}
