package gozero

import (
	"strings"
	"testing"
)

// TestDynCallForms covers calling func-typed values: plain, variadic
// packed and spread, and nil.
func TestDynCallForms(t *testing.T) {
	rt := exprRuntime(t)
	src := `join := func(sep string, parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out = out + sep
		}
		out = out + p
	}
	return out
}
a := join("-", "a", "b", "c")
xs := fields("x y")
b := join("+", xs...)
return a + "|" + b`
	got, err := rt.Eval[string](src, nil)
	if err != nil || got != "a-b-c|x+y" {
		t.Fatalf("got %q, %v", got, err)
	}

	if _, err := rt.Eval[any]("var f func(int64) int64\nv := f(1)\nreturn v", nil); err == nil || !strings.Contains(err.Error(), "nil, not a function") {
		t.Fatalf("nil call: %v", err)
	}
}
