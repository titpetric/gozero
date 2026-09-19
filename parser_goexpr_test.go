package gozero

import (
	"strings"
	"testing"
)

// parseOne parses a single-statement program and returns its
// statement.
func parseOne(t *testing.T, src string) stmt {
	t.Helper()
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return prog.stmts[0]
}

// TestGoExprFoldOracle pins the parse-time fold against the Go
// compiler itself: every want is the same expression written as a Go
// constant in this file. Precedence, integer division, remainder
// sign, arbitrary-precision intermediates, one-time float rounding
// and string ordering are compared with what compiled Go folds, not
// with a second implementation of the rules.
func TestGoExprFoldOracle(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int64
	}{
		"precedence":            {`x := 2 + 3*4;`, 2 + 3*4},
		"parens regroup":        {`x := (2 + 3) * 4;`, (2 + 3) * 4},
		"integer division":      {`x := 7 / 2;`, 7 / 2},
		"truncates toward zero": {`x := -7 / 2;`, -7 / 2},
		"remainder sign":        {`x := 7 % -3;`, 7 % -3},
		"big intermediate":      {`x := 1<<70 / (1 << 65);`, 1 << 70 / (1 << 65)},
		"unary minus grouped":   {`x := -(3 - 5);`, -(3 - 5)},
		"unary complement":      {`x := ^5;`, ^5},
		"unary plus":            {`x := +5;`, +5},
		"hex and mask":          {`x := (1<<8 - 1) ^ 0xF0;`, (1<<8 - 1) ^ 0xF0},
		"bare hex literal":      {`x := 0xF0;`, 0xF0},
		"max int64":             {`x := 1<<63 - 1;`, 1<<63 - 1},
		"min int64":             {`x := -(1 << 63);`, -(1 << 63)},
	} {
		s := parseOne(t, tc.src)
		if s.lit == nil || s.lit.kind != argInt {
			t.Errorf("%s: %q did not fold to an int literal: %+v", name, tc.src, s)
			continue
		}
		if s.lit.i != tc.want {
			t.Errorf("%s: %q folded to %d, the Go compiler folds it to %d", name, tc.src, s.lit.i, tc.want)
		}
	}

	for name, tc := range map[string]struct {
		src  string
		want float64
	}{
		"exact rational":     {`x := 0.1 + 0.2;`, 0.1 + 0.2},
		"third":              {`x := 1 / 3.0;`, 1 / 3.0},
		"mixed widths":       {`x := 2.5 * 4;`, 2.5 * 4},
		"exponent form":      {`x := 1e3 / 4.0;`, 1e3 / 4.0},
		"underflows to zero": {`x := (1e-1000);`, 1e-1000},
	} {
		s := parseOne(t, tc.src)
		if s.lit == nil || s.lit.kind != argFloat {
			t.Errorf("%s: %q did not fold to a float literal: %+v", name, tc.src, s)
			continue
		}
		if s.lit.f != tc.want {
			t.Errorf("%s: %q folded to %v, the Go compiler folds it to %v", name, tc.src, s.lit.f, tc.want)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"concat chain": {`x := "a" + "-" + "b";`, "a" + "-" + "b"},
		"go escapes":   {`x := "\x41" + "B";`, "\x41" + "B"},
	} {
		s := parseOne(t, tc.src)
		if s.lit == nil || s.lit.kind != argString {
			t.Errorf("%s: %q did not fold to a string literal: %+v", name, tc.src, s)
			continue
		}
		if s.lit.str != tc.want {
			t.Errorf("%s: %q folded to %q, the Go compiler folds it to %q", name, tc.src, s.lit.str, tc.want)
		}
	}

	for name, tc := range map[string]struct {
		src  string
		want bool
	}{
		"ordering":        {`x := 1 < 2;`, 1 < 2},
		"string ordering": {`x := "x" < "y";`, "x" < "y"},
		"logic chain":     {`x := 10%3 == 1 && 7 > 2;`, 10%3 == 1 && 7 > 2},
		"negation":        {`x := !(1 > 2);`, !(1 > 2)},
		"or short form":   {`x := false || 3 >= 3;`, false || 3 >= 3},
	} {
		s := parseOne(t, tc.src)
		if s.lit == nil || s.lit.kind != argBool {
			t.Errorf("%s: %q did not fold to a bool literal: %+v", name, tc.src, s)
			continue
		}
		if s.lit.b != tc.want {
			t.Errorf("%s: %q folded to %v, the Go compiler folds it to %v", name, tc.src, s.lit.b, tc.want)
		}
	}
}

