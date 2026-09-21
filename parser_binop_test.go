package gozero

import (
	"strings"
	"testing"
)

// TestParser_BinOp covers the operator assignment: exactly one of +,
// == or != between two values on the right of := or =, and a named
// rejection everywhere else an operator could try to stand.
func TestParser_BinOp(t *testing.T) {
	for name, tc := range map[string]struct {
		src string
		op  string
	}{
		"concat":            {`s := a + b;`, "+"},
		"concat literal":    {`s := a + "x";`, "+"},
		"literal first":     {`s := "x" + a;`, "+"},
		"add":               {`m := n + 2;`, "+"},
		"negative literal":  {`m := n + -2;`, "+"},
		"equal":             {`ok := a == b;`, "=="},
		"not equal":         {`ok := a != b;`, "!="},
		"plain assign":      {`ok = a == b;`, "=="},
		"no spaces":         {`ok:=a==b;`, "=="},
		"no semicolon":      {"s := a + b\n", "+"},
		"bool literal side": {`ok := a == true;`, "=="},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s := prog.stmts[0]
		if s.binOp != tc.op || s.binX == nil || s.binY == nil {
			t.Errorf("%s: parsed op %q with x=%v y=%v, want %q with both operands", name, s.binOp, s.binX, s.binY, tc.op)
		}
	}

	// An operator does not cross a newline: the line end closed the
	// statement, so "s := a" is a name assigned to a name, which has
	// its own error.
	if _, err := (&Parser{}).Parse("s := a\n+ b;"); err == nil {
		t.Error("an operator on the next line should not join the assignment above it")
	}

	for name, tc := range map[string]struct {
		src, want string
	}{
		"nested":            {`s := a + b + c;`, "one operator per assignment"},
		"mixed nesting":     {`ok := a + b == c;`, "one operator per assignment"},
		"parenthesized":     {`s := (a + b);`, "parentheses do not group a value"},
		"paren operand":     {`s := a + (b);`, "parentheses do not group a value"},
		"subtraction":       {`s := a - b;`, "operator - is not in the grammar"},
		"multiplication":    {`s := a * b;`, "operator * is not in the grammar"},
		"logical and":       {`ok := a && b;`, "operator && is not in the grammar"},
		"less than":         {`ok := a < b;`, "comparison placement"},
		"bare":              {`a == b;`, "an operator expression cannot be a statement"},
		"argument":          {`f(a + b);`, "an operator expression cannot be an argument"},
		"return value":      {`return a + b;`, "an operator expression cannot be returned"},
		"returned compare":  {`return a == b;`, "an operator expression cannot be returned"},
		"field value":       {`u.Path = a + b;`, "an operator expression cannot be assigned to a field"},
		"send value":        {`c <- a + b;`, "an operator expression cannot be sent"},
		"composite element": {`u := url.URL{Path: a + b};`, "an operator expression cannot be an element"},
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
