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
		// The body is the block every other construct reads, so the
		// statement kinds a block holds stand in it.
		`f(func(a) { if a { g(a) } else { h() } });`,
		`f(func(a) { for x := range a { g(x) } });`,
		`f(func() { var n int64; n++; g(n) });`,
		`f(func() { return g() });`,
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
		`f(func(a) { g()`:          "unterminated block",
		`f(func(a) { g() g() });`:  "expected ';' or end of line",
		`f(func() { break });`:     "break is only allowed inside a loop body",
		`f(func() { continue });`:  "continue is only allowed inside a loop body",
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

// TestFuncLitBodyDepth pins the loop-depth reset at the body
// boundary: a literal written inside a loop body still reads its own
// body as a program, so var and return stand in it and the loop's
// exits do not.
func TestFuncLitBodyDepth(t *testing.T) {
	good := []string{
		`for x := range xs { f(func() { var n int64; g(n) }) }`,
		`for x := range xs { f(func() { return g() }) }`,
	}
	for _, src := range good {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := map[string]string{
		`for x := range xs { f(func() { break }) }`: "break is only allowed inside a loop body",
		`for x := range xs { var n int64 }`:         "cannot stand inside a loop body",
	}
	for src, want := range bad {
		_, err := (&Parser{}).Parse(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", src, err, want)
		}
	}
}
