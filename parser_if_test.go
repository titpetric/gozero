package gozero

import (
	"strings"
	"testing"
)

// TestParseIf pins the accepted if shapes: the three condition forms,
// the else and else-if chain, nesting, and blocks whose statements
// close on a brace, a semicolon or a line end. The last group only
// parses because go/parser reads the header: parenthesized
// conditions, Go's numeric spellings, raw strings and an argument
// list broken across lines are not in the hand-rolled line grammar.
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

		"paren cond":     {"if (ok) {\n\tf()\n}\n", 1},
		"paren cmp":      {"if (a == b) {\n\tf()\n}\n", 1},
		"hex literal":    {"if n == 0xFF {\n\tf()\n}\n", 1},
		"grouped digits": {"if n == 1_000 {\n\tf()\n}\n", 1},
		"raw string":     {"if m == `GET` {\n\tf()\n}\n", 1},
		"escaped hex":    {"if m == \"\\x41\" {\n\tf()\n}\n", 1},
		"args cross":     {"if f(a,\n\tb) {\n\tg()\n}\n", 1},
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
	} {
		if _, err := (&Parser{}).Parse(src); err == nil {
			t.Errorf("%s: expected a parse error for %q", name, src)
		}
	}
}

// TestParseIfRungRules pins the named rejections of the header walk.
// go/parser accepts the whole of Go's expression grammar, so the walk
// is where the rung narrows it, and each rejection names its rule the
// way the hand-rolled rungs do.
func TestParseIfRungRules(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"and":          {"if a && b {\n\tf()\n}\n", "composition"},
		"or":           {"if a || b {\n\tf()\n}\n", "composition"},
		"not":          {"if !ok {\n\tf()\n}\n", "composition"},
		"arith":        {"if a+1 == 2 {\n\tf()\n}\n", "arithmetic"},
		"arith cond":   {"if a*b {\n\tf()\n}\n", "arithmetic"},
		"bit and":      {"if a&b == 0 {\n\tf()\n}\n", "arithmetic"},
		"chained cmp":  {"if a > b > c {\n\tf()\n}\n", "comparison operand"},
		"paren cmp op": {"if (a > b) == ok {\n\tf()\n}\n", "comparison operand"},
		"rune literal": {"if c == 'a' {\n\tf()\n}\n", "rune literal"},
		"split header": {"if ok\n{\n\tf()\n}\n", "share a line"},
		"index":        {"if xs[0] {\n\tf()\n}\n", "not an if header operand"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}

// TestParseHeaderErr pins the error translation: a header go/parser
// rejects surfaces as one parse error whose offset points into the
// program source, not the captured slice.
func TestParseHeaderErr(t *testing.T) {
	_, err := (&Parser{}).Parse("f()\nif a == {\n\tg()\n}\n")
	if err == nil || !strings.Contains(err.Error(), "if header") {
		t.Fatalf("err = %v, want an if header error", err)
	}
	if !strings.Contains(err.Error(), "offset 1") {
		t.Fatalf("err = %v, want an offset past the first statement", err)
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
	if is.cond.kind != argVar || is.cond.str != "a" {
		t.Fatalf("cond = %+v", is.cond)
	}
	if len(is.then) != 1 || len(is.els) != 1 {
		t.Fatalf("then %d, els %d statements", len(is.then), len(is.els))
	}
	sub := is.els[0].ifs
	if sub == nil {
		t.Fatal("the else arm does not nest an if")
	}
	if sub.cond.kind != argVar || sub.cond.str != "b" {
		t.Fatalf("nested cond = %+v", sub.cond)
	}
	if len(sub.els) != 1 {
		t.Fatalf("nested els %d statements", len(sub.els))
	}
}

// TestParseIfConds pins the condition forms the header walk reads: a
// call keeps its argument list, a chain stays a chain, a dotted path
// stays a path, and a spread survives the walk.
func TestParseIfConds(t *testing.T) {
	prog, err := (&Parser{}).Parse("if strings.HasPrefix(p, \"/api\") {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond := prog.stmts[0].ifs.cond
	if cond.kind != argCall || strings.Join(cond.sub.path, ".") != "strings.HasPrefix" {
		t.Fatalf("cond = %+v", cond)
	}
	if len(cond.sub.args) != 2 || cond.sub.args[1].str != "/api" {
		t.Fatalf("args = %+v", cond.sub.args)
	}

	prog, err = (&Parser{}).Parse("if req.Close {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond = prog.stmts[0].ifs.cond
	if cond.kind != argPath || strings.Join(cond.path, ".") != "req.Close" {
		t.Fatalf("cond = %+v", cond)
	}

	prog, err = (&Parser{}).Parse("if f(x).Ok() {\n\tg()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond = prog.stmts[0].ifs.cond
	if cond.kind != argCall || len(cond.sub.chain) != 1 || cond.sub.chain[0].name != "Ok" {
		t.Fatalf("cond = %+v", cond)
	}

	prog, err = (&Parser{}).Parse("if f(xs...) {\n\tg()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond = prog.stmts[0].ifs.cond
	if cond.kind != argCall || len(cond.sub.args) != 1 || !cond.sub.args[0].spread {
		t.Fatalf("cond = %+v", cond)
	}
}
