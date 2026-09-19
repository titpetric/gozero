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
	}
	for _, src := range good {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := map[string]string{
		`f(func);`:                 "func starts a literal",
		`f(func(a http) { g() });`: "expected ',' or ')' in the parameter list",
		`f(func(a,) { g() });`:     "expected a parameter name",
		`f(func(a) g());`:          "expected '{'",
		`f(func(a) { g()`:          "unterminated func literal body",
		`f(func(a) { g() g() });`:  "expected ';' or end of line",
		`func(a) { g(a) };`:        "", // any error will do: statement position is not in the grammar
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
