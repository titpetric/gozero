package gozero

import (
	"strings"
	"testing"
)

// TestParseIf pins the accepted if shapes: the three condition forms,
// the else and else-if chain, nesting, and blocks whose statements
// close on a brace, a semicolon or a line end.
func TestParseIf(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		stmts int
	}{
		"name cond":     {"ok := f()\nif ok {\n\tg()\n}\n", 2},
		"path cond":     {"r := f()\nif r.Close {\n\tg()\n}\n", 2},
		"call cond":     {"if f(x) {\n\tg()\n}\n", 1},
		"else":          {"if ok {\n\tf()\n} else {\n\tg()\n}\n", 1},
		"else if":       {"if a {\n\tf()\n} else if b {\n\tg()\n} else {\n\th()\n}\n", 1},
		"nested":        {"if a {\n\tif b {\n\t\tf()\n\t}\n}\n", 1},
		"empty arms":    {"if ok {\n} else {\n}\n", 1},
		"one line":      {"if ok { f() } else { g() }", 1},
		"semicolons":    {"if ok { f(); g(); }", 1},
		"after if":      {"if ok {\n\tf()\n}\ng()\n", 2},
		"assign in arm": {"s := \"\"\nif ok {\n\ts = \"x\"\n}\n", 2},
		"cmp names":     {"if a == b {\n\tf()\n}\n", 1},
		"cmp literal":   {"if status == 200 {\n\tf()\n}\n", 1},
		"cmp lit left":  {"if 200 == status {\n\tf()\n}\n", 1},
		"cmp string":    {"if m == \"GET\" {\n\tf()\n}\n", 1},
		"cmp call":      {"if time.Since(t) < time.Hour {\n\tf()\n}\n", 1},
		"cmp field":     {"if req.Method != \"GET\" {\n\tf()\n}\n", 1},
		"cmp negative":  {"if n < -1 {\n\tf()\n}\n", 1},
		"cmp ge":        {"if n >= 500 {\n\tf()\n}\n", 1},
		"and":           {"if a && b {\n\tf()\n}\n", 1},
		"or chain":      {"if a || b || c {\n\tf()\n}\n", 1},
		"not":           {"if !ok {\n\tf()\n}\n", 1},
		"not not":       {"if !!ok {\n\tf()\n}\n", 1},
		"not call":      {"if !f(x) {\n\tf()\n}\n", 1},
		"not paren":     {"if !(a == b) {\n\tf()\n}\n", 1},
		"paren name":    {"if (ok) {\n\tf()\n}\n", 1},
		"paren mix":     {"if (a || b) && c {\n\tf()\n}\n", 1},
		"arith":         {"if n + 1 > m * 2 {\n\tf()\n}\n", 1},
		"arith paren":   {"if (n + 1) * 2 >= lim {\n\tf()\n}\n", 1},
		"arith mod":     {"if n % 2 == 0 {\n\tf()\n}\n", 1},
		"arith div":     {"if total / count < 10 {\n\tf()\n}\n", 1},
		"arith neg lit": {"if n - -1 > 0 {\n\tf()\n}\n", 1},
		"cmp and cmp":   {"if a == b && c != d {\n\tf()\n}\n", 1},
		"trailing op":   {"if a &&\n\tb {\n\tf()\n}\n", 1},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(prog.stmts) != tc.stmts {
			t.Errorf("%s: %d statements, want %d", name, len(prog.stmts), tc.stmts)
		}
	}

	for name, src := range map[string]string{
		"no block":         "if ok f()\n",
		"no cond":          "if {\n\tf()\n}\n",
		"unterminated":     "if ok {\n\tf()\n",
		"stray else":       "else {\n\tf()\n}\n",
		"literal cond":     "if 5 {\n\tf()\n}\n",
		"else next line":   "if ok {\n\tf()\n}\nelse {\n\tg()\n}\n",
		"cond composite":   "if u{Path: \"/\"} {\n\tf()\n}\n",
		"unterminated arm": "if ok { f() g() }",
		"cmp no rhs":       "if a == {\n\tf()\n}\n",
		"cmp arrow":        "if a <-1 {\n\tf()\n}\n",
		"and no rhs":       "if a && {\n\tf()\n}\n",
		"bang alone":       "if ! {\n\tf()\n}\n",
		"unclosed paren":   "if (a || b {\n\tf()\n}\n",
		"leading op":       "if a\n\t&& b {\n\tf()\n}\n",
	} {
		if _, err := (&Parser{}).Parse(src); err == nil {
			t.Errorf("%s: expected a parse error for %q", name, src)
		}
	}
}

// TestParseCmpPlacement pins the placement rule: the comparison
// operators exist only in an if header, and every statement position
// rejects them by name.
func TestParseCmpPlacement(t *testing.T) {
	for name, src := range map[string]string{
		"assign rhs":  "y := x == 5\n",
		"literal lhs": "y := 5 == x\n",
		"bare":        "x == 5\n",
		"bare order":  "x < 5\n",
		"return":      "ok := f()\nreturn ok == true\n",
	} {
		_, err := (&Parser{}).Parse(src)
		if err == nil || !strings.Contains(err.Error(), "comparison placement") {
			t.Errorf("%s: err = %v, want it to name the placement rule", name, err)
		}
	}
}

