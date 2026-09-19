package gozero

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// The oracle: every entry pairs a script program with the same
// expression compiled in Go, so a shared misreading of operator
// semantics between the tiers cannot hide. The script side fixes its
// operand widths with conversion hints; the Go side computes with the
// language itself. Both tiers run every entry and must agree with the
// compiled value and with each other.
var exprOraclePrograms = map[string]struct {
	src  string
	want any
}{
	// Wraparound at every signed width against Go's own overflow.
	"int8 add wraps":  {`n := int8(127); m := n + 1; return m;`, func() int8 { n := int8(127); return n + 1 }()},
	"int8 sub wraps":  {`n := int8(-128); m := n - 1; return m;`, func() int8 { n := int8(-128); return n - 1 }()},
	"int8 mul wraps":  {`n := int8(100); m := n * 3; return m;`, func() int8 { n := int8(100); return n * 3 }()},
	"int32 add wraps": {`n := int32(2147483647); m := n + 1; return m;`, func() int32 { n := int32(math.MaxInt32); return n + 1 }()},
	"uint8 add wraps": {`n := uint8(255); m := n + 1; return m;`, func() uint8 { n := uint8(255); return n + 1 }()},
	"uint8 sub wraps": {`n := uint8(0); m := n - 1; return m;`, func() uint8 { n := uint8(0); return n - 1 }()},
	"uint8 mul wraps": {`n := uint8(200); m := n * 2; return m;`, func() uint8 { n := uint8(200); return n * 2 }()},

	// Signed division and modulo with negatives, and the edge the Go
	// spec fixes: the most negative value over -1 wraps to itself.
	"neg div":          {`n := int32(-7); m := n / 2; return m;`, int32(-7) / 2},
	"neg mod":          {`n := int32(-7); m := n % 2; return m;`, int32(-7) % 2},
	"div neg divisor":  {`n := int32(7); k := int32(-2); m := n / k; return m;`, int32(7) / int32(-2)},
	"mod neg divisor":  {`n := int32(7); k := int32(-2); m := n % k; return m;`, int32(7) % int32(-2)},
	"min int8 over -1": {`n := int8(-128); k := int8(-1); m := n / k; return m;`, func() int8 { n, k := int8(-128), int8(-1); return n / k }()},

	// Shift edges: past the width, signed fills, a count of another
	// integer type.
	"shl to sign":       {`n := int8(1); m := n << 7; return m;`, func() int8 { n := int8(1); return n << 7 }()},
	"shl past width":    {`n := int8(1); s := 9; m := n << s; return m;`, func() int8 { n, s := int8(1), 9; return n << s }()},
	"shr signed":        {`n := int8(-8); m := n >> 1; return m;`, int8(-8) >> 1},
	"shr sign fill":     {`n := int8(-1); s := 20; m := n >> s; return m;`, func() int8 { n, s := int8(-1), 20; return n >> s }()},
	"shr unsigned":      {`n := uint8(128); m := n >> 1; return m;`, uint8(128) >> 1},
	"shift uint8 count": {`n := int8(1); s := uint8(3); m := n << s; return m;`, func() int8 { n, s := int8(1), uint8(3); return n << s }()},

	// Bitwise, including and-not.
	"and not": {`n := int8(6); k := int8(3); m := n &^ k; return m;`, int8(6) &^ int8(3)},
	"xor":     {`n := uint8(6); k := uint8(5); m := n ^ k; return m;`, uint8(6) ^ uint8(5)},
	"and or":  {`n := 12; m := n&10 | 1; return m;`, func() int64 { n := int64(12); return n&10 | 1 }()},

	// Float semantics: NaN never compares, division by a variable
	// zero is an infinity, float32 rounds per operation.
	"nan eq":      {`f := 0.0; g := f / f; ok := g == g; return ok;`, func() bool { f := 0.0; g := f / f; return g == g }()},
	"nan ne":      {`f := 0.0; g := f / f; ok := g != g; return ok;`, true},
	"div inf":     {`f := 1.0; z := 0.0; g := f / z; return g;`, math.Inf(1)},
	"f32 rounds":  {`f := float32(0.1); g := float32(0.2); h := f + g; return h;`, float32(0.1) + float32(0.2)},
	"f32 mul":     {`f := float32(1.1); g := float32(3.0); h := f * g; return h;`, func() float32 { f, g := float32(1.1), float32(3.0); return f * g }()},
	"float chain": {`f := 1.5; g := (f + 0.5) * 2.0; return g;`, func() float64 { f := 1.5; return (f + 0.5) * 2.0 }()},
	"float ge":    {`f := 2.5; ok := f >= 2.5; return ok;`, true},

	// String ordering is byte-wise.
	"string lt":     {`s := "abc"; ok := s < "abd"; return ok;`, "abc" < "abd"},
	"string case":   {`s := "Z"; ok := s < "a"; return ok;`, "Z" < "a"},
	"string ge":     {`s := "ab"; t := "ab"; ok := s >= t; return ok;`, true},
	"concat chain":  {`s := "go"; m := s + "-" + s; return m;`, func() string { s := "go"; return s + "-" + s }()},
	"concat leftpc": {`s := "x"; m := "pre-" + s + "!"; return m;`, func() string { s := "x"; return "pre-" + s + "!" }()},

	// Unary over the widths.
	"neg min int8":   {`n := int8(-128); m := -n; return m;`, func() int8 { n := int8(-128); return -n }()},
	"neg uint8":      {`n := uint8(1); m := -n; return m;`, func() uint8 { n := uint8(1); return -n }()},
	"not uint8":      {`n := uint8(0); m := ^n; return m;`, ^uint8(0)},
	"not int64":      {`n := 7; m := ^n; return m;`, func() int64 { n := int64(7); return ^n }()},
	"neg float":      {`f := 2.5; g := -f; return g;`, -2.5},
	"logical not":    {`b := true; c := !b; return c;`, false},
	"double neg":     {`n := 7; m := -(-n + 1); return m;`, func() int64 { n := int64(7); return -(-n + 1) }()},
	"unary plus":     {`n := 7; m := +n + 1; return m;`, func() int64 { n := int64(7); return +n + 1 }()},
	"unary in chain": {`n := 7; m := -n * 2; return m;`, func() int64 { n := int64(7); return -n * 2 }()},

	// Precedence and mixed logic.
	"shift beats add": {`a := 5; m := a<<2 + 1; return m;`, func() int64 { a := int64(5); return a<<2 + 1 }()},
	"mul beats add":   {`a := 2; b := 3; c := 4; m := a + b*c; return m;`, func() int64 { a, b, c := int64(2), int64(3), int64(4); return a + b*c }()},
	"parens group":    {`a := 2; b := 3; m := (a + b) * a; return m;`, func() int64 { a, b := int64(2), int64(3); return (a + b) * a }()},
	"mixed logic":     {`n := 5; ok := n > 4 && n+1 == 6 || n < 0; return ok;`, func() bool { n := int64(5); return n > 4 && n+1 == 6 || n < 0 }()},
	"cmp of cmp":      {`n := 1; ok := n == 1 == true; return ok;`, func() bool { n := int64(1); return n == 1 == true }()},

	// Short-circuit: the guarded division only runs when the left
	// side does not decide, so no panic reaches either tier.
	"and guards":    {`n := 0; ok := n != 0 && 7/n > 1; return ok;`, func() bool { n := int64(0); return n != 0 && 7/n > 1 }()},
	"or guards":     {`n := 0; ok := n == 0 || 7/n > 1; return ok;`, func() bool { n := int64(0); return n == 0 || 7/n > 1 }()},
	"and evaluates": {`n := 7; ok := n > 0 && n/2 == 3; return ok;`, func() bool { n := int64(7); return n > 0 && n/2 == 3 }()},

	// Constant folding: the whole tree lands as one literal, typed by
	// the slot the way any literal is.
	"fold ints":     {`m := 60 * 60 * 24; return m;`, func() int64 { return 60 * 60 * 24 }()},
	"fold int div":  {`m := 10 / 4; return m;`, func() int64 { return 10 / 4 }()},
	"fold floats":   {`f := 1.0 / 4.0; return f;`, func() float64 { return 1.0 / 4.0 }()},
	"fold strings":  {`s := "a" + "b" + "c"; return s;`, "a" + "b" + "c"},
	"fold logic":    {`ok := 3 < 4 && "a" < "b"; return ok;`, 3 < 4 && "a" < "b"},
	"fold unary":    {`m := -(-5); return m;`, func() int64 { return -(-5) }()},
	"fold at width": {`var w uint8; w = 200 + 55; return w;`, func() uint8 { var w uint8; w = 200 + 55; return w }()},
	"fold adopts":   {`n := int8(1); m := n + 2*3; return m;`, func() int8 { n := int8(1); return n + 2*3 }()},
}

