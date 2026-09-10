package gozero

import (
	"strings"
	"testing"
)

// rhs parses src as a one-statement assignment and returns the
// right-hand side tree.
func rhs(t *testing.T, src string) arg {
	t.Helper()
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	if len(prog.stmts) != 1 || prog.stmts[0].lit == nil {
		t.Fatalf("%q: not a literal assignment: %+v", src, prog.stmts[0])
	}
	return *prog.stmts[0].lit
}

// TestParseExprPrecedence pins the tree shapes Go's precedence table
// produces.
func TestParseExprPrecedence(t *testing.T) {
	// 1 + 2*3: the multiplication binds tighter and sits on the right.
	a := rhs(t, `x := 1 + 2*z;`)
	if a.kind != argBinary || a.op != "+" || a.y.op != "*" {
		t.Fatalf("tree = %+v", a)
	}

	// a || b && c: && binds tighter.
	a = rhs(t, `x := a0 || b0 && c0;`)
	if a.op != "||" || a.y.op != "&&" {
		t.Fatalf("tree = %+v", a)
	}

	// x == y != z associates left.
	a = rhs(t, `x := a0 == b0 != c0;`)
	if a.op != "!=" || a.x.op != "==" {
		t.Fatalf("tree = %+v", a)
	}

	// Unary binds tighter than binary.
	a = rhs(t, `x := -a0 * b0;`)
	if a.op != "*" || a.x.kind != argUnary || a.x.op != "-" {
		t.Fatalf("tree = %+v", a)
	}

	// Parens override.
	a = rhs(t, `x := (a0 + b0) * c0;`)
	if a.op != "*" || a.x.op != "+" {
		t.Fatalf("tree = %+v", a)
	}

	// Maximal munch: &^ is one operator.
	a = rhs(t, `x := a0 &^ b0;`)
	if a.op != "&^" {
		t.Fatalf("tree = %+v", a)
	}

	// Indexing binds tighter than binary and chains.
	a = rhs(t, `x := xs[i0] + ys[j0][k0];`)
	if a.op != "+" || a.x.kind != argIndex || a.y.kind != argIndex || a.y.x.kind != argIndex {
		t.Fatalf("tree = %+v", a)
	}

	// A receive is an operand.
	a = rhs(t, `x := <-c0 + 1;`)
	if a.op != "+" || a.x.kind != argRecv {
		t.Fatalf("tree = %+v", a)
	}

	// -1 stays a negative literal; a-1 is a subtraction.
	a = rhs(t, `x := -1;`)
	if a.kind != argInt || a.i != -1 {
		t.Fatalf("tree = %+v", a)
	}
	a = rhs(t, `x := a0 - 1;`)
	if a.op != "-" || a.y.kind != argInt || a.y.i != 1 {
		t.Fatalf("tree = %+v", a)
	}

	// a < -b needs the space: <- is a receive.
	a = rhs(t, `x := a0 < -b0;`)
	if a.op != "<" || a.y.kind != argUnary {
		t.Fatalf("tree = %+v", a)
	}
}

// TestParseExprLines pins the newline rules: a trailing operator
// continues the statement, a leading one cannot, and an index bracket
// does not open across lines.
func TestParseExprLines(t *testing.T) {
	prog, err := (&Parser{}).Parse("x := a0 +\n\tb0\ny := 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.stmts) != 2 || prog.stmts[0].lit.op != "+" {
		t.Fatalf("stmts = %+v", prog.stmts)
	}

	if _, err := (&Parser{}).Parse("x := a0\n+ b0;"); err == nil {
		t.Error("a binary operator must not start a line")
	}

	// An index bracket does not open across a line: the second line
	// is its own (failing) statement rather than f0()[0].
	if _, err := (&Parser{}).Parse("x := f0()\n[0];"); err == nil {
		t.Error("a bracket must not continue the previous line")
	}
	prog, err = (&Parser{}).Parse("x := f0()\ny := 1;")
	if err != nil {
		t.Fatal(err)
	}
	if prog.stmts[0].call == nil {
		t.Fatalf("stmts = %+v", prog.stmts)
	}
}

// TestParseExprRejects pins what stays an error.
func TestParseExprRejects(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"missing rparen":   {`x := (a0 + b0;`, "expected ')'"},
		"missing rbracket": {`x := xs[1;`, "expected ']'"},
		"name to name":     {`x := y0; z := x;`, ""},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if tc.want == "" {
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	// return with a trailing operator expression.
	prog, err := (&Parser{}).Parse(`return f0(1) + 2;`)
	if err != nil {
		t.Fatal(err)
	}
	if prog.stmts[0].retVal == nil || prog.stmts[0].retVal.op != "+" {
		t.Fatalf("stmts = %+v", prog.stmts[0])
	}
}
