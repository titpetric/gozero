package gozero

import (
	"strings"
	"testing"
)

// parseRHS parses src as an assignment's right side and returns the
// tree, so the fold tests exercise exactly what the compiler folds.
func parseRHS(t *testing.T, src string) arg {
	t.Helper()
	p := &Parser{src: src}
	a, err := p.exprArg()
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return a
}

// TestFoldExpr pins the folded values. The wants are Go constant
// expressions of the same spelling, so the fold agrees with the
// compiler wherever both accept the input.
func TestFoldExpr(t *testing.T) {
	for src, want := range map[string]arg{
		`2 + 3*4`:        {kind: argInt, i: 2 + 3*4},
		`(2 + 3) * 4`:    {kind: argInt, i: (2 + 3) * 4},
		`10 / 4`:         {kind: argInt, i: 10 / 4},
		`10 % 4`:         {kind: argInt, i: 10 % 4},
		`-7 / 2`:         {kind: argInt, i: -7 / 2},
		`6 &^ 3`:         {kind: argInt, i: 6 &^ 3},
		`6 ^ 5`:          {kind: argInt, i: 6 ^ 5},
		`1 << 10`:        {kind: argInt, i: 1 << 10},
		`-8 >> 1`:        {kind: argInt, i: -8 >> 1},
		`-5 >> 100`:      {kind: argInt, i: -1},
		`5 >> 100`:       {kind: argInt, i: 0},
		`0 << 100`:       {kind: argInt, i: 0},
		`1.0 / 4.0`:      {kind: argFloat, f: 1.0 / 4.0},
		`1 + 2.5`:        {kind: argFloat, f: 1 + 2.5},
		`2.5 * 2`:        {kind: argFloat, f: 2.5 * 2},
		`-(-5)`:          {kind: argInt, i: -(-5)},
		`^0`:             {kind: argInt, i: ^0},
		`!true`:          {kind: argBool, b: !true},
		`!(1 < 2)`:       {kind: argBool},
		`3 < 4 && 4 < 3`: {kind: argBool, b: 3 < 4 && 4 < 3},
		`3 < 4 || 4 < 3`: {kind: argBool, b: 3 < 4 || 4 < 3},
		`"a" + "b"`:      {kind: argString, str: "ab"},
		`"a" < "b"`:      {kind: argBool, b: "a" < "b"},
		`"a" == "a"`:     {kind: argBool, b: true},
		`true == false`:  {kind: argBool, b: false},
		`1.5 == 1.5`:     {kind: argBool, b: true},
		`2 == 2.0`:       {kind: argBool, b: 2 == 2.0},
		`9 > 2`:          {kind: argBool, b: true},
	} {
		got, ok, err := foldExpr(parseRHS(t, src))
		if err != nil || !ok {
			t.Errorf("%q: ok=%v err=%v, want a fold", src, ok, err)
			continue
		}
		if got.kind != want.kind || got.i != want.i || got.f != want.f || got.b != want.b || got.str != want.str {
			t.Errorf("%q: folded %+v, want %+v", src, got, want)
		}
	}
}

// TestFoldFloatDivergence pins the one recorded divergence from Go's
// constant arithmetic. Go folds 0.1 + 0.2 exactly and rounds once,
// landing at float64(0.3); this fold works in float64 and rounds per
// operation, so it lands at the runtime sum, one ulp away. The fold
// agrees with what the same program computes through variables, which
// is the cheaper consistency; matching Go's constants exactly is
// go/constant territory and stays refused.
func TestFoldFloatDivergence(t *testing.T) {
	got, ok, err := foldExpr(parseRHS(t, `0.1 + 0.2`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	x, y := 0.1, 0.2
	if got.f != x+y {
		t.Errorf("folded %v, want the runtime sum %v", got.f, x+y)
	}
	if got.f == 0.3 {
		t.Error("folded to Go's exact constant; the recorded divergence no longer holds, update the docs")
	}
}

// TestFoldExprNotConstant proves a tree with a name in it does not
// fold: the compiler types it instead.
func TestFoldExprNotConstant(t *testing.T) {
	for _, src := range []string{`n + 1`, `1 + n`, `n && true`, `-(n)`, `1 + 2*n`} {
		if _, ok, err := foldExpr(parseRHS(t, src)); ok || err != nil {
			t.Errorf("%q: ok=%v err=%v, want no fold and no error", src, ok, err)
		}
	}
}

// TestFoldExprErrors pins the constant faults: division by zero at
// compile time as in Go, and the bound this fold adds beyond Go,
// arithmetic past int64 or float64 errors instead of silently
// wrapping, because exact constant arithmetic is go/constant's and
// this design refuses the dependency.
func TestFoldExprErrors(t *testing.T) {
	for src, want := range map[string]string{
		`1 / 0`:                     "division by zero",
		`1 % 0`:                     "division by zero",
		`1.5 / 0.0`:                 "division by zero",
		`9223372036854775807 + 1`:   "constant overflow",
		`-9223372036854775808 - 1`:  "constant overflow",
		`4294967296 * 4294967296`:   "constant overflow",
		`-9223372036854775808 / -1`: "constant overflow",
		`1 << 64`:                   "constant overflow",
		`2 << 63`:                   "constant overflow",
		`1 << -1`:                   "negative shift count",
		`1 >> -1`:                   "negative shift count",
		`-(-9223372036854775808)`:   "constant overflow",
		`"a" - "b"`:                 "operator - is not defined",
		`"a" + 1`:                   "operator + is not defined",
		`true + false`:              "operator + is not defined",
		`true < false`:              "operator < is not defined",
		`1 && 2`:                    "operator && is not defined",
		`1.5 % 0.5`:                 "operator % is not defined",
		`-"a"`:                      "operator - is not defined",
		`^1.5`:                      "operator ^ is not defined",
		`!5`:                        "operator ! is not defined",
		`179769313486231570000000000.0 * 179769313486231570000000000.0 * 1000000000000000000000000000.0 * 100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000.0`: "constant overflow",
	} {
		_, _, err := foldExpr(parseRHS(t, src))
		if err == nil {
			t.Errorf("%q: expected a fold error", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %q does not name %q", src, err, want)
		}
	}
}