// TestParseCmpShape checks the tree a header comparison builds: the
// operator, and each operand keeping its own form as a leaf.
func TestParseCmpShape(t *testing.T) {
	prog, err := (&Parser{}).Parse("if time.Since(t) < time.Hour {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ce := prog.stmts[0].ifs.cond
	if ce.op != "<" {
		t.Fatalf("op = %q", ce.op)
	}
	if ce.x.op != "" || ce.x.leaf.kind != argCall || strings.Join(ce.x.leaf.sub.path, ".") != "time.Since" {
		t.Fatalf("lhs = %+v", ce.x)
	}
	if ce.y.op != "" || ce.y.leaf.kind != argPath || strings.Join(ce.y.leaf.path, ".") != "time.Hour" {
		t.Fatalf("rhs = %+v", ce.y)
	}
}

// TestParseHeaderShape pins Go precedence in the header tree: || is
// the loosest, then &&, then a comparison, then + -, then * / %,
// with ! and parentheses binding tightest.
func TestParseHeaderShape(t *testing.T) {
	prog, err := (&Parser{}).Parse("if a && b || !c {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ce := prog.stmts[0].ifs.cond
	if ce.op != "||" || ce.x.op != "&&" || ce.y.op != "!" {
		t.Fatalf("tree = %+v", ce)
	}
	if ce.x.x.leaf.str != "a" || ce.x.y.leaf.str != "b" || ce.y.x.leaf.str != "c" {
		t.Fatalf("leaves = %+v %+v %+v", ce.x.x, ce.x.y, ce.y.x)
	}

	prog, err = (&Parser{}).Parse("if n + 1 > m * 2 {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ce = prog.stmts[0].ifs.cond
	if ce.op != ">" || ce.x.op != "+" || ce.y.op != "*" {
		t.Fatalf("tree = %+v", ce)
	}
	if !ce.x.y.lit || ce.x.y.leaf.i != 1 || ce.y.x.leaf.str != "m" {
		t.Fatalf("operands = %+v %+v", ce.x.y, ce.y.x)
	}

	// Parentheses override: (a || b) && c roots at &&.
	prog, err = (&Parser{}).Parse("if (a || b) && c {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ce = prog.stmts[0].ifs.cond
	if ce.op != "&&" || ce.x.op != "||" {
		t.Fatalf("tree = %+v", ce)
	}

	// Left association: a - b - c is (a - b) - c.
	prog, err = (&Parser{}).Parse("if a - b - c > 0 {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ce = prog.stmts[0].ifs.cond
	if ce.x.op != "-" || ce.x.x.op != "-" || ce.x.y.leaf.str != "c" {
		t.Fatalf("tree = %+v", ce.x)
	}
}

// TestParseIfShape checks the tree an else-if chain builds: the else
// arm holds a single statement that is itself an if.
func TestParseIfShape(t *testing.T) {
	prog, err := (&Parser{}).Parse("if a {\n\tf()\n} else if b {\n\tg()\n} else {\n\th()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	is := prog.stmts[0].ifs
	if is == nil {
		t.Fatal("no if statement")
	}
	if is.cond.op != "" || is.cond.leaf.kind != argVar || is.cond.leaf.str != "a" {
		t.Fatalf("cond = %+v", is.cond)
	}
	if len(is.then) != 1 || len(is.els) != 1 {
		t.Fatalf("then %d, els %d statements", len(is.then), len(is.els))
	}
	sub := is.els[0].ifs
	if sub == nil {
		t.Fatal("the else arm does not nest an if")
	}
	if sub.cond.op != "" || sub.cond.leaf.kind != argVar || sub.cond.leaf.str != "b" {
		t.Fatalf("nested cond = %+v", sub.cond)
	}
	if len(sub.els) != 1 {
		t.Fatalf("nested els %d statements", len(sub.els))
	}
}

// TestParseIfConds pins the condition forms the header reads: a call
// keeps its argument list and chain, a dotted path stays a path.
func TestParseIfConds(t *testing.T) {
	prog, err := (&Parser{}).Parse("if strings.HasPrefix(p, \"/api\") {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond := prog.stmts[0].ifs.cond.leaf
	if cond.kind != argCall || strings.Join(cond.sub.path, ".") != "strings.HasPrefix" {
		t.Fatalf("cond = %+v", cond)
	}

	prog, err = (&Parser{}).Parse("if req.Close {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond = prog.stmts[0].ifs.cond.leaf
	if cond.kind != argPath || strings.Join(cond.path, ".") != "req.Close" {
		t.Fatalf("cond = %+v", cond)
	}
}

// TestParseOperatorPlacement pins the placement rule for the L3
// operators: composition and arithmetic exist only in an if header,
// and every statement position rejects them by name.
func TestParseOperatorPlacement(t *testing.T) {
	for name, src := range map[string]string{
		"assign plus":  "y := x + 5\n",
		"assign minus": "y := x - 1\n",
		"assign mul":   "y := x * 2\n",
		"assign and":   "y := a && b\n",
		"assign or":    "y := a || b\n",
		"bare arith":   "x % 2\n",
		"bare literal": "y := 5 - 3\n",
		"return arith": "n := f()\nreturn n + 1\n",
	} {
		_, err := (&Parser{}).Parse(src)
		if err == nil || !strings.Contains(err.Error(), "operator placement") {
			t.Errorf("%s: err = %v, want it to name the placement rule", name, err)
		}
	}
}
