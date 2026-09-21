package gozero

import (
	"strings"
	"testing"
)

// TestParseCmpPlacement pins the placement rule: the comparison
// operators exist only in a condition header, and every statement
// position rejects them by name.
func TestParseCmpPlacement(t *testing.T) {
	for name, src := range map[string]string{
		"assign rhs":  "y := x == 5\n",
		"literal lhs": "y := 5 == x\n",
		"bare":        "x == 5\n",
		"bare order":  "x < 5\n",
		"bare path":   "r.n >= 5\n",
		"return":      "ok := f()\nreturn ok == true\n",
	} {
		_, err := (&Parser{}).Parse(src)
		if err == nil || !strings.Contains(err.Error(), "comparison placement") {
			t.Errorf("%s: err = %v, want it to name the placement rule", name, err)
		}
	}
}

// TestParseCmpShape checks the tree a header comparison builds: the
// operator, and each operand keeping its own form.
func TestParseCmpShape(t *testing.T) {
	prog, err := (&Parser{}).Parse("if time.Since(t) < time.Hour {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	is := prog.stmts[0].ifs
	if is.cmp == nil || is.cond.kind != argString || is.cond.str != "" {
		t.Fatalf("cmp = %+v, cond = %+v", is.cmp, is.cond)
	}
	if is.cmp.op != "<" {
		t.Fatalf("op = %q", is.cmp.op)
	}
	if is.cmp.lhs.kind != argCall || strings.Join(is.cmp.lhs.sub.path, ".") != "time.Since" {
		t.Fatalf("lhs = %+v", is.cmp.lhs)
	}
	if is.cmp.rhs.kind != argPath || strings.Join(is.cmp.rhs.path, ".") != "time.Hour" {
		t.Fatalf("rhs = %+v", is.cmp.rhs)
	}
}

// TestParseCmpArrow pins the one ambiguity the operator reader has:
// '<' followed by '-' is the channel arrow, so a send statement and a
// receive still parse with the comparison operators in the grammar.
func TestParseCmpArrow(t *testing.T) {
	for name, src := range map[string]string{
		"send":  "c <- 1\n",
		"recv":  "v := <-c\n",
		"bare":  "<-c\n",
		"in if": "if ok {\n\tc <- 1\n}\n",
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
