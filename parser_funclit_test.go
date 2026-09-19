package gozero

import (
	"strings"
	"testing"
)

func TestFuncLitParses(t *testing.T) {
	good := []string{
		`f(func() { g() });`,
		`f(func(a) { g(a) });`,
		`f(func(a, b) { g(a); g(b) });`,
		"f(func(a, b) {\n\tg(a)\n\tg(b)\n});",
		`f(func() { });`,
		`f("x", func(a) { g(a) }, "y");`,
		`f(func(a) { g(func() { h() }) });`,
		// The statement forms the body shares with a program.
		`f(func(c) { c <- "x"; v := <-c; g(v) });`,
		`f(func() { var c chan string; g() });`,
		`f(func(r) { r.Method = "GET" });`,
		`f(func() { u := url.URL{Path: "/"}; g(u) });`,
		`f(func() { g(&url.URL{Path: "/"}) });`,
		`f(func(xs) { g(xs...) });`,
		`f(func() { a, b := g(); h(a, b) });`,
		`f(func(r) { g(r.URL.String()) });`,
		`f(func() { g(h().String()) });`,
		`f(func() { return "x" });`,
		`f(func() { g(-1, -2.5) });`,
		// go/parser owns the literal's syntax, so Go spellings the
		// byte-level grammar does not know work in a body for free.
		"f(func() { g(`raw`) });",
		`f(func() { g(0x1F, 1_000) });`,
		`f(func() { g(("x")) });`,
		`f(func(a, b,) { g(a) });`,
		"f(func() {\n\t// a Go comment\n\tg()\n});",
		`f(func() { g('a') });`,
	}
	for _, src := range good {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := map[string]string{
		`f(func);`: "func starts a literal",
		// Mixing a typed and an untyped parameter is go/parser's own
		// error; a uniformly typed list reaches the lowering's rule.
		`f(func(a http.ResponseWriter, r) { g() });`: "missing parameter type",
		`f(func(a http) { g() });`:                   "parameter types come from the target signature",
		`f(func(a) g());`:                            "expected '{' to open the func literal body",
		`f(func() string { g() });`:                  "expected '{' to open the func literal body",
		`f(func(a) { g()`:                            "unterminated func literal body",
		// go/parser reports the body's Go syntax errors itself.
		`f(func(a) { g() g() });`: "expected ';'",
		`f(func(a) { g('ok') });`: "rune literal",
		// Go statements outside the grammar parse and are rejected by
		// name in the lowering.
		`f(func(a) { if a { g() } });`: "an if statement is not in the statement grammar",
		`f(func(a) { for { g() } });`:  "a for loop is not in the statement grammar",
		`f(func(a) { go g() });`:       "a go statement is not in the statement grammar",
		`f(func(a) { defer g() });`:    "a defer statement is not in the statement grammar",
		`f(func(a) { a++ });`:          "an increment statement is not in the statement grammar",
		`f(func(a) { g(a + 1) });`:     "the grammar has no operators",
		`f(func(a) { a += 1 });`:       "the grammar has no operators",
		`f(func(a) { g(a[0]) });`:      "is not in the argument grammar",
		`f(func() { var x = 1 });`:     "an initial value is an assignment",
		`func(a) { g(a) };`:            "", // any error will do: statement position is not in the grammar
	}
	for src, want := range bad {
		_, err := (&Parser{}).Parse(src)
		if err == nil {
			t.Errorf("%s: parsed, want error", src)
			continue
		}
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", src, err, want)
		}
	}
}