// TestExprOracle runs every program on both tiers and compares each
// against the value the Go compiler produced for the same expression.
func TestExprOracle(t *testing.T) {
	rt := NewRuntime()
	for name, tc := range exprOraclePrograms {
		t.Run(name, func(t *testing.T) {
			jit, slow := compilePair(t, rt, tc.src)
			jitRes, jitErr := jit(context.Background(), nil, nil)
			slowRes, slowErr := slow(context.Background(), nil, nil)
			if jitErr != nil || slowErr != nil {
				t.Fatalf("err = %v (jit) vs %v (reflect)", jitErr, slowErr)
			}
			if jitRes != slowRes {
				t.Errorf("result = %v (%T, jit) vs %v (%T, reflect)", jitRes, jitRes, slowRes, slowRes)
			}
			if jitRes != tc.want {
				t.Errorf("got %v (%T), Go says %v (%T)", jitRes, jitRes, tc.want, tc.want)
			}
		})
	}
}

// TestExprOraclePanics pins the runtime faults: both tiers turn the
// same arithmetic panic compiled Go raises into a *PanicError naming
// the same runtime error.
func TestExprOraclePanics(t *testing.T) {
	rt := NewRuntime()
	for name, tc := range map[string]struct{ src, want string }{
		"divide by zero":   {`n := 0; m := 7 / n; return m;`, "integer divide by zero"},
		"modulo by zero":   {`n := 0; m := 7 % n; return m;`, "integer divide by zero"},
		"negative shift":   {`n := 1; s := -1; m := n << s; return m;`, "negative shift amount"},
		"negative shr":     {`n := 1; s := 0 - 1; m := n >> s; return m;`, "negative shift amount"},
		"unguarded divide": {`n := 0; ok := n == 0 && 7/n > 1; return ok;`, "integer divide by zero"},
	} {
		t.Run(name, func(t *testing.T) {
			jit, slow := compilePair(t, rt, tc.src)
			for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
				_, err := guard(fn)(context.Background(), nil, nil)
				if err == nil {
					t.Fatalf("%s: expected the panic to surface", tier)
				}
				var pe *PanicError
				if !errors.As(err, &pe) {
					t.Fatalf("%s: err is %T, want *PanicError", tier, err)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s: error %q does not name %q", tier, err, tc.want)
				}
			}
		})
	}
}

