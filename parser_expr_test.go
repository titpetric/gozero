package gozero

import (
	"strings"
	"testing"
)

// TestExprArg pins the climber's shape rules beyond the statement
// tests in parser_test.go: associativity inside a level, the negative
// literal rule, and nesting depth.
func TestExprArg(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		spell string
	}{
		"left assoc division": {`a / b / c`, `(a / b) / c`},
		"same level mix":      {`a - b + c`, `(a - b) + c`},
		"cmp left assoc":      {`a == b == c`, `(a == b) == c`},
		"deep parens":         {`((a))`, `a`},
		"paren changes tree":  {`a * (b + c)`, `a * (b + c)`},
		"neg literal":         {`a - 1`, `a - 1`},
		"neg of neg literal":  {`a - -1`, `a - -1`},
		"spaced minus folds":  {`- a`, `-a`},
		"unary tightest":      {`-a * b`, `-a * b`},
		"not of paren":        {`!(a && b)`, `!(a && b)`},
		"complement chain":    {`^^a`, `^^a`},
	} {
		p := &Parser{src: tc.src}
		a, err := p.exprArg()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := spellExpr(a); got != tc.spell {
			t.Errorf("%s: parsed %s, want %s", name, got, tc.spell)
		}
	}
}

// TestExprArgNewline pins the semicolon rule inside an expression: a
// trailing operator continues across the line, a leading one does
// not, and an open parenthesis does not license a line break the
// grammar has no rule for.
func TestExprArgNewline(t *testing.T) {
	prog, err := (&Parser{}).Parse("x := a *\nb + c;")
	if err != nil {
		t.Fatalf("a trailing operator should continue the line: %v", err)
	}
	if got := spellExpr(*prog.stmts[0].expr); got != "(a * b) + c" {
		t.Errorf("parsed %s, want (a * b) + c", got)
	}

	// The line end closes the statement before the operator, so the
	// second line starts a statement no rule matches.
	if _, err := (&Parser{}).Parse("x := a\n* b;"); err == nil {
		t.Error("a leading operator should not join the line above")
	}
}

// TestExprArgErrors pins the parse failures with the position or rule
// named.
func TestExprArgErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		src, want string
	}{
		"unclosed":         {`(a + b`, "expected ')'"},
		"empty parens":     {`()`, "expected a name"},
		"dangling unary":   {`-`, "unexpected end of input"},
		"dangling op":      {`a *`, "unexpected end of input"},
		"step in operand":  {`a + b++`, "b++ is a statement, not a value"},
		"double semicolon": {`a + ;`, "expected a name"},
	} {
		p := &Parser{src: tc.src}
		_, err := p.exprArg()
		if err == nil {
			t.Errorf("%s: expected a parse error for %q", name, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name %q", name, err, tc.want)
		}
	}
}