// TestGoExprFoldFaults pins the constant faults: the messages come
// from go/types itself, so division by zero and its relatives read
// exactly as the Go compiler reports them, and the two landing rules
// this language adds, int64 and float64 width, are named.
func TestGoExprFoldFaults(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"division by zero":        {`x := 1 / 0;`, "division by zero"},
		"float division by zero":  {`x := 1.0 / 0.0;`, "division by zero"},
		"remainder by zero":       {`x := 1 % 0;`, "division by zero"},
		"zero divisor expression": {`x := 10 / (5 - 5);`, "division by zero"},
		"mismatched constants":    {`x := "a" + 1;`, "mismatched types"},
		"plus on constant bools":  {`x := true + false;`, "not defined"},
		"negative shift":          {`x := 1 << -1;`, "negative shift count"},
		"int64 landing":           {`x := 1 << 70;`, "overflows int64"},
		"int64 negative landing":  {`x := -(1<<63) - 1;`, "overflows int64"},
		"float64 landing":         {`x := 10.0 * 1e308;`, "overflows float64"},
		"imaginary literal":       {`x := 2 + 3i;`, "imaginary literals are not in the language"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil {
			t.Errorf("%s: expected a parse error for %q", name, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the rule %q", name, err, tc.want)
		}
	}
}

// TestGoExprOperands covers what survives to the runtime operator: a
// bound name on either side, a folded constant on the other, and a
// named rejection for every leaf the language does not run.
func TestGoExprOperands(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		op    string
		xKind argKind
		yKind argKind
	}{
		"name plus name":     {`s := a + b;`, "+", argVar, argVar},
		"folded right":       {`m := n + 2*3;`, "+", argVar, argInt},
		"folded left":        {`m := 2*3 + n;`, "+", argInt, argVar},
		"folded string side": {`s := a + ("x" + "y");`, "+", argVar, argString},
		"folded bool side":   {`ok := a == (1 == 1);`, "==", argVar, argBool},
		"parens around all":  {`s := (a + b);`, "+", argVar, argVar},
		"parens around name": {`s := a + (b);`, "+", argVar, argVar},
		"unary in fold":      {`m := n + -2;`, "+", argVar, argInt},
	} {
		s := parseOne(t, tc.src)
		if s.binOp != tc.op || s.binX == nil || s.binY == nil {
			t.Errorf("%s: parsed op %q, want %q with two operands", name, s.binOp, tc.op)
			continue
		}
		if s.binX.kind != tc.xKind || s.binY.kind != tc.yKind {
			t.Errorf("%s: operand kinds %d,%d, want %d,%d", name, s.binX.kind, s.binY.kind, tc.xKind, tc.yKind)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"call operand":        {`m := urlOf() == n;`, "urlOf() cannot be an operand"},
		"field operand":       {`s := u.Path + "x";`, "u.Path cannot be an operand"},
		"nil operand":         {`ok := u == nil;`, "nil cannot be an operand"},
		"composite operand":   {`s := url.URL{} == url.URL{};`, "url.URL{} cannot be an operand"},
		"receive operand":     {`s := <-c + 1;`, "a receive cannot be an operand"},
		"index operand":       {`s := xs[0] + 1;`, "indexing is not in the grammar"},
		"nested runtime op":   {`s := a + b + c;`, "one operator per assignment at runtime"},
		"mixed nesting":       {`ok := a + b == c;`, "one operator per assignment at runtime"},
		"unary on a name":     {`x := -a;`, "unary - applies to constants only"},
		"not on a name":       {`ok := !done;`, "unary ! applies to constants only"},
		"single quotes":       {`s := 'a' + b;`, "single-quoted strings do not take operators"},
		"raw string":          {"s := `raw` + b;", "raw strings are not in the grammar"},
		"paren around name":   {`s := (a);`, "cannot assign a name to a name"},
		"runtime subtraction": {`s := a - b;`, "operator - runs only in a constant expression"},
		"runtime less than":   {`ok := a < b;`, "operator < runs only in a constant expression"},
		"runtime and":         {`ok := a && b;`, "operator && runs only in a constant expression"},
		"half constant and":   {`ok := 1 == 1 && a;`, "operator && runs only in a constant expression"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil {
			t.Errorf("%s: expected a parse error for %q", name, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the rule %q", name, err, tc.want)
		}
	}
}

// TestGoExprCapture covers the slice of source the router hands to
// go/parser: it stops at the statement end, ignores operators inside
// double-quoted strings, and an expression does not cross a line the
// way no statement does.
func TestGoExprCapture(t *testing.T) {
	// Operators inside a string are bytes, not structure.
	s := parseOne(t, `x := "a;b" + "+//c";`)
	if s.lit == nil || s.lit.str != "a;b"+"+//c" {
		t.Errorf("quoted separators leaked into the capture: %+v", s.lit)
	}

	// A second statement on the same line survives the capture.
	prog, err := (&Parser{}).Parse(`x := 2 * 3; y := x + 1;`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.stmts) != 2 || prog.stmts[0].lit == nil || prog.stmts[0].lit.i != 6 {
		t.Errorf("capture crossed the semicolon: %+v", prog.stmts)
	}

	// A comment closes the expression like the line end it runs to.
	s = parseOne(t, "x := 2 + 3 // folded\n")
	if s.lit == nil || s.lit.i != 5 {
		t.Errorf("capture crossed a comment: %+v", s.lit)
	}

	// The line end closes the statement before the operator arrives,
	// exactly as it does for every other statement.
	if _, err := (&Parser{}).Parse("x := (1 +\n2);"); err == nil {
		t.Error("an expression crossed a line end")
	}
}
