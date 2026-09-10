package gozero

import (
	"strings"
	"testing"
)

// TestParseIf covers the if forms: bare, else, else-if chains, and
// the header rules.
func TestParseIf(t *testing.T) {
	prog, err := (&Parser{}).Parse(`if a > 0 { f() };`)
	if err != nil {
		t.Fatal(err)
	}
	is := prog.stmts[0].ifs
	if is == nil || is.cond.op != ">" || len(is.then.stmts) != 1 || is.els != nil {
		t.Fatalf("if = %+v", is)
	}

	prog, err = (&Parser{}).Parse("if a > 5 { f() } else if a > 0 { g() } else { h() }\nx := 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.stmts) != 2 {
		t.Fatalf("stmts = %d", len(prog.stmts))
	}
	is = prog.stmts[0].ifs
	if is.els == nil || len(is.els.stmts) != 1 {
		t.Fatalf("els = %+v", is.els)
	}
	inner := is.els.stmts[0].ifs
	if inner == nil || inner.els == nil {
		t.Fatalf("else-if = %+v", inner)
	}

	// else must share the closing brace's line.
	if _, err := (&Parser{}).Parse("if a > 0 { f() }\nelse { g() }"); err == nil {
		t.Error("else on its own line must fail")
	}
	// A bare composite literal cannot stand in a header.
	if _, err := (&Parser{}).Parse(`if u == url.URL{} { f() };`); err == nil {
		t.Error("a composite literal in a header must fail")
	}
	// Parenthesised it is fine, and so is one inside call arguments.
	if _, err := (&Parser{}).Parse(`if eq(u, url.URL{Path: "/"}) { f() };`); err != nil {
		t.Errorf("composite in call args: %v", err)
	}
}

// TestParseFor covers the four loop forms and the clause grammar.
func TestParseFor(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		check func(f *forStmt) bool
	}{
		"bare":      {`for { f() };`, func(f *forStmt) bool { return f.cond == nil && f.rangeX == nil && f.init == nil }},
		"cond":      {`for a < 3 { f() };`, func(f *forStmt) bool { return f.cond != nil && f.cond.op == "<" }},
		"three":     {`for i := 0; i < 3; i++ { f() };`, func(f *forStmt) bool { return f.init != nil && f.cond != nil && f.post != nil }},
		"no init":   {`for ; a < 3; a++ { f() };`, func(f *forStmt) bool { return f.init == nil && f.cond != nil && f.post != nil }},
		"no post":   {`for i := 0; i < 3; { f() };`, func(f *forStmt) bool { return f.init != nil && f.post == nil }},
		"range kv":  {`for k, v := range xs { f() };`, func(f *forStmt) bool { return f.rangeX != nil && f.key == "k" && f.val == "v" }},
		"range k":   {`for i := range xs { f() };`, func(f *forStmt) bool { return f.rangeX != nil && f.key == "i" && f.val == "" }},
		"range _":   {`for _, v := range xs { f() };`, func(f *forStmt) bool { return f.key == "_" && f.val == "v" }},
		"range val": {`for range xs { f() };`, func(f *forStmt) bool { return f.rangeX != nil && f.key == "" && f.val == "" }},
		"post call": {`for i := 0; i < 3; step() { f() };`, func(f *forStmt) bool { return f.post != nil && f.post.call != nil }},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		f := prog.stmts[0].fors
		if f == nil || !tc.check(f) {
			t.Errorf("%s: for = %+v", name, f)
		}
	}
}

// TestParseIncDec pins the desugaring: i++ is the assignment
// i = i + 1, on names and on field paths.
func TestParseIncDec(t *testing.T) {
	prog, err := (&Parser{}).Parse(`i++;`)
	if err != nil {
		t.Fatal(err)
	}
	s := prog.stmts[0]
	if len(s.lhs) != 1 || s.lit == nil || s.lit.op != "+" || s.lit.y.i != 1 {
		t.Fatalf("i++ = %+v", s)
	}
	prog, err = (&Parser{}).Parse(`b.N--;`)
	if err != nil {
		t.Fatal(err)
	}
	s = prog.stmts[0]
	if s.fieldLhs == nil || s.lit.op != "-" {
		t.Fatalf("b.N-- = %+v", s)
	}
}

// TestParseBlockRejects pins the block-level failures.
func TestParseBlockRejects(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"unterminated": {`if a > 0 { f()`, "unterminated block"},
		"no brace":     {`if a > 0 f();`, "expected '{'"},
		"bad header":   {`for i := 0 i < 3 { f() };`, ""},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
