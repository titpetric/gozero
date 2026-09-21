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
		"step in arm":   {"n := 0\nif ok {\n\tn++\n}\n", 2},
		"return in arm": {"if ok {\n\treturn\n}\nf()\n", 2},
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
		"operator cond":    "if a == b {\n\tf()\n}\n",
		"cond composite":   "if u{Path: \"/\"} {\n\tf()\n}\n",
		"unterminated arm": "if ok { f() g() }",
	} {
		if _, err := (&Parser{}).Parse(src); err == nil {
			t.Errorf("%s: expected a parse error for %q", name, src)
		}
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

// TestParseIfConds pins the condition forms the header reads: a call
// keeps its argument list and chain, a dotted path stays a path.
func TestParseIfConds(t *testing.T) {
	prog, err := (&Parser{}).Parse("if strings.HasPrefix(p, \"/api\") {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond := prog.stmts[0].ifs.cond
	if cond.kind != argCall || strings.Join(cond.sub.path, ".") != "strings.HasPrefix" {
		t.Fatalf("cond = %+v", cond)
	}

	prog, err = (&Parser{}).Parse("if req.Close {\n\tf()\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cond = prog.stmts[0].ifs.cond
	if cond.kind != argPath || strings.Join(cond.path, ".") != "req.Close" {
		t.Fatalf("cond = %+v", cond)
	}
}
