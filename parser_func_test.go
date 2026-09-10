package gozero

import (
	"strings"
	"testing"
)

// TestParseFuncDecl covers declarations: plain, receivers, results,
// init, and the literal form.
func TestParseFuncDecl(t *testing.T) {
	src := `func add(a, b int64) int64 { return a + b }
func (p Point) Get() int64 { return p.X }
func (p *Point) Set(v int64) { p.X = v }
func pair() (int64, error) { return 1, nil }
func init() { setup() }
x := 1;`
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.funcs) != 5 || len(prog.stmts) != 1 {
		t.Fatalf("funcs = %d, stmts = %d", len(prog.funcs), len(prog.stmts))
	}
	add := prog.funcs[0]
	if add.name != "add" || len(add.params) != 2 || add.params[0].typ != "int64" || add.results[0].typ != "int64" {
		t.Fatalf("add = %+v", add)
	}
	get := prog.funcs[1]
	if get.recv == nil || get.recv.name != "p" || get.recv.typ != "Point" {
		t.Fatalf("get = %+v", get)
	}
	set := prog.funcs[2]
	if set.recv.typ != "*Point" {
		t.Fatalf("set = %+v", set)
	}
	pair := prog.funcs[3]
	if len(pair.results) != 2 || pair.results[1].typ != "error" {
		t.Fatalf("pair = %+v", pair)
	}
	if prog.funcs[4].name != "init" {
		t.Fatalf("init = %+v", prog.funcs[4])
	}

	// A literal parses in value position, and a multi-value return
	// inside a body.
	prog, err = (&Parser{}).Parse(`f := func(n int64) (int64, error) { return n, nil }; g := f(1); return g;`)
	if err != nil {
		t.Fatal(err)
	}
	lit := prog.stmts[0].lit
	if lit == nil || lit.kind != argFuncLit || len(lit.fn.params) != 1 {
		t.Fatalf("lit = %+v", lit)
	}
	if rets := lit.fn.body.stmts[0].rets; len(rets) != 2 {
		t.Fatalf("rets = %+v", rets)
	}

	// if with an init clause.
	prog, err = (&Parser{}).Parse(`if v, err := f(); err == nil { g(v) };`)
	if err != nil {
		t.Fatal(err)
	}
	is := prog.stmts[0].ifs
	if is.init == nil || len(is.init.lhs) != 2 {
		t.Fatalf("if init = %+v", is)
	}
}

// TestParseFuncRejects pins the failure modes.
func TestParseFuncRejects(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"no body":   {`func f() int64;`, "expected '{'"},
		"bad recv":  {`func (p Point Get() int64 { return 1 };`, "expected ')' after the receiver"},
		"no parens": {`func f { return 1 };`, "expected '(' after the function name"},
	} {
		if _, err := (&Parser{}).Parse(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