// TestExprOracleCompileErrors pins the constant rules against the Go
// compiler's: a constant division by zero, a constant overflow and a
// negative constant shift all fail at compile time, as they do in Go.
func TestExprOracleCompileErrors(t *testing.T) {
	rt := NewRuntime()
	for name, tc := range map[string]struct{ src, want string }{
		"const div zero":       {`x := 1 / 0;`, "division by zero"},
		"const mod zero":       {`x := 1 % 0;`, "division by zero"},
		"const float div zero": {`x := 1.5 / 0.0;`, "division by zero"},
		"typed over const 0":   {`n := 1; x := n / 0;`, "division by zero"},
		"typed mod const 0":    {`n := 1; x := n % 0;`, "division by zero"},
		"float over const 0":   {`f := 1.5; x := f / 0.0;`, "division by zero"},
		"const add overflow":   {`x := 9223372036854775807 + 1;`, "constant overflow"},
		"const mul overflow":   {`x := 4294967296 * 4294967296;`, "constant overflow"},
		"const shl overflow":   {`x := 1 << 64;`, "constant overflow"},
		"const neg shift":      {`x := 1 << -1;`, "negative shift count"},
		"typed neg shift":      {`n := 1; x := n << -1;`, "negative shift count"},
		"const string minus":   {`x := "a" - "b";`, `operator - is not defined on "a" and "b"`},
		"const bool plus":      {`x := true + false;`, "operator + is not defined on true and false"},
		"const mixed kinds":    {`x := 1 + "a";`, `operator + is not defined on 1 and "a"`},
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
