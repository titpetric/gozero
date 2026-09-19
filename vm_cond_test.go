package gozero

import (
	"fmt"
	"strings"
	"testing"
)

// parseHeader parses one header expression for the fold tests.
func parseHeader(t *testing.T, src string) *condExpr {
	t.Helper()
	p := &Parser{src: src}
	ce, err := p.hdrExpr(1)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return ce
}

// TestFoldArith pins constant folding: Go's constant arithmetic at
// int64 and float64 widths, integer division truncating toward zero,
// mixed int and float folding as float, and the named errors for a
// zero divisor and a non-numeric operand.
func TestFoldArith(t *testing.T) {
	for _, tc := range []struct {
		src  string
		i    int64
		f    float64
		kind argKind
	}{
		{src: "1 + 2 * 3", i: 7, kind: argInt},
		{src: "(1 + 2) * 3", i: 9, kind: argInt},
		{src: "7 / 2", i: 3, kind: argInt},
		{src: "-7 / 2", i: -3, kind: argInt},
		{src: "7 % 2", i: 1, kind: argInt},
		{src: "-7 % 2", i: -1, kind: argInt},
		{src: "10 - 2 - 3", i: 5, kind: argInt},
		{src: "1 + 2.5", f: 3.5, kind: argFloat},
		{src: "7.0 / 2", f: 3.5, kind: argFloat},
		{src: "2.0 * 3", f: 6.0, kind: argFloat},
	} {
		lit, ok, err := foldArith(parseHeader(t, tc.src))
		if err != nil || !ok {
			t.Errorf("%q: ok=%v err=%v", tc.src, ok, err)
			continue
		}
		if lit.kind != tc.kind || lit.i != tc.i || lit.f != tc.f {
			t.Errorf("%q: folded to %+v", tc.src, lit)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"div zero":        {"1 / 0", "constant division by zero"},
		"mod zero":        {"1 % 0", "constant division by zero"},
		"div folded zero": {"1 / (2 - 2)", "constant division by zero"},
		"float div zero":  {"1.0 / 0.0", "constant division by zero"},
		"float mod":       {"1.5 % 1", "numeric arithmetic"},
		"string plus":     {`"a" + "b"`, "numeric arithmetic"},
		"bool plus":       {"true + 1", "numeric arithmetic"},
	} {
		_, _, err := foldArith(parseHeader(t, tc.src))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}

	// A subtree with a name in it does not fold and is not an error.
	if _, ok, err := foldArith(parseHeader(t, "n + 1")); ok || err != nil {
		t.Errorf("n + 1: ok=%v err=%v, want neither", ok, err)
	}
}

// TestPredReflect runs composed and arithmetic headers on the
// reflect evaluator alone, the semantic reference the direct tier is
// pinned against. Short-circuit order is observable through the
// recorder: the right side of a decided && or || never runs.
func TestPredReflect(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want any
		seen []string
	}{
		"and true": {
			src:  `a := yes(""); b := yes(""); s := "f"; if a && b { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"or short-circuits": {
			src:  `s := "f"; if yes("") || record("skipped") == "x" { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"and short-circuits": {
			src:  `s := "f"; if no("") && record("skipped") == "x" { s = "t"; }; return s;`,
			want: "f", seen: []string{},
		},
		"rhs runs when needed": {
			src:  `s := "f"; if no("") || record("ran") == "ran" { s = "t"; }; return s;`,
			want: "t", seen: []string{"ran"},
		},
		"not": {
			src:  `s := "f"; if !no("") { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"precedence and over or": {
			src:  `s := "f"; if no("") && yes("") || yes("") { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
		"parens override": {
			src:  `s := "f"; if no("") && (yes("") || yes("")) { s = "t"; }; return s;`,
			want: "f", seen: []string{},
		},
		"arith operand": {
			src:  `n := 7; s := "f"; if n * 3 + 1 > 20 { s = "t"; }; return s;`,
			want: "t", seen: []string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			rt := ifPairRuntime(t, &seen)
			prog, err := (&Parser{}).Parse(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			p, err := rt.compiler.compileProgram(prog)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.run(t.Context(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if fmt.Sprint(seen) != fmt.Sprint(tc.seen) {
				t.Errorf("effects %v, want %v", seen, tc.seen)
			}
		})
	}
}
