package gozero

import (
	"context"
	"strings"
	"testing"
)

// TestExprReflect covers the tree compiler and evaluator on the
// reflect tier, the semantic reference: adoption at depth, named
// types, and the result type rule at the assigned name.
func TestExprReflect(t *testing.T) {
	rt := binopRuntime(t)
	for name, tc := range map[string]struct {
		src  string
		want any
	}{
		"constant adopts deep": {`var w uint8; w = 7; z := w*2 + 1; return z;`, uint8(15)},
		"named string concat":  {`a := word(); b := word(); s := a + b + a; return s;`, word("leftleftleft")},
		"named string order":   {`a := word(); ok := a < "m" && a >= "l"; return ok;`, true},
		"comparison is bool":   {`n := 1; ok := n == 1; both := ok == true; return both;`, true},
		"logic chain":          {`a := true; b := false; ok := a && !b && (a || b); return ok;`, true},
		"unary of paren":       {`n := 3; m := -(n + 1) * 2; return m;`, int64(-8)},
		"shift count var":      {`n := 1; s := 3; m := n << s << 1; return m;`, int64(16)},
	} {
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
				t.Errorf("got %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestExprCompileErrors pins the tree-level rejections: the operand
// rule at depth, mismatches between subtrees, and the operator
// admission per node.
func TestExprCompileErrors(t *testing.T) {
	rt := binopRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"call operand deep": {
			`n := 1; m := n + urlOf() == n;`,
			"urlOf(...) cannot be an operand",
		},
		"field operand deep": {
			`u := urlOf(); n := 1; s := n + 1 == 2 && u.Path == "x";`,
			"u.Path cannot be an operand",
		},
		"mismatch at depth": {
			`n := 1; f := 2.5; z := 1 + n*f;`,
			"mismatched types int64 and float64",
		},
		"logic over strings": {
			`a := "x"; b := "y"; ok := a && b;`,
			"operator && wants bool operands, not string",
		},
		"logic mixed": {
			`a := true; n := 1; ok := a && n;`,
			"mismatched types bool and int64",
		},
		"comparison feeds arithmetic": {
			`n := 1; m := n + (n == 1);`,
			"mismatched types int64 and bool",
		},
		"order on named bool result": {
			`n := 1; ok := (n == 1) < true;`,
			"operator < is not defined on bool",
		},
		"undefined name deep": {
			`n := 1; m := n + ghost*2;`,
			"ghost has no static type here",
		},
		"overflow at adopted width": {
			`var w uint8; z := w + 300;`,
			"300 overflows uint8",
		},
		"result type at name": {
			`x := 2.5; n := 1; x = n == 1 && true;`,
			"cannot use bool as float64",
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

// TestExprShortCircuitReflect proves laziness on the reflect tier by
// effect: the guarded division never runs, the unguarded one panics.
func TestExprShortCircuitReflect(t *testing.T) {
	rt := NewRuntime()
	fn, err := rt.Compile(`n := 0; ok := n != 0 && 7/n > 1; return ok;`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(context.Background(), nil, nil)
	if err != nil || got != false {
		t.Fatalf("guarded division: got %v, %v", got, err)
	}
	fn, err = rt.Compile(`n := 0; ok := n == 0 && 7/n > 1; return ok;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "integer divide by zero") {
		t.Fatalf("unguarded division: err = %v", err)
	}
}
